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

package gcsfs

import (
	"errors"
	"io"
	"io/fs"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Three pieces of gcsfs state are shared without a guard, and the fake backend
// the package already tests against (CreateFakeGcsBackend, gcsfs_test.go:242)
// shows all three without a cloud project.
//
//  1. Close (gcsfs.go:322-326) zeroes ctx (:70), bucket (:71) and baseDir (:72)
//     while holding nothing. Every operation reads those fields unlocked: Open at
//     :232, :233, :235 and :237, listDir at :281, :282, :285 and :288. A Close
//     that lands between a caller's read and its request hands that caller a nil
//     context, and nothing orders the field writes against the reads.
//
//  2. gcsFile.offset (:83) is read by Read at :108 and :111 and written at :112,
//     and Seek writes it at :147, :149, :151 and :157. One handle handed to two
//     callers gives them one position.
//
//  3. gcsFile.dirOffset (:88) is read by ReadDir at :177, :184, :188 and :189 and
//     written at :185 and :190. dirEntries (:87) is filled at Open (:222, :270)
//     and read-only after, so the snapshot itself is sound - the position into it
//     is not, which is the same handle family #748 lists.
//
// gcsFile.writer (:90), nil'd by the handle's own Close at :163-166 against
// WriteString at :135 and :138, is the same shape on the write path and is
// deliberately not exercised here.
//
// The tests are red on purpose. The fix that turns them green is open question 1
// in #748 - does a handle belong to one goroutine? - plus the Close ordering
// question #746 was about.

// raceGCSFile and raceGCSDir are names the shared fixture serves: object "a" and
// the virtual directory in front of "dir/c" and "dir/d".
const (
	raceGCSFile = "a"
	raceGCSDir  = "dir"
)

// newRaceGcsFS builds a driver over the package's fake backend, the same one the
// existing tests use (gcsfs_test.go:242 and the makeGCSFSWithClient calls below
// it). The fake server is stopped by that helper's own cleanup.
func newRaceGcsFS(t *testing.T) *gcsFS {
	t.Helper()

	client := CreateFakeGcsBackend(t)
	fsys, err := makeGCSFSWithClient(t.Context(), client, "gs://first")
	if err != nil {
		t.Fatalf("makeGCSFSWithClient(ctx, client, %q) = %v, want nil", "gs://first", err)
	}
	return fsys
}

// TestGCSOperationsDuringClose runs ordinary operations against a file system
// that another goroutine is closing at the same time. Errors from a half-closed
// driver are expected, so the first few are kept only as context for a failure
// message rather than asserted: the point is that no caller is handed a panic,
// and that the field writes at :323, :324 and :325 have no ordering against the
// unlocked reads the operations perform.
func TestGCSOperationsDuringClose(t *testing.T) {
	t.Parallel()

	fsys := newRaceGcsFS(t)

	if _, err := fsys.ReadDir(raceGCSDir); err != nil {
		t.Fatalf("ReadDir(%q) before Close = %v, want nil", raceGCSDir, err)
	}

	var mu sync.Mutex
	var panics []any
	var errs []error
	var firstPanicStack string
	record := func(recovered any) {
		if recovered == nil {
			return
		}
		mu.Lock()
		if len(panics) < 3 {
			panics = append(panics, recovered)
		}
		if firstPanicStack == "" {
			frames := strings.Split(string(debug.Stack()), "\n")
			if len(frames) > 13 {
				frames = frames[:13]
			}
			firstPanicStack = strings.Join(frames, "\n")
		}
		mu.Unlock()
	}
	// Errors are kept only as context for the failure message: a half-closed
	// driver is allowed to answer them, and which ones it answers with is the
	// ordering question #746 covers rather than something this test decides.
	recordError := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		if len(errs) < 3 {
			errs = append(errs, err)
		}
		mu.Unlock()
	}

	operations := []func() error{
		func() error { _, err := fsys.Stat(raceGCSFile); return err },
		func() error { _, err := fsys.ReadDir(raceGCSDir); return err },
		func() error {
			file, err := fsys.Open(raceGCSFile)
			if err != nil {
				return err
			}
			return file.Close()
		},
	}

	var running sync.WaitGroup
	for worker := range 8 {
		operation := operations[worker%len(operations)]
		running.Go(func() {
			for range 50 {
				func() {
					defer func() { record(recover()) }()
					recordError(operation())
				}()
			}
		})
	}
	running.Go(func() {
		defer func() { record(recover()) }()
		recordError(fsys.Close())
	})
	running.Wait()

	if len(panics) > 0 {
		t.Errorf("operations racing Close panicked %d time(s), first was %v, with errors %v alongside, want no panic: a closed file system owes callers an error, not a crash. First panic stack:\n%s", len(panics), panics[0], errs, firstPanicStack)
	}
}

