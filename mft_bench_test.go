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

// Benchmarks comparing MFT-accelerated path enumeration against fs.WalkDir.
// Run on an elevated Windows shell with:
//
//	go test -bench=. -benchmem -tags root ./...
//
// Each WalkDir/MFT pair uses the same tree so ns/op can be compared directly.
// Tree sizes (encoded in benchmark names):
//
//	Small:    5 dirs ×   20 files =    100 files
//	Medium:  20 dirs ×   50 files =  1 000 files
//	Large:  100 dirs ×   50 files =  5 000 files
//
// Note: MFT benchmarks scan the entire volume MFT and filter to the temp
// subtree, so they pay a fixed per-volume overhead that WalkDir does not.
// The crossover point where MFT beats WalkDir depends on volume size and
// the number of files in the subtree.

package ufs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// buildMFTBenchTree creates a localFS rooted at a temp directory containing
// numDirs subdirectories each with filesPerDir files. Cleanup is via b.Cleanup.
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

// ---- ListFilenames: WalkDir vs MFT -----------------------------------------

func BenchmarkListFilenamesWalkDirSmall(b *testing.B) {
	fsys := buildMFTBenchTree(b, 5, 20)
	b.ResetTimer()
	for range b.N {
		if _, err := fsys.listFilenamesWalkDir(cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilenamesMFTSmall(b *testing.B) {
	requireAdmin(b)
	fsys := buildMFTBenchTree(b, 5, 20)
	b.ResetTimer()
	for range b.N {
		if _, err := fsys.ListFilenames(cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilenamesWalkDirMedium(b *testing.B) {
	fsys := buildMFTBenchTree(b, 20, 50)
	b.ResetTimer()
	for range b.N {
		if _, err := fsys.listFilenamesWalkDir(cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilenamesMFTMedium(b *testing.B) {
	requireAdmin(b)
	fsys := buildMFTBenchTree(b, 20, 50)
	b.ResetTimer()
	for range b.N {
		if _, err := fsys.ListFilenames(cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilenamesWalkDirLarge(b *testing.B) {
	fsys := buildMFTBenchTree(b, 100, 50)
	b.ResetTimer()
	for range b.N {
		if _, err := fsys.listFilenamesWalkDir(cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilenamesMFTLarge(b *testing.B) {
	requireAdmin(b)
	fsys := buildMFTBenchTree(b, 100, 50)
	b.ResetTimer()
	for range b.N {
		if _, err := fsys.ListFilenames(cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

// ---- ForEachFilename: WalkDir vs MFT ----------------------------------------

func BenchmarkForEachFilenameWalkDirSmall(b *testing.B) {
	fsys := buildMFTBenchTree(b, 5, 20)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := fsys.forEachFilenameWalkDir(cwdPath, noop); err != nil {
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
		if err := fsys.ForEachFilename(cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilenameWalkDirMedium(b *testing.B) {
	fsys := buildMFTBenchTree(b, 20, 50)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := fsys.forEachFilenameWalkDir(cwdPath, noop); err != nil {
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
		if err := fsys.ForEachFilename(cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilenameWalkDirLarge(b *testing.B) {
	fsys := buildMFTBenchTree(b, 100, 50)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := fsys.forEachFilenameWalkDir(cwdPath, noop); err != nil {
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
		if err := fsys.ForEachFilename(cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}
