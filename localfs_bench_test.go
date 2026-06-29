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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// noIterFS wraps any fs.FS as a plain fs.FS, hiding the ForEachFilenameIter,
// ForEachFileInfoIter, and ListFilenames interfaces so that op.go functions
// cannot detect the fast-path and fall back to their fs.WalkDir implementations.
// This gives a fair WalkDir baseline when benchmarking against MFT enumeration.
type noIterFS struct{ fs.FS }

// initializeFileStructureForTest creates a localFS rooted at a temp directory
// containing numDirs subdirectories each with filesPerDir files. Cleanup is
// registered with tb.Cleanup so callers need not close explicitly.
//
// Tree sizes used across bench pairs (encoded in benchmark name suffixes):
//
//	Small:    5 dirs ×   20 files =    100 files
//	Medium:  20 dirs ×   50 files =  1 000 files
//	Large:  100 dirs ×   50 files =  5 000 files
func initializeFileStructureForTest(tb testing.TB, numDirs, filesPerDir int) *localFS {
	tb.Helper()
	dir := tb.TempDir()
	for i := range numDirs {
		subdir := filepath.Join(dir, fmt.Sprintf("d%03d", i))
		if err := os.MkdirAll(subdir, 0o755); err != nil {
			tb.Fatal(err)
		}
		for j := range filesPerDir {
			p := filepath.Join(subdir, fmt.Sprintf("f%04d.txt", j))
			if err := os.WriteFile(p, nil, 0o644); err != nil {
				tb.Fatal(err)
			}
		}
	}
	fsys, err := makeLocalFS(dir)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { fsys.Close() })
	return fsys
}

// ---- ForEachFilename via fs.WalkDir -----------------------------------------

func BenchmarkForEachFilenameWalkDirSmall(b *testing.B) {
	fsys := initializeFileStructureForTest(b, 5, 20)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := ForEachFilename(noIterFS{fsys}, cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilenameWalkDirMedium(b *testing.B) {
	fsys := initializeFileStructureForTest(b, 20, 50)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := ForEachFilename(noIterFS{fsys}, cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilenameWalkDirLarge(b *testing.B) {
	fsys := initializeFileStructureForTest(b, 100, 50)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := ForEachFilename(noIterFS{fsys}, cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

// ---- ListFiles via fs.WalkDir ------------------------------------------------

func BenchmarkListFilesWalkDirSmall(b *testing.B) {
	fsys := initializeFileStructureForTest(b, 5, 20)
	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(noIterFS{fsys}, cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesWalkDirMedium(b *testing.B) {
	fsys := initializeFileStructureForTest(b, 20, 50)
	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(noIterFS{fsys}, cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesWalkDirLarge(b *testing.B) {
	fsys := initializeFileStructureForTest(b, 100, 50)
	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(noIterFS{fsys}, cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}
