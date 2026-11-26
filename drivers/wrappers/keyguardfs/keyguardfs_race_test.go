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

package keyguardfs

import (
	"errors"
	"io"
	"io/fs"
	"slices"
	"sync"
	"testing"
)

// A guard in hide mode answers Open of a directory with a hiddenDirHandle
// (keyguardfs.go:167) that filters the listing of the guarded handle once and
// then paginates that snapshot: hiddenDirHandle (keyguardfs_readdirfs.go:40-46)
// fills entries on the first walk (:49 tests for it, :50 asks the guarded handle,
// :54 writes the filtered result) and keeps its own position in off, read at :56
// and :58 and written at :58 and :67. Nothing guards either field, so two
// callers paginating the one handle Open hands out can both decide the snapshot
// has not been taken yet and write :54 twice, and can move one position between
// them and be handed the same entries twice or miss others.
//
// The snapshot itself is the right design and the doc comment at :37-39 says
// why: without it a count-limited ReadDir loses the entries that sit behind a
// run of hidden ones. What that comment does not say — and what the code does
// not provide — is any safety against a second walker.
//
// The test is red on purpose. The fix that turns it green is open question 1 in
// #748: guard the snapshot and the position, or narrow the doc so one handle
// belongs to one goroutine.

// raceGuardDir is the seeded root of the fixture. It holds both protected and
// unprotected names at one level, which is the case the snapshot exists for, and
// it is the same directory the guard's own ReadDir answers, so the two paths
// have to agree.
const raceGuardDir = "."

// TestHideModeDirHandleSharedByTwoWalkers walks one directory handle from two
// goroutines. Between them they must be handed exactly the names the guard
// reports for that directory, each exactly once: none skipped, none twice,
// nothing hidden.
func TestHideModeDirHandleSharedByTwoWalkers(t *testing.T) {
	t.Parallel()

	fsys := makeGuard(t, Options{Patterns: testPatterns, Mode: ModeHide})

	guarded, err := fsys.ReadDir(raceGuardDir)
	if err != nil {
		t.Fatalf("ReadDir(%q) = %v, want nil", raceGuardDir, err)
	}
	if len(guarded) == 0 {
		t.Fatalf("ReadDir(%q) = %v, want the seeded listing to hold entries", raceGuardDir, guarded)
	}

	dir, err := fsys.Open(raceGuardDir)
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil", raceGuardDir, err)
	}
	defer func() {
		if err := dir.Close(); err != nil {
			t.Errorf("Close of %q = %v, want nil", raceGuardDir, err)
		}
	}()
	walker, ok := dir.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("Open(%q) = %T, want an io/fs.ReadDirFile", raceGuardDir, dir)
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

	want := make([]string, 0, len(guarded))
	for _, entry := range guarded {
		want = append(want, entry.Name())
	}
	slices.Sort(seen)
	slices.Sort(want)
	if !slices.Equal(seen, want) {
		t.Errorf("one handle walked by two goroutines returned %d entries %v, want each of the %d names the guard reports for %q (%v) exactly once",
			len(seen), seen, len(want), raceGuardDir, want)
	}
}
