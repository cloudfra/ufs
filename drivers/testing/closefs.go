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

package testing

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
)

// closeDuringOperations races ordinary operations against Close.
//
// Closing a file system that is still in use is the caller's to avoid, and a
// driver need not wait for the operations in progress, but it must not crash
// or race on them either: every field an operation reads stays valid after
// Close, and an operation that starts after Close has returned reports
// [fs.ErrClosed]. So an operation overlapping Close may fail however the
// backend fails a request cut off under it, but must not panic, and the race
// detector checks that Close writes nothing it reads; every operation that
// starts once Close has returned must report fs.ErrClosed. That last part is
// not asked of a read-only fixture ([Args.ReadOnly]), as the Close suite does
// not ask it either: a file system with nothing to release, such as null://,
// may keep answering.
//
// It runs for every file system, read-only ones included, because it only
// reads: the root, and the first entry the root lists, if there is one, so a
// driver whose root is answered without its backend is still made to reach it.
func closeDuringOperations(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("CloseDuringOperations", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		names := []string{"."}
		if entries, err := fs.ReadDir(fsys, "."); err == nil && len(entries) > 0 {
			names = append(names, entries[0].Name())
		}

		// Each worker keeps going until Close has returned and it has then
		// run a few more rounds, so operations land before, during and after
		// Close whichever goroutine the scheduler favors.
		const workers, roundsAfterClose = 4, 3
		var closed atomic.Bool
		var mu sync.Mutex
		var failures []string
		fail := func(format string, a ...any) {
			mu.Lock()
			if len(failures) < 5 {
				failures = append(failures, fmt.Sprintf(format, a...))
			}
			mu.Unlock()
		}
		// check judges one operation. after reports whether Close had
		// returned before the round the operation belongs to began.
		check := func(after bool, op string, err error) {
			if after && !args.ReadOnly && !errors.Is(err, fs.ErrClosed) {
				fail("%s after Close = %v, want an error matching fs.ErrClosed", op, err)
			}
		}
		round := func() {
			after := closed.Load()
			defer func() {
				if panicked := recover(); panicked != nil {
					fail("operation racing Close panicked: %v\n%s", panicked, debug.Stack())
				}
			}()
			for _, name := range names {
				if f, err := fsys.Open(name); err != nil {
					check(after, fmt.Sprintf("Open(%q)", name), err)
				} else {
					// Closing a handle the file system has since closed may
					// fail either way; only a crash is wrong.
					_ = f.Close() //nolint:errcheck // either outcome is allowed
					check(after, fmt.Sprintf("Open(%q)", name), err)
				}
				_, err := fs.Stat(fsys, name)
				check(after, fmt.Sprintf("Stat(%q)", name), err)
			}
			_, err := fs.ReadDir(fsys, ".")
			check(after, `ReadDir(".")`, err)
		}

		// Close waits until every worker has finished a round, so that it
		// lands among operations rather than ahead of all of them.
		var started sync.WaitGroup
		started.Add(workers)
		var running sync.WaitGroup
		for range workers {
			running.Go(func() {
				round()
				started.Done()
				for after := 0; after < roundsAfterClose; {
					if closed.Load() {
						after++
					}
					round()
				}
			})
		}
		running.Go(func() {
			started.Wait()
			if err := fsys.Close(); err != nil {
				fail("Close() = %v, want nil", err)
			}
			closed.Store(true)
		})
		running.Wait()

		for _, failure := range failures {
			t.Error(failure)
		}
	})
}
