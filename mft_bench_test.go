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

//go:build windows && root

// Benchmarks comparing MFT-accelerated file scanning against fs.WalkDir, both
// exercised through the op.go public API (ForEachFilename, ListFiles).
//
// Run on an elevated Windows shell with:
//
//	make bench-root
//
// Each WalkDir/MFT pair uses an identical tree so ns/op can be compared
// directly.  Tree sizes (encoded in benchmark names):
//
//	Small:    5 dirs ×   20 files =    100 files
//	Medium:  20 dirs ×   50 files =  1 000 files
//	Large:  100 dirs ×   50 files =  5 000 files
//
// WalkDir benchmarks pass the localFS through a noIterFS wrapper that hides
// the ForEachFilenameIter / ListFilenames interfaces, forcing op.go to fall
// back to its fs.WalkDir path.  MFT benchmarks pass the localFS directly so
// op.go detects the interface and uses MFT enumeration.
//
// Note: MFT benchmarks scan the entire volume MFT and filter to the temp
// subtree, paying a fixed per-volume overhead that WalkDir does not.  The
// crossover point where MFT beats WalkDir depends on volume size and subtree
// file count.

package ufs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// noIterFS wraps a localFS as a plain fs.FS, hiding the ForEachFilenameIter,
// ForEachFileInfoIter, and ListFilenames interfaces.  op.go functions that
// receive a noIterFS cannot detect the fast-path interfaces and fall back to
// their fs.WalkDir implementations, giving a fair baseline for comparison.
type noIterFS struct{ fs.FS }

// buildMFTBenchTree creates a localFS rooted at a temp directory containing
// numDirs subdirectories each with filesPerDir files.  Cleanup is registered
// via b.Cleanup.
func buildMFTBenchTree(b *testing.B, numDirs, filesPerDir int) *localFS {
	b.Helper()
	dir := b.TempDir()
	for i := range numDirs {
		subdir := filepath.Join(dir, fmt.Sprintf("d%03d", i))
		if err := os.MkdirAll(subdir, 0o755); err != nil {
			b.Fatal(err)
		}
		for j := range filesPerDir {
			path := filepath.Join(subdir, fmt.Sprintf("f%04d.txt", j))
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	fsys, err := makeLocalFS(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { fsys.Close() })
	return fsys
}

// ---- ForEachFilename: WalkDir vs MFT (via op.go) ----------------------------

func BenchmarkForEachFilenameWalkDirSmall(b *testing.B) {
	fsys := buildMFTBenchTree(b, 5, 20)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := ForEachFilename(noIterFS{fsys}, cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilenameMFTSmall(b *testing.B) {
	requireAdmin(b)
	fsys := buildMFTBenchTree(b, 5, 20)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := ForEachFilename(fsys, cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilenameWalkDirMedium(b *testing.B) {
	fsys := buildMFTBenchTree(b, 20, 50)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := ForEachFilename(noIterFS{fsys}, cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilenameMFTMedium(b *testing.B) {
	requireAdmin(b)
	fsys := buildMFTBenchTree(b, 20, 50)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := ForEachFilename(fsys, cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilenameWalkDirLarge(b *testing.B) {
	fsys := buildMFTBenchTree(b, 100, 50)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := ForEachFilename(noIterFS{fsys}, cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilenameMFTLarge(b *testing.B) {
	requireAdmin(b)
	fsys := buildMFTBenchTree(b, 100, 50)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := ForEachFilename(fsys, cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

// ---- ListFiles: WalkDir vs MFT (via op.go) -----------------------------------

func BenchmarkListFilesWalkDirSmall(b *testing.B) {
	fsys := buildMFTBenchTree(b, 5, 20)
	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(noIterFS{fsys}, cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesMFTSmall(b *testing.B) {
	requireAdmin(b)
	fsys := buildMFTBenchTree(b, 5, 20)
	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(fsys, cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesWalkDirMedium(b *testing.B) {
	fsys := buildMFTBenchTree(b, 20, 50)
	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(noIterFS{fsys}, cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesMFTMedium(b *testing.B) {
	requireAdmin(b)
	fsys := buildMFTBenchTree(b, 20, 50)
	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(fsys, cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesWalkDirLarge(b *testing.B) {
	fsys := buildMFTBenchTree(b, 100, 50)
	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(noIterFS{fsys}, cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesMFTLarge(b *testing.B) {
	requireAdmin(b)
	fsys := buildMFTBenchTree(b, 100, 50)
	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(fsys, cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}
