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
	"testing"
)

const benchFileCount = 1000

// makeBenchMemFS builds a memFS with n files spread across n/100 directories.
// Layout: "d000/f000.txt", "d000/f001.txt", ..., "d009/f099.txt"
func makeBenchMemFS(b *testing.B, n int) *memFS {
	b.Helper()
	fsys := makeMemFS("memory://bench")
	dirsPerFile := 100
	for i := range n {
		dir := fmt.Sprintf("d%03d", i/dirsPerFile)
		name := fmt.Sprintf("%s/f%03d.txt", dir, i%dirsPerFile)
		if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
			b.Fatal(err)
		}
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		f.Close()
	}
	return fsys
}

// --- op.go benchmarks ---

func BenchmarkList(b *testing.B) {
	fsys := makeBenchMemFS(b, benchFileCount)
	defer fsys.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := List(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFiles(b *testing.B) {
	fsys := makeBenchMemFS(b, benchFileCount)
	defer fsys.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := ListFiles(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilename(b *testing.B) {
	fsys := makeBenchMemFS(b, benchFileCount)
	defer fsys.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := ForEachFilename(fsys, ".", func(string) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFileInfo(b *testing.B) {
	fsys := makeBenchMemFS(b, benchFileCount)
	defer fsys.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := ForEachFileInfo(fsys, ".", func(fs.FileInfo) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

// --- memFS internal benchmarks ---

func BenchmarkMemFSListDir(b *testing.B) {
	// Benchmark listing a single large directory (all files in one dir).
	fsys := makeMemFS("memory://bench")
	defer fsys.Close()
	if err := fsys.MkdirAll("bigdir", fs.ModePerm); err != nil {
		b.Fatal(err)
	}
	for i := range benchFileCount {
		name := fmt.Sprintf("bigdir/f%04d.txt", i)
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		f.Close()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := fsys.listDir("bigdir"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemFSGlob(b *testing.B) {
	fsys := makeBenchMemFS(b, benchFileCount)
	defer fsys.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := fsys.Glob("d000/*.txt"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemFileWrite(b *testing.B) {
	fsys := makeMemFS("memory://bench")
	defer fsys.Close()
	f, err := fsys.Create("bench.txt")
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	payload := make([]byte, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := f.Write(payload); err != nil {
			b.Fatal(err)
		}
	}
}
