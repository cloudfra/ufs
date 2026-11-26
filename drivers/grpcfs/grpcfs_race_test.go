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

package grpcfs_test

import (
	"errors"
	"io"
	"io/fs"
	"slices"
	"sync"
	"testing"

	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
)

// grpcDir (grpcfs_client_dir.go:33-44) is a client-side view over one ListFiles
// answer: the listing is fetched lazily the first time the handle is walked
// (ReadDir at :65-86, which fills d.entries and sets d.listed at :71) and the
// pagination position is a plain int read at :74, :78, :81 and :82 and written
// at :75 and :83. Nothing guards any of that, so two goroutines walking the
// one handle Open hands out fetch the listing twice over the wire — both are
// seen writing :71 — and can hand the same entries back twice or skip others.
//
// The sibling handle for a file is not written this way: grpcFile keeps its
// position, stream and buffer under a mutex (grpcfs_client_file.go:56), and
// even the metadata that io.SeekEnd is measured from is read and refreshed
// under that same mutex. The file system as a whole is guarded too — Close
// flips an atomic closed flag that every call checks (grpcfs_client.go:207-212)
// — so the directory handle looks like the oversight rather than the convention.
//
// The fetch makes the first pair easy to reach: filling the listing is an rpc,
// so a second walker that arrives while the first is still waiting for it sees
// listed still false and fetches it as well, and both write :71.
//
// The test is red on purpose. The fix that turns it green is open question 1 in
// #748: guard the position and the listing, or narrow the doc so one handle
// belongs to one goroutine.

// raceGRPCDir and raceGRPCEntries name the directory and files the walk works
// on. Six entries and pages of two means the handle is paginated many times
// before the two walkers finish.
const raceGRPCDir = "boxes"

var raceGRPCEntries = []string{"alpha.txt", "beta.txt", "delta.txt", "epsilon.txt", "gamma.txt", "zeta.txt"}

// TestClientDirHandleSharedByTwoWalkers walks one directory handle from two
// goroutines. Between them they must be handed every entry of the listing
// exactly once: none skipped, none twice.
func TestClientDirHandleSharedByTwoWalkers(t *testing.T) {
	t.Parallel()

	fsys := servedClient(t)
	if err := fsys.MkdirAll(raceGRPCDir, 0o777); err != nil {
		t.Fatalf("MkdirAll(%q) = %v, want nil", raceGRPCDir, err)
	}
	for _, name := range raceGRPCEntries {
		ufsdriversTesting.WriteFile(t, fsys, raceGRPCDir+"/"+name, "ufs-race-"+name)
	}

	dir, err := fsys.Open(raceGRPCDir)
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil", raceGRPCDir, err)
	}
	defer func() {
		if err := dir.Close(); err != nil {
			t.Errorf("Close of %q = %v, want nil", raceGRPCDir, err)
		}
	}()
	walker, ok := dir.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("Open(%q) = %T, want an io/fs.ReadDirFile", raceGRPCDir, dir)
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
	want := slices.Clone(raceGRPCEntries)
	slices.Sort(want)
	if !slices.Equal(seen, want) {
		t.Errorf("one handle walked by two goroutines returned %d entries %v, want each of %v exactly once",
			len(seen), seen, want)
	}
}
