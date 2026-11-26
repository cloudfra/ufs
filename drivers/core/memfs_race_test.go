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
	"io"
	"io/fs"
	"slices"
	"sync"
	"testing"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

// The doc on memDirFile (memfs.go:250-252) says the handle snapshots the
// directory at Open time "so that paginated ReadDir calls are stable even if
// the filesystem is modified concurrently". The snapshot really is detached —
// listDir (memfs.go:352-380) builds fresh entries under the lock — but the
// place the handle has reached in that snapshot is a plain int (memfs.go:256)
// that ReadDir reads at :279 and advances at :287 with nothing guarding it, and
// a concurrent modification is not the only second hand on a handle: a caller
// that keeps one handle and serves two readers from it is the ordinary one.
//
// TestMemFSConcurrentOpenAndTruncate in memfs_test.go:1458 already covers one
// handle against a rewrite of the tree, so this file covers only the half it
// does not. It is red on purpose: it reports the race as it stands so that the
// fix — a mutex in the handle, or a doc that drops the concurrency promise for
// handles — has something to turn green.

// raceMemDirName is the directory the handle walks, and raceMemEntries are the
// files in it, in the order listDir sorts them.
const raceMemDirName = "box"

var raceMemEntries = []string{"alpha", "beta", "delta", "epsilon", "gamma", "zeta"}

// TestMemFSConcurrentReadDirOnOneHandle walks one directory handle from two
// goroutines. A handle answers from where its last ReadDir left off, so two
// walkers over one handle are only correct while that position stays whole:
// between them they have to be handed every entry of the snapshot exactly once,
// with none skipped and none handed out twice.
func TestMemFSConcurrentReadDirOnOneHandle(t *testing.T) {
	t.Parallel()
	fsys, err := newMemFS(t.Context(), "memory://test")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	for _, name := range raceMemEntries {
		file, err := fsys.Create(raceMemDirName + "/" + name)
		if err != nil {
			t.Fatalf("Create(%q) = %v, want nil", raceMemDirName+"/"+name, err)
		}
		if _, err := file.WriteString(name + " content"); err != nil {
			t.Fatalf("WriteString(%q) = %v, want nil", raceMemDirName+"/"+name, err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("Close(%q) = %v, want nil", raceMemDirName+"/"+name, err)
		}
	}

	file, err := fsys.Open(raceMemDirName)
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil", raceMemDirName, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("Close of the directory handle: %v", err)
		}
	}()
	dir, ok := file.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("Open(%q) gave %T, want an fs.ReadDirFile", raceMemDirName, file)
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
	want := slices.Clone(raceMemEntries)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("one directory handle walked by %d goroutines handed out %v, want every entry exactly once, of %v",
			walkers, got, want)
	}
}
