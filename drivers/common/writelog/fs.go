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

package writelog

import (
	"errors"
	"io/fs"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/ops"
	pb "github.com/cloudfra/ufs/proto"
)

var (
	_ ufs.FS     = (*FS)(nil)
	_ types.File = (*file)(nil)
)

// Mode selects when [FS] records a write.
type Mode int

const (
	// Async records writes on a background goroutine. Records can trail the
	// writes; [FS.Barrier] waits for them to catch up.
	Async Mode = iota
	// Sync records a write in the calling goroutine before the write returns.
	Sync
)

// asyncQueueSize bounds the entries waiting to be recorded in Async mode. A
// full queue blocks the writer rather than dropping a record.
const asyncQueueSize = 1024

// FS wraps a [types.WriteFS] and records the writes made through it into a [Log].
// The wrapped file system is not modified, so any driver can be recorded.
//
// Only operations that succeed on the wrapped file system are recorded:
//
//   - OP_PUT when a file from Create is closed, with the file's mode, modTime
//     and size but not its content.
//   - OP_MKDIR on MkdirAll.
//   - OP_REMOVE_ALL on Remove and RemoveAll. A Remove that fails with
//     fs.ErrNotExist is still recorded, because the path may exist in a copy
//     of the file system that this one never saw.
//
// Reads pass straight through. FS owns the wrapped file system: Close waits
// for pending records and then closes it.
type FS struct {
	types.WriteFS
	log  Log
	mode Mode

	mu     sync.RWMutex // guards closed and sends on queue
	closed bool
	queue  chan *pb.WriteLogEntry
	done   chan struct{}

	sent      atomic.Uint64
	progress  sync.Mutex
	caughtUp  *sync.Cond
	processed uint64
}

// NewFS returns inner wrapped so that its writes are recorded into log.
func NewFS(inner types.WriteFS, log Log, mode Mode) *FS {
	f := &FS{WriteFS: inner, log: log, mode: mode}
	f.caughtUp = sync.NewCond(&f.progress)
	if mode == Async {
		f.queue = make(chan *pb.WriteLogEntry, asyncQueueSize)
		f.done = make(chan struct{})
		go f.recordLoop()
	}
	return f
}

// recordLoop appends queued entries to the log until the queue is closed.
func (f *FS) recordLoop() {
	defer close(f.done)
	for e := range f.queue {
		if err := f.log.Append(e); err != nil {
			slog.Warn("writelog: cannot record write", "op", e.GetOp(), "path", e.GetName(), "error", err)
		}
		f.progress.Lock()
		f.processed++
		f.caughtUp.Broadcast()
		f.progress.Unlock()
	}
}

// Barrier blocks until every write that completed before the call has been
// recorded. It returns at once in Sync mode.
func (f *FS) Barrier() {
	if f.mode != Async {
		return
	}
	target := f.sent.Load()
	f.progress.Lock()
	for f.processed < target {
		f.caughtUp.Wait()
	}
	f.progress.Unlock()
}

// record records e. In Sync mode it returns the log's error; in Async mode a
// failure is logged by the background goroutine.
func (f *FS) record(e *pb.WriteLogEntry) error {
	if f.mode == Sync {
		return f.log.Append(e)
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.closed {
		return nil
	}
	f.sent.Add(1)
	f.queue <- e
	return nil
}

// Create creates name in the wrapped file system. The returned file records
// an OP_PUT when it is closed successfully.
func (f *FS) Create(name string) (types.File, error) {
	inner, err := f.WriteFS.Create(name)
	if err != nil {
		return nil, err
	}
	return &file{File: inner, fsys: f, name: name}, nil
}

// MkdirAll creates name in the wrapped file system and records an OP_MKDIR.
func (f *FS) MkdirAll(name string, perm fs.FileMode) error {
	if err := f.WriteFS.MkdirAll(name, perm); err != nil {
		return err
	}
	if name == pathutil.CwdPath {
		return nil
	}
	return f.recordErr("mkdir", name, f.record(&pb.WriteLogEntry{
		Op:      pb.WriteLogEntry_OP_MKDIR,
		Name:    name,
		Mode:    uint32(fs.ModeDir | perm.Perm()),
		ModTime: timestamppb.New(time.Now()),
	}))
}

// Remove removes name from the wrapped file system and records an
// OP_REMOVE_ALL, also when name did not exist there.
func (f *FS) Remove(name string) error {
	err := f.WriteFS.Remove(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return ufserrors.Join(err, f.recordErr("remove", name, f.record(removeEntry(name))))
}

// RemoveAll removes name from the wrapped file system and records an
// OP_REMOVE_ALL.
func (f *FS) RemoveAll(name string) error {
	if err := f.WriteFS.RemoveAll(name); err != nil {
		return err
	}
	return f.recordErr("removeall", name, f.record(removeEntry(name)))
}

// Rename renames oldPath to newPath in the wrapped file system with
// [ops.Rename] and records the result. The log has no rename operation, so
// it records an OP_REMOVE_ALL for oldPath, then an OP_MKDIR for each
// directory and an OP_PUT for each file now at or below newPath, as
// MkdirAll and Create would.
func (f *FS) Rename(oldPath, newPath string) error {
	if err := ops.Rename(f.WriteFS, oldPath, newPath); err != nil {
		return err
	}
	if err := f.record(removeEntry(oldPath)); err != nil {
		return f.recordErr("rename", oldPath, err)
	}
	return fs.WalkDir(f.WriteFS, newPath, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		e := &pb.WriteLogEntry{
			Op:      pb.WriteLogEntry_OP_PUT,
			Name:    name,
			Mode:    uint32(info.Mode()),
			ModTime: timestamppb.New(info.ModTime()),
			Size:    info.Size(),
		}
		if d.IsDir() {
			e.Op = pb.WriteLogEntry_OP_MKDIR
			e.Size = 0
		}
		return f.recordErr("rename", name, f.record(e))
	})
}

// Close waits for pending records, stops recording and closes the wrapped
// file system. Later calls close the wrapped file system again.
func (f *FS) Close() error {
	f.mu.Lock()
	wasClosed := f.closed
	f.closed = true
	if !wasClosed && f.queue != nil {
		close(f.queue)
	}
	f.mu.Unlock()
	if f.done != nil {
		<-f.done
	}
	return f.WriteFS.Close()
}

// String describes the wrapper and the wrapped file system.
func (f *FS) String() string {
	return "writelog(" + f.WriteFS.String() + ")"
}

func (f *FS) recordErr(op, name string, err error) error {
	if err == nil {
		return nil
	}
	return ufserrors.NewPathError(op, name, err)
}

func removeEntry(name string) *pb.WriteLogEntry {
	return &pb.WriteLogEntry{Op: pb.WriteLogEntry_OP_REMOVE_ALL, Name: name}
}

// file records an OP_PUT when it is first closed successfully.
type file struct {
	types.File
	fsys   *FS
	name   string
	closed atomic.Bool
}

func (f *file) Close() error {
	if err := f.File.Close(); err != nil {
		return err
	}
	if f.closed.Swap(true) {
		return nil
	}
	info, err := f.fsys.Stat(f.name)
	if err != nil || info.IsDir() {
		// The file was removed or replaced by a directory after it was
		// written; that change is recorded on its own.
		return nil //nolint:nilerr // see above
	}
	return f.fsys.recordErr("close", f.name, f.fsys.record(&pb.WriteLogEntry{
		Op:      pb.WriteLogEntry_OP_PUT,
		Name:    f.name,
		Mode:    uint32(info.Mode()),
		ModTime: timestamppb.New(info.ModTime()),
		Size:    info.Size(),
	}))
}
