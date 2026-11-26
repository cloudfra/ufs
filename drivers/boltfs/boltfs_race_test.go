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

package boltfs

import (
	"errors"
	"io"
	"io/fs"
	"slices"
	"sync"
	"testing"
)

// boltDirFile (boltfs.go:265-271) snapshots a directory listing at Open time so
// that paginated ReadDir calls stay stable while the file system is modified —
// that is what the doc comment at :262-264 promises, and the snapshot itself is
// read-only, so it is a sound design. What is not guarded is the pagination
// position that goes with it: offset is read at :287, :291, :294 and :295 and
// written at :288 and :296, with nothing held, and Open hands one of these
// handles to every caller that opens a directory. Two callers paginating the
// same handle move one position between them, so one of them gets the entries
// the other was supposed to get and the other walks off the end early.
//
// This is the only window this driver exposes, and the reason it stands out is
// that everything else here is guarded: Close takes fsys.mu and nils the
// database (:542-553), and every operation reaches the database through the
// mutex-guarded liveDB lookup (:443-451). The handle was simply left out.
//
// The test is red on purpose. The fix that turns it green is open question 1 in
// #748: guard the position, or narrow the doc so one handle belongs to one
// goroutine.

// raceBoltDir and raceBoltEntries name the directory and files the walk works
// on. Six entries paginated two at a time means the handle is stepped many
// times before the two walkers finish.
const raceBoltDir = "boxes"

var raceBoltEntries = []string{"alpha.txt", "beta.txt", "delta.txt", "epsilon.txt", "gamma.txt", "zeta.txt"}

// TestBoltDirHandleSharedByTwoWalkers walks one directory handle from two
// goroutines. Between them they must be handed every entry of the snapshot
// exactly once: none skipped, none twice.
func TestBoltDirHandleSharedByTwoWalkers(t *testing.T) {
	t.Parallel()

	fsys := newTestBoltFS(t)
	if err := fsys.MkdirAll(raceBoltDir, fs.ModePerm); err != nil {
		t.Fatalf("MkdirAll(%q) = %v, want nil", raceBoltDir, err)
	}
	for _, name := range raceBoltEntries {
		file, err := fsys.Create(raceBoltDir + "/" + name)
		if err != nil {
			t.Fatalf("Create(%q) = %v, want nil", name, err)
		}
		if _, err := io.WriteString(file, "ufs-race-"+name); err != nil {
			t.Fatalf("write %q = %v, want nil", name, err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("Close of %q = %v, want nil", name, err)
		}
	}

	dir, err := fsys.Open(raceBoltDir)
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil", raceBoltDir, err)
	}
	defer func() {
		if err := dir.Close(); err != nil {
			t.Errorf("Close of %q = %v, want nil", raceBoltDir, err)
		}
	}()
	walker, ok := dir.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("Open(%q) = %T, want an io/fs.ReadDirFile", raceBoltDir, dir)
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

	slices.Sort(seen)
	want := slices.Clone(raceBoltEntries)
	slices.Sort(want)
	if !slices.Equal(seen, want) {
		t.Errorf("one handle walked by two goroutines returned %d entries %v, want each of %v exactly once",
			len(seen), seen, want)
	}
}
