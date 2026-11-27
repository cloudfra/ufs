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

package ufs

import (
	"errors"
	"io/fs"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
)

// The two tests here drive the closedness barrier of nestFS directly instead
// of racing it, so they say what the barrier promises rather than what a
// schedule happened to show. nestFS operations hold the nest's lifetime read
// lock across their closed check and the call they make into the base file
// system, and Close holds that lock for writing across letting go of the base
// and the mounts (nestfs.go). Two things follow, and each test pins one:
// Close cannot reach the base's Close while an operation is still working
// through the base, and an operation that arrives after Close has claimed the
// closed flag is refused even while Close is still inside the base.
//
// Everything is ordered through channels, so there is no sleep and no timing
// assumption: the stub announces where it is and waits on the test.

// barrierFileName is the path the operations name.
const barrierFileName = "file.txt"

// barrierStubFS is a types.WriteFS that reports its own progress to the test
// and waits where the test tells it. Its ReadDir counts itself in flight from
// entry to exit and parks on readRelease; its first Close records whether any
// base operation was in flight at that moment, parks on closeRelease, and
// marks the base closed afterwards. With the barrier in place the recorded
// flag can never be set: the nest's Close may not reach this Close until the
// guarded section around ReadDir has ended, and by then ReadDir has left the
// base.
type barrierStubFS struct {
	readEnter    chan struct{} // ReadDir reached the base
	readRelease  chan struct{} // the test lets ReadDir return
	closeEnter   chan struct{} // the first Close reached the base
	closeRelease chan struct{} // the test lets Close return

	inFlight      atomic.Int32
	closeCalls    atomic.Int32
	closeDuringOp atomic.Bool
	baseClosed    atomic.Bool
}

func (stub *barrierStubFS) ReadDir(_ string) ([]fs.DirEntry, error) {
	stub.inFlight.Add(1)
	stub.readEnter <- struct{}{}
	<-stub.readRelease
	stub.inFlight.Add(-1)
	return []fs.DirEntry{types.NewDirEntry(barrierFileName)}, nil
}

func (stub *barrierStubFS) Close() error {
	if stub.closeCalls.Add(1) == 1 {
		if stub.inFlight.Load() > 0 {
			stub.closeDuringOp.Store(true)
		}
		stub.closeEnter <- struct{}{}
		<-stub.closeRelease
		stub.baseClosed.Store(true)
	}
	return nil
}

func (stub *barrierStubFS) notExist(op, name string) error {
	return &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
}

func (stub *barrierStubFS) Open(name string) (fs.File, error) {
	return nil, stub.notExist("open", name)
}

func (stub *barrierStubFS) ReadFile(name string) ([]byte, error) {
	return nil, stub.notExist("readfile", name)
}

func (stub *barrierStubFS) ReadLink(name string) (string, error) {
	return "", stub.notExist("readlink", name)
}

func (stub *barrierStubFS) Stat(name string) (fs.FileInfo, error) {
	return nil, stub.notExist("stat", name)
}

func (stub *barrierStubFS) Lstat(name string) (fs.FileInfo, error) {
	return nil, stub.notExist("lstat", name)
}

func (stub *barrierStubFS) Create(name string) (types.File, error) {
	return nil, stub.notExist("create", name)
}

func (stub *barrierStubFS) MkdirAll(name string, _ fs.FileMode) error {
	return stub.notExist("mkdir", name)
}

func (stub *barrierStubFS) Remove(name string) error {
	return stub.notExist("remove", name)
}

func (stub *barrierStubFS) RemoveAll(name string) error {
	return stub.notExist("removeall", name)
}

func (stub *barrierStubFS) String() string {
	return "barrierStubFS"
}

func (stub *barrierStubFS) DeviceInfo() types.DeviceMap {
	return types.DeviceMap{}
}

func (stub *barrierStubFS) URI() (*url.URL, error) {
	return nil, nil
}

func (stub *barrierStubFS) IsSame(other fs.FS) bool {
	otherStub, ok := other.(*barrierStubFS)
	return ok && otherStub == stub
}

