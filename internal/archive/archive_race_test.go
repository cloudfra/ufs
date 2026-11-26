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

package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"sync"
	"testing"
)

// This package has two unsynchronized windows, and both are exposed here
// because they belong to this component (issue #748 keeps one exposure file per
// component).
//
// The first is the archive handle itself. Close (archive.go:270-278) makes
// itself idempotent by writing nil into fsys.fsys at :271 and into fsys.closer
// at :274 - TestCloseIdempotent (archive_test.go:305) is what that nil write is
// for - while every operation reads fsys.fsys unlocked: Open at :166 and :169,
// Stat at :245, ReadDir at :258. Nothing checks for nil on the way in, so the
// operation that loses the race does not get an error, it gets a method call on
// a nil archive and the process dies. The recover in the operation counts those
// crashes and reports them rather than hiding them, because an unrecovered
// panic would take the rest of the package's tests with it.
//
// The second is the directory handle snapshotDirFileHandle returns for a
// directory (archive.go:207-236): the listing is filled in lazily at :220-221
// and the position is a plain int read at :223 and advanced at :225 and :234,
// with nothing guarding either. Two readers on the one handle a host gets from
// Open are handed the same entries twice and skip others.
//
// Both tests are red on purpose. The fixes they turn green are the same two the
// other rows of #748 need: guard the inner handle (open question 3), and guard
// the pagination position or narrow the doc for one handle (open question 1).

// raceArchiveDir and raceArchiveEntries name the directory and files the two
// tests work on. The zip is built here rather than taken from
// testing/testassets so that the file needs nothing generated to run.
const raceArchiveDir = "box"

var raceArchiveEntries = []string{"alpha.txt", "beta.txt", "delta.txt", "epsilon.txt", "gamma.txt", "zeta.txt"}

// raceArchiveZip writes a zip holding raceArchiveEntries under raceArchiveDir
// and returns its path.
func raceArchiveZip(t *testing.T) string {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "race-*.zip")
	if err != nil {
		t.Fatalf("os.CreateTemp = %v, want nil", err)
	}
	path := file.Name()
	writer := zip.NewWriter(file)
	for _, name := range raceArchiveEntries {
		entry, err := writer.Create(raceArchiveDir + "/" + name)
		if err != nil {
			t.Fatalf("zip Create(%q) = %v, want nil", name, err)
		}
		if _, err := io.WriteString(entry, "ufs-race-"+name); err != nil {
			t.Fatalf("write %q = %v, want nil", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zip writer Close = %v, want nil", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}
	return path
}

// TestArchiveFSOperationsDuringClose runs the ordinary Open path against Close.
// What a caller is allowed to see is a file or an error matching fs.ErrClosed:
// never a crash.
func TestArchiveFSOperationsDuringClose(t *testing.T) {
	t.Parallel()

	fsys, err := New(t.Context(), raceArchiveZip(t))
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}

	const opens = 200
	name := raceArchiveDir + "/" + raceArchiveEntries[0]

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
				opened, err := fsys.Open(name)
				if err != nil {
					return err
				}
				if err := opened.Close(); err != nil && !errors.Is(err, fs.ErrClosed) {
					return fmt.Errorf("Close of %q = %w, want nil", name, err)
				}
				return nil
			}()
			if err != nil && !errors.Is(err, fs.ErrClosed) {
				t.Errorf("Open(%q) while closing = %v, want nil or an error matching fs.ErrClosed", name, err)
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

// TestConcurrentReadDirOnOneHandle walks one directory handle from two
// goroutines. Between them they must be handed every entry of the listing
// exactly once: none skipped, none twice.
func TestConcurrentReadDirOnOneHandle(t *testing.T) {
	t.Parallel()

	fsys, err := New(t.Context(), raceArchiveZip(t))
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close = %v, want nil", err)
		}
	}()

	dir, err := fsys.Open(raceArchiveDir)
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil", raceArchiveDir, err)
	}
	defer func() {
		if err := dir.Close(); err != nil {
			t.Errorf("Close of %q = %v, want nil", raceArchiveDir, err)
		}
	}()
	walker, ok := dir.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("Open(%q) = %T, want an io/fs.ReadDirFile", raceArchiveDir, dir)
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
	want := slices.Clone(raceArchiveEntries)
	slices.Sort(want)
	if !slices.Equal(seen, want) {
		t.Errorf("one handle walked by two goroutines returned %d entries %v, want each of %v exactly once",
			len(seen), seen, want)
	}
}
