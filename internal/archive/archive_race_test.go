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
	"sync"
	"testing"
)

// Close used to make itself idempotent by writing nil into the fields every
// operation reads unlocked, so an operation racing it met a nil archive and
// panicked. Close now leaves them set and records closedness in an atomic flag;
// TestArchiveFSOperationsDuringClose checks that an operation racing Close
// either succeeds or reports fs.ErrClosed, never a panic or a data race.

// raceArchiveDir and raceArchiveEntries name the directory and files the test
// works on. The zip is built here rather than taken from
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
