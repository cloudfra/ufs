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
	"fmt"
	"io/fs"
	"sync"
	"testing"
)

// archiveFS used to make Close idempotent by writing nil over the archive every
// operation reads unlocked, so an operation racing Close either raced the write
// or called Open on a nil archive and crashed. The recover in the operation
// counts crashes rather than hiding them, so a regression reports itself instead
// of taking the rest of the package's tests with it.

// raceArchiveName is the entry the operations open while the archive closes.
const raceArchiveName = "alpha.txt"

// TestArchiveFSOperationsDuringClose runs the ordinary Open path against Close.
// What a caller is allowed to see is a file or an error matching fs.ErrClosed:
// never a crash.
func TestArchiveFSOperationsDuringClose(t *testing.T) {
	t.Parallel()

	fsys, err := newArchiveFSFromLocalFS(t.Context(), createArchiveWithEntries(t, raceArchiveName, "beta.txt"))
	if err != nil {
		t.Fatalf("newArchiveFSFromLocalFS = %v, want nil", err)
	}

	const opens = 200

	var mu sync.Mutex
	var crashes []string
	var running sync.WaitGroup
	running.Go(func() {
		for range opens {
			err := func() (err error) {
				defer func() {
					if panicked := recover(); panicked != nil {
						mu.Lock()
						if len(crashes) < 3 {
							crashes = append(crashes, fmt.Sprint(panicked))
						}
						mu.Unlock()
					}
				}()
				opened, err := fsys.Open(raceArchiveName)
				if err != nil {
					return err
				}
				if err := opened.Close(); err != nil && !errors.Is(err, fs.ErrClosed) {
					return fmt.Errorf("Close of %q = %w, want nil", raceArchiveName, err)
				}
				return nil
			}()
			if err != nil && !errors.Is(err, fs.ErrClosed) {
				t.Errorf("Open(%q) while closing = %v, want nil or an error matching fs.ErrClosed", raceArchiveName, err)
				return
			}
		}
	})
	running.Go(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close = %v, want nil", err)
		}
	})
	running.Wait()

	if len(crashes) > 0 {
		t.Errorf("%d operations met an archive that Close had already let go and panicked instead of returning fs.ErrClosed: %v",
			len(crashes), crashes)
	}
}
