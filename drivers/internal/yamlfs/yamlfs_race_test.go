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

package yamlfs

import (
	"errors"
	"io"
	"io/fs"
	"slices"
	"sync"
	"testing"
)

// The package doc promises that "the tree never changes and every operation is
// safe for concurrent use" (yamlfs.go:22-23). The tree never changes, and the
// file-system methods really are safe — TestConcurrentReads in yamlfs_test.go
// already walks that half of the promise. What is not safe is one handle: dirFile
// keeps its walk position in a plain int (yamlfs.go:142, moved by ReadDir at
// :157-169) and leafFile inherits the position of a *bytes.Reader
// (yamlfs.go:125), and a position is the one thing two goroutines may not share
// without a lock. The two tests here run one handle through two goroutines each,
// which is what a host that keeps a handle and serves concurrent callers from it
// does.
//
// They are red on purpose: they report the race as it stands so that the fix — a
// mutex in each handle, or a doc that limits the promise to the file system
// rather than to its handles — has something to turn green.

// raceDoc is the tree these tests walk: one directory of six leaves.
const raceDoc = `
root:
  one: first leaf
  two: second leaf
  three: third leaf
  four: fourth leaf
  five: fifth leaf
  six: sixth leaf
`

// raceEntries is that directory's listing, whatever order the tree holds it in.
var raceEntries = []string{"five", "four", "one", "six", "three", "two"}

// raceFS builds the file system these tests share.
func raceFS(t *testing.T) fs.FS {
	t.Helper()
	fsys, err := New("race.yaml", []byte(raceDoc))
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	return fsys
}

// TestConcurrentReadDirOnOneHandle walks one directory handle from two
// goroutines. A handle hands out entries from where its last ReadDir left off,
// so two walkers over one handle are only correct while that position stays
// whole: between them they have to be handed every entry exactly once, with none
// skipped and none twice.
func TestConcurrentReadDirOnOneHandle(t *testing.T) {
	t.Parallel()
	fsys := raceFS(t)

	file, err := fsys.Open("root")
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil", "root", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("Close of the directory handle: %v", err)
		}
	}()
	dir, ok := file.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("Open(%q) gave %T, want an fs.ReadDirFile", "root", file)
	}

	const walkers = 2
	const calls = 100

	var mu sync.Mutex
	var seen []string
	var running sync.WaitGroup
	for range walkers {
		running.Go(func() {
			for range calls {
				entries, err := dir.ReadDir(2)
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

	got := slices.Clone(seen)
	slices.Sort(got)
	want := slices.Clone(raceEntries)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("one directory handle walked by %d goroutines handed out %v, want every entry exactly once, of %v",
			walkers, got, want)
	}
}

// TestConcurrentReadOnOneHandle reads one file handle from two goroutines, each
// reading to the end. The handle answers from a *bytes.Reader and moves its
// position on every call, so the two share that position: what they read between
// them has to be the content over exactly once, because a position that moves
// under both hands one byte twice and loses another.
func TestConcurrentReadOnOneHandle(t *testing.T) {
	t.Parallel()
	fsys := raceFS(t)

	file, err := fsys.Open("root/one")
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil", "root/one", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("Close of the file handle: %v", err)
		}
	}()

	const want = "first leaf"
	buf := make([]byte, 3)

	var mu sync.Mutex
	var got []byte
	var running sync.WaitGroup
	for range 2 {
		running.Go(func() {
			for {
				n, err := file.Read(buf)
				mu.Lock()
				got = append(got, buf[:n]...)
				mu.Unlock()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						t.Errorf("Read = %v, want nil or io.EOF", err)
					}
					return
				}
			}
		})
	}
	running.Wait()

	if string(got) != want {
		t.Errorf("one file handle read by two goroutines gave %q (%d bytes), want its content read over exactly once: %q (%d bytes)",
			string(got), len(got), want, len(want))
	}
}
