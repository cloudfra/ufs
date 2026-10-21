// Copyright 2026 Jeremy Edwards
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
	"archive/zip"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cloudfra/ufs/internal/osutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestArchiveConcurrentFirstAccess opens a directory that holds an archive and
// has many goroutines reach into the archive at once, before it is mounted or
// indexed. Every one of them must see the archive's contents; the data races
// this guards against only fail under the race detector.
func TestArchiveConcurrentFirstAccess(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	const (
		archiveDir = "single-testassets.zip" + archiveDirExt
		workers    = 8
	)
	for range 10 {
		fsys, err := New(t.Context(), filepath.Join(wd, testAssetsArchivesDir))
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := range workers {
			wg.Go(func() {
				if i%2 == 0 {
					if _, err := fsys.Stat(archiveDir + "/index.html"); err != nil {
						t.Errorf("Stat() = %v, want nil", err)
					}
					return
				}
				entries, err := fsys.ReadDir(archiveDir)
				if err != nil {
					t.Errorf("ReadDir() = %v, want nil", err)
				}
				if len(entries) == 0 {
					t.Error("ReadDir() returned no entries, want the archive's contents")
				}
			})
		}
		wg.Wait()
		if err := fsys.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestArchiveEmptyConcurrentAccess verifies that an archive with no entries
// can be read concurrently. The archive library rebuilds the index of an empty
// archive on every ReadDir, so such an archive never gets to skip the locks.
func TestArchiveEmptyConcurrentAccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	zf, err := osutil.Create(filepath.Join(dir, "empty.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if err := zip.NewWriter(zf).Close(); err != nil {
		t.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}
	fsys, err := New(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	const archiveDir = "empty.zip" + archiveDirExt
	// The first ReadDir runs alone, so the workers below only exercise the
	// state after the first index build.
	if entries, err := fsys.ReadDir(archiveDir); err != nil || len(entries) != 0 {
		t.Fatalf("ReadDir() = %v, %v, want no entries and nil", entries, err)
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for range 50 {
				if i%2 == 0 {
					if _, err := fsys.Stat(archiveDir + "/missing"); !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("Stat() = %v, want fs.ErrNotExist", err)
					}
					continue
				}
				if entries, err := fsys.ReadDir(archiveDir); err != nil || len(entries) != 0 {
					t.Errorf("ReadDir() = %v, %v, want no entries and nil", entries, err)
				}
			}
		})
	}
	wg.Wait()
}
