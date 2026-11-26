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
	"fmt"
	"io/fs"
	"sync"
	"testing"
)

// nestFS closes the base file system it wraps by nil'ing the field that holds
// it: Close (nestfs.go:527-538) reads fsys.fsys at :531, calls it at :532 and
// writes nil at :535, holding no lock. Every operation reads that same field
// unlocked, both to decide whether the file system is closed at all -
// validPath at :579 - and to work through it, as Open does at :509. The mounts
// beside it are guarded (mounts.Close takes mountMap's lock), so the base
// handle is the odd one out, and reaching it takes no exotic caller: nestFS
// closes the archives it mounted for itself when their volume is released, and
// a request that is already in flight is the second goroutine.
//
// The window has two faces, and the test reports both. Open reads the field
// twice - once in validPath at :579, once when it works through the handle at
// :509 - and nothing makes that pair atomic, so an operation that passes the
// closed check a moment before Close writes nil reaches a method call on a nil
// file system: that crashes the process instead of reporting fs.ErrClosed. An
// operation whose read lands while Close writes is a data race, and that is the
// report this test produces. The recover in the operation counts crashes rather
// than hiding them, because a panic here would take the rest of the package
// with it and bury the race report that is the point of the file.
//
// It is red on purpose: it reports the window as it stands so that the fix - a
// mutex around the base handle, or a closed flag that operations check under
// the same lock Close takes - has something to turn green.

// raceNestSeed is the file the operations work on while the file system closes.
const raceNestSeed = "seed.txt"

// TestNestFSOperationsDuringClose runs the ordinary Open path against Close.
// What a caller is allowed to see is either a file or an error matching
// fs.ErrClosed: nothing else, and no crash.
func TestNestFSOperationsDuringClose(t *testing.T) {
	t.Parallel()
	fsys := makeNestFS(t.Context(), mustBaseFS(t, "memory:///"))

	file, err := fsys.Create(raceNestSeed)
	if err != nil {
		t.Fatalf("Create(%q) = %v, want nil", raceNestSeed, err)
	}
	if _, err := file.WriteString("seed content"); err != nil {
		t.Fatalf("WriteString(%q) = %v, want nil", raceNestSeed, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q) = %v, want nil", raceNestSeed, err)
	}

	const ops = 200

	var mu sync.Mutex
	var crashes []string
	var running sync.WaitGroup
	running.Go(func() {
		for range ops {
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
				opened, err := fsys.Open(raceNestSeed)
				if err != nil {
					return err
				}
				return opened.Close()
			}()
			if err != nil && !errors.Is(err, fs.ErrClosed) {
				t.Errorf("Open(%q) while closing = %v, want nil or an error matching fs.ErrClosed", raceNestSeed, err)
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
		t.Errorf("%d operations met a file system that Close had already let go and panicked instead of returning fs.ErrClosed: %v",
			len(crashes), crashes)
	}
}