func makeBarrierStub() *barrierStubFS {
	return &barrierStubFS{
		readEnter:    make(chan struct{}, 1),
		readRelease:  make(chan struct{}),
		closeEnter:   make(chan struct{}, 1),
		closeRelease: make(chan struct{}),
	}
}

// TestNestFSCloseBarrierWaitsForBaseUse parks an operation inside the base
// file system and starts Close behind it. The base Close records what was in
// flight when it began: with the barrier it is nothing, because the nest's
// Close holds the lifetime lock for writing and the parked operation still
// holds it for reading, so the teardown cannot start until the operation has
// finished its use and left the base. Without the barrier the recorded flag
// says the base was torn down around a live operation.
func TestNestFSCloseBarrierWaitsForBaseUse(t *testing.T) {
	t.Parallel()
	stub := makeBarrierStub()
	fsys := makeNestFS(t.Context(), stub)

	opDone := make(chan error, 1)
	go func() {
		_, err := fsys.ReadDir(".")
		opDone <- err
	}()
	<-stub.readEnter

	closeStarted := make(chan struct{})
	closeDone := make(chan error, 1)
	go func() {
		close(closeStarted)
		closeDone <- fsys.Close()
	}()
	<-closeStarted
	close(stub.readRelease)

	if err := <-opDone; err != nil {
		t.Fatalf(`ReadDir(".") = %v, want nil`, err)
	}
	close(stub.closeRelease)
	if err := <-closeDone; err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}

	if stub.closeDuringOp.Load() {
		t.Error("the base was closed while an operation was still working through it: the closedness check and the use of the base file system are not one decision with respect to Close")
	}
	if !stub.baseClosed.Load() {
		t.Error("Close never reached the base file system")
	}
	if calls := stub.closeCalls.Load(); calls != 1 {
		t.Errorf("the base file system was closed %d times, want 1", calls)
	}
	if _, err := fsys.Open(barrierFileName); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Open after Close = %v, want an error matching fs.ErrClosed", err)
	}
}

// TestNestFSCloseBarrierRefusesAfterClose parks Close inside the base file
// system, where it holds the lifetime lock for writing, and then asks every
// operation of the nest. Each is refused with [fs.ErrClosed] by its own check
// before it can reach the lock, so no operation queues behind the parked
// Close and none reaches the base: an operation that starts after the close
// has been claimed sees it closed whichever way the race is settled.
func TestNestFSCloseBarrierRefusesAfterClose(t *testing.T) {
	t.Parallel()
	stub := makeBarrierStub()
	fsys := makeNestFS(t.Context(), stub)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- fsys.Close()
	}()
	<-stub.closeEnter

	checked := []struct {
		name string
		call func() error
	}{
		{"Open", func() error { _, err := fsys.Open(barrierFileName); return err }},
		{"ReadDir", func() error { _, err := fsys.ReadDir("."); return err }},
		{"Stat", func() error { _, err := fsys.Stat(barrierFileName); return err }},
		{"Lstat", func() error { _, err := fsys.Lstat(barrierFileName); return err }},
		{"ReadFile", func() error { _, err := fsys.ReadFile(barrierFileName); return err }},
		{"ReadLink", func() error { _, err := fsys.ReadLink(barrierFileName); return err }},
		{"Create", func() error { _, err := fsys.Create(barrierFileName); return err }},
		{"MkdirAll", func() error { return fsys.MkdirAll("dir", 0o755) }},
		{"Remove", func() error { return fsys.Remove(barrierFileName) }},
		{"RemoveAll", func() error { return fsys.RemoveAll(barrierFileName) }},
		{"Glob", func() error { _, err := fsys.Glob("*"); return err }},
	}
	for _, op := range checked {
		if err := op.call(); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("%s while Close holds the base = %v, want an error matching fs.ErrClosed", op.name, err)
		}
	}

	close(stub.closeRelease)
	if err := <-closeDone; err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}
	if !stub.baseClosed.Load() {
		t.Error("Close never reached the base file system")
	}
	for _, op := range checked {
		if err := op.call(); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("%s after Close = %v, want an error matching fs.ErrClosed", op.name, err)
		}
	}
	if err := fsys.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
	if calls := stub.closeCalls.Load(); calls != 1 {
		t.Errorf("the base file system was closed %d times, want 1", calls)
	}
}
