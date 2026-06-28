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
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"testing"
)

// seedData returns exactly n bytes of deterministic data based on seed.
func seedData(seed byte, n int) []byte {
	rep := bytes.Repeat([]byte{seed}, 1<<20) // 1 MB max block
	if len(rep) < n {
		return append(rep, bytes.Repeat([]byte{seed ^ 1}, n-len(rep))...)
	}
	return rep[:n]
}

// buildTree creates a localFS-backed directory tree with nFiles files of fileBytes bytes each,
// spread across ~depth directory levels. Returns (localFS, tempDir) so caller can use it directly.
func buildTree(t testing.TB, nFiles int, depth int, fileBytes int) FS {
	t.Helper()
	dir := t.TempDir()
	lfs, err := newLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range nFiles {
		level := i % depth
		subdir := ""
		for l := 0; l < level; l++ {
			subdir += fmt.Sprintf("d%d/", l)
		}
		dirPath := dir + "/" + subdir
		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			t.Fatal(err)
		}
		name := subdir + fmt.Sprintf("file_%d.dat", i)
		data := seedData(byte(i%256), fileBytes)
		lfsPath := dir + "/" + name
		if err := os.WriteFile(lfsPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return lfs
}

// mustMemFS creates a memFS and attaches t.Cleanup to close it.
func mustMemFS(t testing.TB, name string) FS {
	fsys, err := newMemFS(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fsys.Close() })
	return fsys
}

// --- Copy benchmarks (pure memFS — no backend issues) ---

func BenchmarkCopySmall(b *testing.B) {
	src, _ := newMemFS("memory://src")
	f, _ := src.Create("data.bin")
	f.Write(bytes.Repeat([]byte("A"), 1024))
	f.Close()

	for range b.N {
		dst, _ := newMemFS(fmt.Sprintf("memory://dst%d", b.N))
		if err := Copy(src, "data.bin", dst, "out.bin"); err != nil {
			b.Fatal(err)
		}
		dst.Close()
	}
}

func BenchmarkCopyMedium(b *testing.B) {
	src, _ := newMemFS("memory://src")
	f, _ := src.Create("data.bin")
	f.Write(bytes.Repeat([]byte("B"), 100<<10)) // 100 KB
	f.Close()

	for range b.N {
		dst, _ := newMemFS(fmt.Sprintf("memory://dst%d", b.N))
		if err := Copy(src, "data.bin", dst, "out.bin"); err != nil {
			b.Fatal(err)
		}
		dst.Close()
	}
}

func BenchmarkCopyLarge(b *testing.B) {
	src, _ := newMemFS("memory://src")
	f, _ := src.Create("data.bin")
	f.Write(bytes.Repeat([]byte("C"), 10<<20)) // 10 MB
	f.Close()

	for range b.N {
		dst, _ := newMemFS(fmt.Sprintf("memory://dst%d", b.N))
		if err := Copy(src, "data.bin", dst, "out.bin"); err != nil {
			b.Fatal(err)
		}
		dst.Close()
	}
}

// --- Rsync benchmarks (uses localFS for reliable walking with "." root) ---

func BenchmarkRsyncSmall(b *testing.B) {
	src := buildTree(b, 100, 5, 512) // 100 files, ~512B each

	b.ResetTimer()
	for range b.N {
		dst, _ := newMemFS(fmt.Sprintf("memory://dst%d", b.N))
		if err := Rsync(src, dst, "."); err != nil {
			b.Fatal(err)
		}
		dst.Close()
	}
}

func BenchmarkRsyncMedium(b *testing.B) {
	src := buildTree(b, 10_000, 128, 128)

	b.ResetTimer()
	for range b.N {
		dst, _ := newMemFS(fmt.Sprintf("memory://dst%d", b.N))
		if err := Rsync(src, dst, "."); err != nil {
			b.Fatal(err)
		}
		dst.Close()
	}
}

func BenchmarkRsyncLarge(b *testing.B) {
	src := buildTree(b, 100_000, 128, 128)

	b.ResetTimer()
	for range b.N {
		dst, _ := newMemFS(fmt.Sprintf("memory://dst%d", b.N))
		if err := Rsync(src, dst, "."); err != nil {
			b.Fatal(err)
		}
		dst.Close()
	}
}

// --- List benchmarks (use memFS — ListFiles falls through to WalkDir on memFS) ---

