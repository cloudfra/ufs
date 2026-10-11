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

package core

import (
	"bytes"
	"fmt"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// mustMemFS opens the memory file system name and attaches t.Cleanup to close it.
func mustMemFS(tb testing.TB, name string) ufs.WriteFS {
	tb.Helper()
	fsys := MakeMemFS(name)
	tb.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			tb.Fatal(err)
		}
	})
	return fsys
}

// --- memFS internal benchmarks ---

func BenchmarkMemFSReadFileSmall(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	if err := fsys.MkdirAll(".", fs.ModePerm); err != nil {
		b.Fatal(err)
	}
	f, err := fsys.Create("small.bin")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("X"), 1<<10)); err != nil { // 1 KB
		b.Fatal(err)
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := fs.ReadFile(fsys, "small.bin"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemFSReadFileMedium(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	if err := fsys.MkdirAll(".", fs.ModePerm); err != nil {
		b.Fatal(err)
	}
	f, err := fsys.Create("medium.bin")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("Y"), 100<<10)); err != nil { // 100 KB
		b.Fatal(err)
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := fs.ReadFile(fsys, "medium.bin"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemFSReadFileLarge(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	if err := fsys.MkdirAll(".", fs.ModePerm); err != nil {
		b.Fatal(err)
	}
	f, err := fsys.Create("large.bin")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("Z"), 10<<20)); err != nil { // 10 MB
		b.Fatal(err)
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := fs.ReadFile(fsys, "large.bin"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemFSGlob(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	for i := range 10_000 {
		level := i % 256
		subdir := fmt.Sprintf("d%d/", level)
		name := subdir + "file_" + fmt.Sprintf("%d.dat", i)
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		data := ufsTesting.SeedData(byte(i%256), 1)
		if _, err := f.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	patterns := []string{"d*/file_*", "*.dat"}
	for _, pattern := range patterns {
		b.Run(pattern, func(b *testing.B) {
			b.ResetTimer()
			for b.Loop() {
				if _, err := fsys.(fs.GlobFS).Glob(pattern); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMemFSReadDir(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	if err := fsys.MkdirAll(".", fs.ModePerm); err != nil {
		b.Fatal(err)
	}
	for i := 1000; i < 2000; i++ {
		name := fmt.Sprintf("entry_%d", i)
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := fmt.Fprintf(f, "data-%d", i); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := fs.ReadDir(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemFSCreate(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	if err := fsys.MkdirAll(".", fs.ModePerm); err != nil {
		b.Fatal(err)
	}
	data := bytes.Repeat([]byte("D"), 1024)

	b.ResetTimer()
	for b.Loop() {
		name := fmt.Sprintf("file_%d", b.N)
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemFSRemoveAll(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	if err := fsys.MkdirAll(".", fs.ModePerm); err != nil {
		b.Fatal(err)
	}
	for i := range 5000 {
		name := fmt.Sprintf("subdir_%d/file", i)
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := f.Write([]byte("x")); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		if err := fsys.RemoveAll("."); err != nil {
			b.Fatal(err)
		}
		// Rebuild for next iteration
		for i := range 5000 {
			name := fmt.Sprintf("subdir_%d/file", i)
			f, err := fsys.Create(name)
			if err != nil {
				b.Fatal(err)
			}
			if n, err := f.Write([]byte("x")); err != nil {
				b.Fatal(err)
			} else if n != 1 {
				b.Fatalf("Write() = %d, want 1", n)
			}

			if err := f.Close(); err != nil {
				b.Fatal(err)
			}
		}
	}
}
