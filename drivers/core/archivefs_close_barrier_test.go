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

package core

import (
	"errors"
	"io"
	"io/fs"
	"sync/atomic"
	"testing"
	"time"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestArchiveFSCloseBarrierWaitsForInFlightOperation proves the lifetime
// guard of #748 at this layer without a timer: the archive is an injected
// stub whose Open parks inside the base call until the test releases it, so
// "Close waited" is observed through channel ordering alone. The stub answers
// with a file, not an error, because a success from an archive Close has
// already let go is exactly what the guard must make impossible: an
// indexed-format listing served from a released archive looks like a healthy
// result.
//
// The stub's Close records whether any base call was in flight when it ran.
// With the barrier that is impossible: Open holds the read lock across its
// call into the stub, and Close releases the archive only under the write
// lock, so the write lock is only taken after the parked call returned. It
// then checks that an operation starting after Close returned is refused with
// fs.ErrClosed without ever reaching the released archive.

// barrierArchive is an [archive.FS] stub that parks its Open inside the base
// call until the test hands over the release, and records what was in flight
// when Close ran.
type barrierArchive struct {
	ufsTesting.EmptyFS
	openEntered   chan struct{} // Open arrived at the base and is about to park
	openRelease   chan struct{} // the test lets the parked base call proceed
	openCalls     atomic.Int32
	inFlight      atomic.Int32
	closeCalls    atomic.Int32
	closeInFlight atomic.Bool // Close ran while a base call was in flight
}

func (f *barrierArchive) Open(_ string) (fs.File, error) {
	f.openCalls.Add(1)
	f.inFlight.Add(1)
	f.openEntered <- struct{}{}
	<-f.openRelease
	f.inFlight.Add(-1)
	return barrierFile{}, nil
}

func (f *barrierArchive) Stat(name string) (fs.FileInfo, error) { return fs.Stat(f.EmptyFS, name) }

func (f *barrierArchive) ReadDir(name string) ([]fs.DirEntry, error) {
	return fs.ReadDir(f.EmptyFS, name)
}

func (f *barrierArchive) Close() error {
	f.closeCalls.Add(1)
	if f.inFlight.Load() > 0 {
		f.closeInFlight.Store(true)
	}
	return nil
}

// barrierFile is the successful result the stub base hands out, and
// barrierInfo is its metadata.
type barrierFile struct{}

func (barrierFile) Stat() (fs.FileInfo, error) { return barrierInfo{}, nil }

func (barrierFile) Read(_ []byte) (int, error) { return 0, io.EOF }

func (barrierFile) Close() error { return nil }

type barrierInfo struct{}

func (barrierInfo) Name() string       { return "alpha.txt" }
func (barrierInfo) Size() int64        { return 0 }
func (barrierInfo) Mode() fs.FileMode  { return 0 }
func (barrierInfo) ModTime() time.Time { return time.Time{} }
func (barrierInfo) IsDir() bool        { return false }
func (barrierInfo) Sys() any           { return nil }

func TestArchiveFSCloseBarrierWaitsForInFlightOperation(t *testing.T) {
	t.Parallel()

	base := &barrierArchive{
		openEntered: make(chan struct{}),
		openRelease: make(chan struct{}),
	}
	fsys := makeArchiveFS(base, "barrier.zip")

	openResult := make(chan error, 1)
	go func() {
		file, err := fsys.Open("alpha.txt")
		if err == nil {
			err = file.Close()
		}
		openResult <- err
	}()

	// The operation is now inside the call into the stub base: it has passed
	// its closed check, and with the barrier it holds the read lock here.
	<-base.openEntered

	closeResult := make(chan error, 1)
	go func() { closeResult <- fsys.Close() }()

	// Hand the parked base call its release. Main is blocked on this
	// unbuffered send while the operation is still inside the base, so a
	// Close that did not wait on the lock meets the stub's Close here and
	// trips closeInFlight below.
	base.openRelease <- struct{}{}

	if err := <-openResult; err != nil {
		t.Errorf("Open working through the archive while Close was pending = %v, want the file the base handed out", err)
	}
	if err := <-closeResult; err != nil {
		t.Errorf("Close = %v, want nil", err)
	}
	if base.closeInFlight.Load() {
		t.Error("Close released the archive while an operation was still working through it")
	}
	if got := base.closeCalls.Load(); got != 1 {
		t.Errorf("archive Close calls = %d, want 1", got)
	}

	// An operation that starts after Close returned must be refused without
	// reaching the released archive.
	if _, err := fsys.Open("alpha.txt"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Open after Close = %v, want an error matching fs.ErrClosed", err)
	}
	if got := base.openCalls.Load(); got != 1 {
		t.Errorf("archive Open calls = %d, want 1: an operation after Close reached the released archive", got)
	}
}
