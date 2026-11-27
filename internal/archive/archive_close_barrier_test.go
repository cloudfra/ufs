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

package archive

// These tests drive the lifetime guard of issue #748 directly: the archive is a
// stand-in that parks on a channel, so the interleaving is decided by the test
// rather than by the scheduler. archive_race_test.go stays the exposure file —
// it shows what the unguarded Close used to do — and this file shows that the
// guard holds.

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"slices"
	"sync"
	"testing"
	"time"
)

// barrierLog records, in order, when the archive stand-in is entered and left
// and when its file is released. The order is the assertion: an event written
// by the release between the enter and the return of an operation means the
// archive was torn down underneath that operation.
type barrierLog struct {
	mu     sync.Mutex
	events []string
}

func (l *barrierLog) add(event string) {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *barrierLog) snapshot() []string {
	l.mu.Lock()
	out := slices.Clone(l.events)
	l.mu.Unlock()
	return out
}

// barrierArchive stands in for the archive library handle and its file. Every
// operation announces itself on entered, parks until the test closes release,
// and then answers; Close counts how many times the archive file was released.
type barrierArchive struct {
	log     *barrierLog
	entered chan struct{}
	release chan struct{}
	once    sync.Once

	// entries is what ReadDir answers. A nil slice is an empty listing, which
	// is the case archiveFS.ReadDir confirms with a second call.
	entries []fs.DirEntry

	mu     sync.Mutex
	closes int
}

// wait parks the caller inside the archive until the test releases it.
func (a *barrierArchive) wait(op string) {
	a.once.Do(func() { close(a.entered) })
	a.log.add(op + "-enter")
	<-a.release
	a.log.add(op + "-return")
}

func (a *barrierArchive) Open(name string) (fs.File, error) {
	a.wait("base-open")
	return &barrierFile{name: path.Base(name)}, nil
}

func (a *barrierArchive) Stat(name string) (fs.FileInfo, error) {
	a.wait("base-stat")
	return &barrierInfo{name: path.Base(name)}, nil
}

func (a *barrierArchive) ReadDir(_ string) ([]fs.DirEntry, error) {
	a.wait("base-readdir")
	return a.entries, nil
}

// Close is the release of the archive file.
func (a *barrierArchive) Close() error {
	a.mu.Lock()
	a.closes++
	a.mu.Unlock()
	a.log.add("archive-file-released")
	return nil
}

func (a *barrierArchive) closeCount() int {
	a.mu.Lock()
	count := a.closes
	a.mu.Unlock()
	return count
}

// barrierEntries is a non-empty listing, so archiveFS.ReadDir answers from it
// directly instead of confirming it with another call.
var barrierEntries = []fs.DirEntry{
	&barrierDirEntry{name: "alpha.txt"},
	&barrierDirEntry{name: "beta.txt"},
}

type barrierDirEntry struct {
	name string
}

func (e *barrierDirEntry) Name() string { return e.name }

func (e *barrierDirEntry) IsDir() bool { return false }

func (e *barrierDirEntry) Type() fs.FileMode { return 0 }

func (e *barrierDirEntry) Info() (fs.FileInfo, error) {
	return &barrierInfo{name: e.name}, nil
}

type barrierFile struct {
	name string
}

func (f *barrierFile) Stat() (fs.FileInfo, error) { return &barrierInfo{name: f.name}, nil }

func (f *barrierFile) Read([]byte) (int, error) { return 0, io.EOF }

func (f *barrierFile) Close() error { return nil }

type barrierInfo struct {
	name string
}

func (i *barrierInfo) Name() string { return i.name }

func (i *barrierInfo) Size() int64 { return 0 }

func (i *barrierInfo) Mode() fs.FileMode { return 0 }

func (i *barrierInfo) ModTime() time.Time { return time.Time{} }

func (i *barrierInfo) IsDir() bool { return false }

func (i *barrierInfo) Sys() any { return nil }

// newBarrierFS returns an archiveFS over the stand-in and its file, exactly as
// makeArchiveFS builds one over a real archive.
func newBarrierFS(t *testing.T, entries []fs.DirEntry) (*archiveFS, *barrierArchive) {
	t.Helper()

	var log barrierLog
	base := &barrierArchive{
		log:     &log,
		entered: make(chan struct{}),
		release: make(chan struct{}),
		entries: entries,
	}
	return makeArchiveFS(base, "barrier.zip", base), base
}

// postCloseCalls are the operations a caller may still attempt after Close has
// returned. None of them may reach the archive: an indexed archive answers
// entries and metadata out of the library's index even with its file released,
// so an answer from it after Close would look like success.
var postCloseCalls = []struct {
	name string
	call func(FS) error
}{
	{"Open", func(fsys FS) error { _, err := fsys.Open("box/alpha.txt"); return err }},
	{"Stat", func(fsys FS) error { _, err := fsys.Stat("box/alpha.txt"); return err }},
	{"ReadDir", func(fsys FS) error { _, err := fsys.ReadDir("box"); return err }},
}

