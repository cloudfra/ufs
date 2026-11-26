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

// archiveFS keeps the archive it mounted in a plain field
// (drivers/core/archivefs.go:56-58) and makes Close idempotent by writing nil
// into it: Close reads fsys.fsys at :69, writes nil at :70 and closes what it
// kept at :74, holding no lock. Open reads the same field at :65, and unlike
// nestFS there is no closed check in front of it at all, so the two faces of
// this window are not a close call: an operation whose read lands while Close
// writes is a data race, and one whose read lands after the write calls Open on
// a nil archive, which crashes the process instead of answering fs.ErrClosed.
//
// Nothing here needs an exotic caller. Close arrives on its own whenever the
// archive is let go - a volume released, a host path unmounted, a file system
// closed while a request that already holds it is still running - and Open is
// the ordinary path a request takes.
//
// The recover in the operation counts crashes rather than hiding them: a panic
// would take the rest of the package's tests with it and bury the report that is
// the point of the file.
//
// It is red on purpose, and red either way: without -race the crash alone fails
// it (ten runs out of ten met the nil dereference), and under -race the access
// pair is reported as well. That leaves the fix - a mutex around the inner
// handle, or a sync.Once close plus a closed flag that Open answers with
// fs.ErrClosed - with something to turn green.

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
