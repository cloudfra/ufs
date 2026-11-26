// Copyright 2026 Cloudfra
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !wasm

package boltfs

// BatchCreate with the batched transaction runner only it uses is the [types.BatchCreateFS] half, which [types.WriteFS] does not ask for but this driver answers.

import (
	"errors"
	"fmt"
	"io/fs"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// BatchCreate writes files in one transaction, using bolt.DB.Batch so that
// the calls of concurrent goroutines share a transaction. A call whose shared
// transaction fails is retried by itself, so it fails only itself; a call
// waits at most batchMaxDelay for company before its transaction starts.
//
// Names are validated and every record encoded before the transaction opens,
// so the transaction itself only writes, and watchers are notified once it has
// committed.
//
// An entry whose name already holds a file replaces it, as Create does, and a
// watcher is told NotifyWrite for it rather than NotifyCreate. An entry whose
// name holds a directory — empty or not — fails the way Create does, and so do
// two entries that spell one path as a file and as a directory, in either
// order: the transaction is refused, so none of the call is written, not even
// the parent directories it made on the way. TestBoltBatchCreateOverwrite and
// TestBoltBatchCreateDescendantName pin those cases.
func (fsys *boltFS) BatchCreate(files []types.BatchFile) error {
	// Names are checked before the transaction opens, so a bad entry cannot
	// leave the entries before it written.
	if err := types.ValidateBatchFiles(batchCreateOp, files); err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}

	// The records are encoded before the transaction opens rather than inside
	// it: bbolt may run the transaction function more than once, so a retry
	// repeats only the writes.
	now := time.Now()
	records := make([][]byte, len(files))
	for i := range files {
		file := &files[i]
		perm := file.Perm.Perm()
		if perm == 0 {
			perm = fs.ModePerm
		}
		record, err := encodeBoltRecord(perm, now, file.Data)
		if err != nil {
			return ufserrors.NewPathError(batchCreateOp, file.Name, err)
		}
		records[i] = record
	}

	// events holds what the transaction changed, and is notified only once it
	// has committed. It is emptied at the start of the transaction because
	// bbolt may run that function again after refusing it, and the changes of
	// a refused attempt are not changes at all.
	events := make([]batchEvent, 0, len(files))
	err := fsys.batch(func(tx *bolt.Tx) error {
		events = events[:0]
		created := func(dir string) {
			events = append(events, batchEvent{types.NotifyCreate, dir})
		}
		for i := range files {
			file := &files[i]
			bkt, key, err := parentBucketCreating(tx, file.Name, created)
			if err != nil {
				return ufserrors.NewPathError(batchCreateOp, file.Name, err)
			}
			if bkt.Bucket(key) != nil {
				return ufserrors.NewPathError(batchCreateOp, file.Name, fmt.Errorf("%w: %w", errIsDirectory, fs.ErrInvalid))
			}
			op := types.NotifyCreate
			if bkt.Get(key) != nil {
				op = types.NotifyWrite
			}
			if err := bkt.Put(key, records[i]); err != nil {
				return ufserrors.NewPathError(batchCreateOp, file.Name, err)
			}
			events = append(events, batchEvent{op, file.Name})
		}
		return nil
	})
	if err != nil {
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			return err
		}
		return ufserrors.NewPathError(batchCreateOp, files[0].Name, err)
	}
	for _, e := range events {
		fsys.notify(e.op, e.path)
	}
	return nil
}

// batchEvent is a change that a batch makes, reported once it is committed.
type batchEvent struct {
	op   types.NotifyOp
	path string
}