func BenchmarkListFilesSmall(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("file_%d.dat", i)
		f, _ := fsys.Create(name)
		data := seedData(byte(i%256), 512)
		f.Write(data)
		f.Close()
	}

	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesMedium(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	for i := 0; i < 10_000; i++ {
		name := fmt.Sprintf("file_%d.dat", i)
		f, _ := fsys.Create(name)
		data := seedData(byte(i%256), 128)
		f.Write(data)
		f.Close()
	}

	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesLarge(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	for i := 0; i < 100_000; i++ {
		name := fmt.Sprintf("file_%d.dat", i)
		f, _ := fsys.Create(name)
		data := seedData(byte(i%256), 128)
		f.Write(data)
		f.Close()
	}

	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListSmall(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	for i := 0; i < 1000; i++ {
		subdir := fmt.Sprintf("sub%d", i%10)
		if i < 10 {
			_ = fsys.MkdirAll(subdir, fs.ModePerm)
		}
		name := subdir + "/file_" + fmt.Sprintf("%d.dat", i)
		f, _ := fsys.Create(name)
		data := seedData(byte(i%256), 64)
		f.Write(data)
		f.Close()
	}

	b.ResetTimer()
	for range b.N {
		if _, err := List(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListMedium(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	for i := 0; i < 10_000; i++ {
		subdir := fmt.Sprintf("sub%d", i%64)
		if i < 64 {
			_ = fsys.MkdirAll(subdir, fs.ModePerm)
		}
		name := subdir + "/file_" + fmt.Sprintf("%d.dat", i)
		f, _ := fsys.Create(name)
		data := seedData(byte(i%256), 64)
		f.Write(data)
		f.Close()
	}

	b.ResetTimer()
	for range b.N {
		if _, err := List(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

// --- ForEachFilename benchmarks (use localFS for reliable walking) ---

func BenchmarkForEachFilenameSmall(b *testing.B) {
	lfs := buildTree(b, 100, 5, 512)

	b.ResetTimer()
	for range b.N {
		count := 0
		_ = ForEachFilename(lfs, ".", func(name string) error {
			count++
			return nil
		})
	}
}

func BenchmarkForEachFilenameMedium(b *testing.B) {
	lfs := buildTree(b, 10_000, 128, 128)

	b.ResetTimer()
	for range b.N {
		count := 0
		_ = ForEachFilename(lfs, ".", func(name string) error {
			count++
			return nil
		})
	}
}

// --- Walk benchmarks (use localFS for reliable walking) ---

func BenchmarkWalkSmall(b *testing.B) {
	lfs := buildTree(b, 100, 5, 512)

	b.ResetTimer()
	for range b.N {
		count := 0
		_ = Walk(lfs, ".", WalkArgs{}, func(name string) error {
			count++
			return nil
		})
	}
}

func BenchmarkWalkMedium(b *testing.B) {
	lfs := buildTree(b, 10_000, 128, 128)

	b.ResetTimer()
	for range b.N {
		count := 0
		_ = Walk(lfs, ".", WalkArgs{}, func(name string) error {
			count++
			return nil
		})
	}
}

func BenchmarkWalkWithExcludes(b *testing.B) {
	lfs := buildTree(b, 10_000, 128, 128)

	b.ResetTimer()
	for range b.N {
		count := 0
		_ = Walk(lfs, ".", WalkArgs{ExcludeDirectory: []string{"d1", "d2"}}, func(name string) error {
			count++
			return nil
		})
	}
}

// --- memFS internal benchmarks ---

func BenchmarkMemFSReadFileSmall(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	f, _ := fsys.Create("small.bin")
	f.Write(bytes.Repeat([]byte("X"), 1<<10)) // 1 KB
	f.Close()

	b.ResetTimer()
	for range b.N {
		if _, err := fs.ReadFile(fsys, "small.bin"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemFSReadFileMedium(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	f, _ := fsys.Create("medium.bin")
	f.Write(bytes.Repeat([]byte("Y"), 100<<10)) // 100 KB
	f.Close()

	b.ResetTimer()
	for range b.N {
		if _, err := fs.ReadFile(fsys, "medium.bin"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemFSReadFileLarge(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	f, _ := fsys.Create("large.bin")
	f.Write(bytes.Repeat([]byte("Z"), 10<<20)) // 10 MB
	f.Close()

	b.ResetTimer()
	for range b.N {
		if _, err := fs.ReadFile(fsys, "large.bin"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemFSGlob(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	for i := 0; i < 10_000; i++ {
		level := i % 256
		subdir := fmt.Sprintf("d%d/", level)
		name := subdir + "file_" + fmt.Sprintf("%d.dat", i)
		f, _ := fsys.Create(name)
		data := seedData(byte(i%256), 1)
		f.Write(data)
		f.Close()
	}

	patterns := []string{"d*/file_*", "*.dat"}
	for _, pattern := range patterns {
		b.Run(pattern, func(b *testing.B) {
			b.ResetTimer()
			for range b.N {
				if _, err := fsys.(fs.GlobFS).Glob(pattern); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMemFSReadDir(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	for i := 1000; i < 2000; i++ {
		name := fmt.Sprintf("entry_%d", i)
		f, _ := fsys.Create(name)
		f.Write([]byte(fmt.Sprintf("data-%d", i)))
		f.Close()
	}

	b.ResetTimer()
	for range b.N {
		if _, err := fs.ReadDir(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemFSCreate(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	data := bytes.Repeat([]byte("D"), 1024)

	b.ResetTimer()
	for range b.N {
		name := fmt.Sprintf("file_%d", b.N)
		f, _ := fsys.Create(name)
		f.Write(data)
		f.Close()
	}
}

func BenchmarkMemFSRemoveAll(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	for i := 0; i < 5000; i++ {
		name := fmt.Sprintf("subdir_%d/file", i)
		f, _ := fsys.Create(name)
		f.Write([]byte("x"))
		f.Close()
	}

	b.ResetTimer()
	for range b.N {
		if err := RemoveAll(fsys, "."); err != nil {
			b.Fatal(err)
		}
		// Rebuild for next iteration
		for i := 0; i < 5000; i++ {
			name := fmt.Sprintf("subdir_%d/file", i)
			f, _ := fsys.Create(name)
			f.Write([]byte("x"))
			f.Close()
		}
	}
}

// --- nestFS benchmarks (uses localFS underneath) ---

func BenchmarkNestFSReadDir(b *testing.B) {
	dir := b.TempDir()
	lfs, _ := newLocalFS(dir)
	for i := 0; i < 1000; i++ {
		name := fmt.Sprintf("file_%d.dat", i)
		data := bytes.Repeat([]byte("Nest"), 128)
		if err := os.WriteFile(dir+"/"+name, data, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	nfs := makeNestFS(context.Background(), lfs)

	b.ResetTimer()
	for range b.N {
		if _, err := nfs.ReadDir("."); err != nil {
			b.Fatal(err)
		}
	}
	nfs.Close()
}

func BenchmarkNestFSReadFile(b *testing.B) {
	dir := b.TempDir()
	lfs, _ := newLocalFS(dir)
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("file_%d.dat", i)
		data := bytes.Repeat([]byte("Nest"), 128)
		if err := os.WriteFile(dir+"/"+name, data, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	nfs := makeNestFS(context.Background(), lfs)

	b.ResetTimer()
	for range b.N {
		if _, err := nfs.ReadFile("file_42.dat"); err != nil {
			b.Fatal(err)
		}
	}
	nfs.Close()
}

// --- localFS benchmarks ---

func BenchmarkLocalFSReadDir(b *testing.B) {
	dir := b.TempDir()
	lfs, _ := newLocalFS(dir)
	data := bytes.Repeat([]byte("LocData"), 64) // ~600B each
	for i := 0; i < 1000; i++ {
		name := fmt.Sprintf("file_%d.dat", i)
		if err := os.WriteFile(dir+"/"+name, data, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	defer lfs.Close()

	b.ResetTimer()
	for range b.N {
		if _, err := lfs.ReadDir("."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLocalFSListFiles(b *testing.B) {
	dir := b.TempDir()
	lfs, _ := newLocalFS(dir)
	data := bytes.Repeat([]byte("L"), 512)
	for i := 0; i < 5000; i++ {
		name := fmt.Sprintf("file_%d.dat", i)
		if err := os.WriteFile(dir+"/"+name, data, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	defer lfs.Close()

	b.ResetTimer()
	for range b.N {
		if _, err := ListFiles(lfs, "."); err != nil {
			b.Fatal(err)
		}
	}
}
