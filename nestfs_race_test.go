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

// nestFS used to close the base file system it wraps by nil'ing the field every
// operation reads unlocked, so an operation racing Close either raced the write
// or worked through a nil file system and crashed. nestFS also closes the
// archives it mounted for itself when they are released while a request may be
// using them, so unlike the drivers it keeps a lifetime guard of its own; the
// barrier tests in nestfs_close_barrier_test.go pin that. The recover in the
// operation counts crashes rather than hiding them.

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
