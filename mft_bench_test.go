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

// MFT-accelerated file scanning benchmarks. These require Windows Administrator
// privileges and are the MFT counterparts to the WalkDir benchmarks in
// localfs_bench_test.go. Run both sets together with:
//
//	make bench-root
//
// Tree sizes match localfs_bench_test.go so WalkDir and MFT ns/op are directly
// comparable in the same benchmark run.
//
// Note: MFT benchmarks scan the entire volume MFT and filter to the temp
// subtree, paying a fixed per-volume overhead that WalkDir does not. The
// crossover point where MFT beats WalkDir depends on volume size and subtree
// file count.

package ufs

import (
	"testing"
)

// ---- ForEachFilename via MFT -------------------------------------------------

func BenchmarkForEachFilenameMFTSmall(b *testing.B) {
	requireAdmin(b)
	fsys := initializeFileStructureForTest(b, 5, 20)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := ForEachFilename(fsys, cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilenameMFTMedium(b *testing.B) {
	requireAdmin(b)
	fsys := initializeFileStructureForTest(b, 20, 50)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := ForEachFilename(fsys, cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilenameMFTLarge(b *testing.B) {
	requireAdmin(b)
	fsys := initializeFileStructureForTest(b, 100, 50)
	noop := func(string) error { return nil }
	b.ResetTimer()
	for range b.N {
		if err := ForEachFilename(fsys, cwdPath, noop); err != nil {
			b.Fatal(err)
		}
	}
}

// ---- ListFiles via MFT -------------------------------------------------------

func BenchmarkListFilesMFTSmall(b *testing.B) {
	requireAdmin(b)
	fsys := initializeFileStructureForTest(b, 5, 20)
	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(fsys, cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesMFTMedium(b *testing.B) {
	requireAdmin(b)
	fsys := initializeFileStructureForTest(b, 20, 50)
	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(fsys, cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesMFTLarge(b *testing.B) {
	requireAdmin(b)
	fsys := initializeFileStructureForTest(b, 100, 50)
	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(fsys, cwdPath); err != nil {
			b.Fatal(err)
		}
	}
}