// TestArchiveFSCloseWaitsForInFlightOperation parks an operation inside the
// archive and starts Close while it is parked. Close must neither return nor
// release the archive file until that operation is done with it; the release
// must happen exactly once and after the operation left the archive; and every
// operation started after Close must be refused with fs.ErrClosed without
// reaching the archive at all.
//
// The atomic closed flag alone fails this on the order of events: it lets Close
// release the archive while the parked operation is still inside it.
func TestArchiveFSCloseWaitsForInFlightOperation(t *testing.T) {
	t.Parallel()

	for _, operation := range []struct {
		name   string
		events []string
		call   func(FS) error
	}{
		{
			"Open",
			[]string{"base-open-enter", "base-open-return"},
			func(fsys FS) error { _, err := fsys.Open("box/alpha.txt"); return err },
		},
		{
			"Stat",
			[]string{"base-stat-enter", "base-stat-return"},
			func(fsys FS) error { _, err := fsys.Stat("box/alpha.txt"); return err },
		},
		// The listing is not empty on purpose: an empty one makes ReadDir call
		// back into the file system, which is the case
		// TestArchiveFSOperationsWithPendingClose covers.
		{
			"ReadDir",
			[]string{"base-readdir-enter", "base-readdir-return"},
			func(fsys FS) error { _, err := fsys.ReadDir("box"); return err },
		},
	} {
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()

			fsys, base := newBarrierFS(t, barrierEntries)

			done := make(chan error, 1)
			go func() { done <- operation.call(fsys) }()

			// The operation is now parked inside the archive, holding the guard.
			<-base.entered

			closeDone := make(chan error, 1)
			go func() { closeDone <- fsys.Close() }()

			select {
			case err := <-closeDone:
				t.Errorf("Close returned (%v) while %s was inside the archive", err, operation.name)
			default:
			}
			select {
			case err := <-done:
				t.Errorf("%s returned (%v) while its archive call was still parked", operation.name, err)
			default:
			}
			if count := base.closeCount(); count != 0 {
				t.Errorf("the archive file was released %d times while %s was inside it, want 0", count, operation.name)
			}

			// Let the operation finish; Close may only proceed after it does.
			close(base.release)
			if err := <-done; err != nil {
				t.Fatalf("%s = %v, want nil", operation.name, err)
			}
			if err := <-closeDone; err != nil {
				t.Errorf("Close = %v, want nil", err)
			}

			want := slices.Concat(operation.events, []string{"archive-file-released"})
			if events := base.log.snapshot(); !slices.Equal(events, want) {
				t.Errorf("events = %v, want %v: Close must release the archive only after the operation using it has left it",
					events, want)
			}
			if count := base.closeCount(); count != 1 {
				t.Errorf("the archive file was released %d times, want exactly 1", count)
			}

			for _, call := range postCloseCalls {
				if err := call.call(fsys); !errors.Is(err, fs.ErrClosed) {
					t.Errorf("%s after Close = %v, want an error matching fs.ErrClosed", call.name, err)
				}
			}
			if err := fsys.Close(); err != nil {
				t.Errorf("second Close = %v, want nil", err)
			}
			if count := base.closeCount(); count != 1 {
				t.Errorf("the archive file was released %d times after the operations that ran post-Close, want 1", count)
			}
			if events := base.log.snapshot(); !slices.Equal(events, want) {
				t.Errorf("events after the post-Close calls = %v, want %v: a refused operation must not reach the archive",
					events, want)
			}
		})
	}
}

// TestArchiveFSOperationsWithPendingClose parks each operation in the archive
// while a Close is already under way, then releases it. The point is that both
// the operation and Close finish. ReadDir is the interesting row: an empty
// listing makes it confirm with Stat, which takes the guard again, and an
// operation that took the guard twice while holding it would block forever
// against the waiting Close and end this test only by tripping the timeout.
func TestArchiveFSOperationsWithPendingClose(t *testing.T) {
	t.Parallel()

	for _, operation := range []struct {
		name    string
		entries []fs.DirEntry
		call    func(FS) error
	}{
		{"Open", barrierEntries, func(fsys FS) error { _, err := fsys.Open("box/alpha.txt"); return err }},
		{"Stat", barrierEntries, func(fsys FS) error { _, err := fsys.Stat("box/alpha.txt"); return err }},
		{"ReadDir-empty", nil, func(fsys FS) error { _, err := fsys.ReadDir("box"); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()

			fsys, base := newBarrierFS(t, operation.entries)

			done := make(chan error, 1)
			go func() { done <- operation.call(fsys) }()
			<-base.entered

			closeDone := make(chan error, 1)
			go func() { closeDone <- fsys.Close() }()

			// Whatever the schedule, the operation and Close must both finish.
			// Whichever way they interleave, the only errors that are allowed
			// out are the ones a closed file system reports: ReadDir turns the
			// Stat that confirms an empty listing into fs.ErrNotExist when that
			// Stat is refused as closed.
			close(base.release)
			if err := <-done; err != nil && !errors.Is(err, fs.ErrClosed) && !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s against a pending Close = %v, want nil or an error matching fs.ErrClosed", operation.name, err)
			}
			if err := <-closeDone; err != nil {
				t.Errorf("Close = %v, want nil", err)
			}
			if count := base.closeCount(); count != 1 {
				t.Errorf("the archive file was released %d times, want exactly 1", count)
			}
		})
	}
}
