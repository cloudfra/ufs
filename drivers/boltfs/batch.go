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
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// BatchCreate writes files in one transaction. Calls from several goroutines
// share transactions: a call commits at once when no commit is running, and
// the calls that arrive while one is running are committed together after
// it. Unlike bolt.DB.Batch this adds no delay for a lone writer.
func (fsys *boltFS) BatchCreate(files []types.BatchFile) error {
	// Names are checked before the request reaches the commit queue, because a
	// queued request can be committed inside another goroutine's transaction.
	// types.ValidateBatchFiles replaces this loop with one call once it exists,
	// which also refuses a name repeated within a single batch.
	for i := range files {
		file := &files[i]
		if err := pathutil.ValidateFileName(batchCreateOp, file.Name); err != nil {
			return err
		}
	}
	if len(files) == 0 {
		return nil
	}
	now := time.Now()
	req := &batchRequest{
		files:   files,
		records: make([][]byte, len(files)),
		lead:    make(chan bool, 1),
	}
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
		req.records[i] = record
	}

	if err := fsys.batches.submit(fsys.update, req); err != nil {
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			return err
		}
		return ufserrors.NewPathError(batchCreateOp, files[0].Name, err)
	}
	for _, e := range req.events {
		fsys.notify(e.op, e.path)
	}
	return nil
}

// batchEvent is a change that a batch makes, reported once it is committed.
type batchEvent struct {
	op   types.NotifyOp
	path string
}

// batchRequest is one BatchCreate call waiting to be committed.
type batchRequest struct {
	files   []types.BatchFile
	records [][]byte

	// lead receives true when this request is to commit the queue, which
	// holds it, and false once another request committed it.
	lead chan bool

	// err and events are the result, set by whichever request commits this
	// one before it receives from lead.
	err    error
	events []batchEvent
}

// apply writes the files of req in tx and records their events.
func (req *batchRequest) apply(tx *bolt.Tx) error {
	req.events = req.events[:0]
	created := func(dir string) {
		req.events = append(req.events, batchEvent{types.NotifyCreate, dir})
	}
	for i := range req.files {
		file := &req.files[i]
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
		if err := bkt.Put(key, req.records[i]); err != nil {
			return ufserrors.NewPathError(batchCreateOp, file.Name, err)
		}
		req.events = append(req.events, batchEvent{op, file.Name})
	}
	return nil
}

// batchCommitter groups the BatchCreate calls of several goroutines into
// shared transactions. Its zero value is ready to use.
type batchCommitter struct {
	mu    sync.Mutex
	queue []*batchRequest
	// leading is set while a request commits the queue.
	leading bool
}

// submit queues req and returns once it is committed. When no request is
// committing, req commits the queue itself; otherwise it waits to be
// committed by the request that is, or to be handed the queue. A request
// that commits hands the queue on afterwards rather than committing again,
// so that no caller waits for more than the commit before its own and that
// one. Each request receives from lead at most once and lead is buffered,
// so the handoff never blocks.
func (c *batchCommitter) submit(update func(tx func(*bolt.Tx) error) error, req *batchRequest) error {
	c.mu.Lock()
	c.queue = append(c.queue, req)
	if c.leading {
		c.mu.Unlock()
		if !<-req.lead {
			return req.err
		}
	} else {
		c.leading = true
		c.mu.Unlock()
	}

	c.mu.Lock()
	batch := c.queue
	c.queue = nil
	c.mu.Unlock()

	commitBatch(update, batch)
	for _, other := range batch {
		if other != req {
			other.lead <- false
		}
	}

	c.mu.Lock()
	if len(c.queue) == 0 {
		c.leading = false
		c.mu.Unlock()
		return req.err
	}
	next := c.queue[0]
	c.mu.Unlock()
	next.lead <- true
	return req.err
}

// commitBatch writes every request of batch in one transaction using update
// and sets their results. When that fails and batch holds more than one
// request, each is committed on its own instead, so that one failing request
// does not fail the others. A panic is reported to every request of the
// batch rather than unwinding: submit hands each result to its caller
// through lead, so a request left without a result would keep its caller
// blocked there, and one left without the queue would stop all later batch
// writes as well.
func commitBatch(update func(tx func(*bolt.Tx) error) error, batch []*batchRequest) {
	defer func() {
		if p := recover(); p != nil {
			err := fmt.Errorf("%s panicked while committing: %v", batchCreateOp, p)
			for _, req := range batch {
				req.err = err
			}
		}
	}()
	err := update(func(tx *bolt.Tx) error {
		for _, req := range batch {
			if err := req.apply(tx); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil || len(batch) == 1 {
		for _, req := range batch {
			req.err = err
		}
		return
	}
	for _, req := range batch {
		req.err = update(req.apply)
	}
}
