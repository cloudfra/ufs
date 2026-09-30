// Copyright 2026 Jeremy Edwards
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

import (
	"fmt"
	"io/fs"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	pb "github.com/cloudfra/ufs/proto"
)

// batchEvent is a notification to publish once a batch commits.
type batchEvent struct {
	op   ufs.NotifyOp
	path string
}

// BatchWrite applies entries in order in a single bolt transaction: either
// every entry is applied or, on any error, none is. It satisfies the
// writelog.BatchWriter interface.
//
//   - OP_PUT writes the entry's content, mode and mod_time to its file,
//     creating missing parent directories.
//   - OP_MKDIR creates the directory and missing parents with the entry's
//     permission bits and mod_time.
//   - OP_REMOVE_ALL removes the path and everything below it; a missing path
//     succeeds.
//
// Watchers are notified of every change after the transaction commits. A
// failure returns an *fs.PathError with op "batchwrite" naming the entry that
// failed.
func (fsys *boltFS) BatchWrite(entries []*pb.WriteLogEntry) error {
	if fsys.isClosed() {
		return ufserrors.NewPathError("batchwrite", pathutil.CwdPath, fs.ErrClosed)
	}
	for _, e := range entries {
		if err := pathutil.Validate("batchwrite", e.GetName()); err != nil {
			return err
		}
	}
	var (
		events []batchEvent
		failed string
	)
	err := fsys.update(func(tx *bolt.Tx) error {
		for _, e := range entries {
			var err error
			if events, err = applyEntry(tx, e, events); err != nil {
				failed = e.GetName()
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ufserrors.NewPathError("batchwrite", failed, err)
	}
	for _, ev := range events {
		fsys.notify(ev.op, ev.path)
	}
	return nil
}

// applyEntry applies e within tx and appends the notifications it causes.
func applyEntry(tx *bolt.Tx, e *pb.WriteLogEntry, events []batchEvent) ([]batchEvent, error) {
	name := e.GetName()
	modTime := time.Now()
	if ts := e.GetModTime(); ts != nil {
		modTime = ts.AsTime()
	}
	switch e.GetOp() {
	case pb.WriteLogEntry_OP_PUT:
		if name == pathutil.CwdPath {
			return events, fmt.Errorf("%w: %w", errIsDirectory, fs.ErrInvalid)
		}
		bkt, key, err := parentBucket(tx, name)
		if err != nil {
			return events, err
		}
		if bkt.Bucket(key) != nil {
			return events, fmt.Errorf("%w: %w", errIsDirectory, fs.ErrInvalid)
		}
		op := ufs.NotifyCreate
		if bkt.Get(key) != nil {
			op = ufs.NotifyWrite
		}
		record, err := encodeBoltRecord(fs.FileMode(e.GetMode()), modTime, e.GetContent())
		if err != nil {
			return events, err
		}
		if err := bkt.Put(key, record); err != nil {
			return events, err
		}
		return append(events, batchEvent{op, name}), nil
	case pb.WriteLogEntry_OP_MKDIR:
		if name == pathutil.CwdPath {
			return events, nil
		}
		perm := fs.FileMode(e.GetMode()).Perm()
		if perm == 0 {
			perm = fs.ModePerm
		}
		created, err := mkdirAllTx(tx, name, perm, modTime)
		if err != nil {
			return events, err
		}
		for _, p := range created {
			events = append(events, batchEvent{ufs.NotifyCreate, p})
		}
		return events, nil
	case pb.WriteLogEntry_OP_REMOVE_ALL:
		var removed []string
		if err := removeAllTx(tx, name, &removed); err != nil {
			return events, err
		}
		for _, p := range removed {
			events = append(events, batchEvent{ufs.NotifyRemove, p})
		}
		return events, nil
	default:
		return events, fmt.Errorf("unsupported write log op %v: %w", e.GetOp(), fs.ErrInvalid)
	}
}
