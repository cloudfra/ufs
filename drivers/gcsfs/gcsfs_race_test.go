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
	"runtime/debug"
	"strings"
	"sync"
	"testing"
)

// Close used to blank ctx, bucket and baseDir while holding nothing, which every
// operation reads unlocked, so a request already under way could be handed a nil
// context. TestGCSOperationsDuringClose races operations against Close and
// checks each one either succeeds or reports fs.ErrClosed, never a panic or a
// data race. A handle is not shared between goroutines by the driver; callers
// that do share one wrap it with drivers/common/syncfs.

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