// TestGCSFileHandleSharedByTwoReaders reads one opened object from two
// goroutines. Between them they must be handed every byte of the object exactly
// once: gcsFile.offset is one counter, so a shared handle hands one reader bytes
// the other was owed and shows the other the same bytes twice.
func TestGCSFileHandleSharedByTwoReaders(t *testing.T) {
	t.Parallel()

	fsys := newRaceGcsFS(t)

	// The expectation comes from a second handle on the same object, so the
	// fixture's content is not repeated here.
	want, err := fs.ReadFile(fsys, raceGCSFile)
	if err != nil {
		t.Fatalf("ReadFile(%q) = %v, want nil", raceGCSFile, err)
	}
	if len(want) == 0 {
		t.Fatalf("ReadFile(%q) = %q, want the fixture object to have content", raceGCSFile, want)
	}

	file, err := fsys.Open(raceGCSFile)
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil", raceGCSFile, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("Close of %q = %v, want nil", raceGCSFile, err)
		}
	}()

	const reads = 200

	var mu sync.Mutex
	var got []byte
	var running sync.WaitGroup
	for range 2 {
		running.Go(func() {
			buf := make([]byte, 1)
			for range reads {
				n, err := file.Read(buf)
				if err != nil && !errors.Is(err, io.EOF) {
					t.Errorf("Read(1 byte) = %v, want nil or io.EOF", err)
					return
				}
				if n > 0 {
					mu.Lock()
					got = append(got, buf[:n]...)
					mu.Unlock()
				}
			}
		})
	}
	running.Wait()

	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("one handle read by two goroutines returned %d bytes, want the %d bytes of %q exactly once: %d of them differ", len(got), len(want), raceGCSFile, len(got))
	}
}

// TestGCSDirHandleSharedByTwoWalkers paginates one directory handle from two
// goroutines. They must be handed exactly the entries the driver reports for
// that directory, each exactly once: none skipped, none twice.
func TestGCSDirHandleSharedByTwoWalkers(t *testing.T) {
	t.Parallel()

	fsys := newRaceGcsFS(t)

	listing, err := fsys.ReadDir(raceGCSDir)
	if err != nil {
		t.Fatalf("ReadDir(%q) = %v, want nil", raceGCSDir, err)
	}
	if len(listing) == 0 {
		t.Fatalf("ReadDir(%q) = %v, want the fixture directory to hold entries", raceGCSDir, listing)
	}

	dir, err := fsys.Open(raceGCSDir)
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil", raceGCSDir, err)
	}
	defer func() {
		if err := dir.Close(); err != nil {
			t.Errorf("Close of %q = %v, want nil", raceGCSDir, err)
		}
	}()
	walker, ok := dir.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("Open(%q) = %T, want an io/fs.ReadDirFile", raceGCSDir, dir)
	}

	const pages = 100

	var mu sync.Mutex
	var seen []string
	var running sync.WaitGroup
	for range 2 {
		running.Go(func() {
			for range pages {
				entries, err := walker.ReadDir(2)
				if err != nil && !errors.Is(err, io.EOF) {
					t.Errorf("ReadDir(2) = %v, want nil or io.EOF", err)
					return
				}
				mu.Lock()
				for _, entry := range entries {
					seen = append(seen, entry.Name())
				}
				mu.Unlock()
			}
		})
	}
	running.Wait()

	want := make([]string, 0, len(listing))
	for _, entry := range listing {
		want = append(want, entry.Name())
	}
	slices.Sort(seen)
	slices.Sort(want)
	if !slices.Equal(seen, want) {
		t.Errorf("one handle walked by two goroutines returned %d entries %v, want each of the %d names the driver reports for %q (%v) exactly once",
			len(seen), seen, len(want), raceGCSDir, want)
	}
}
