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
		for l := range level {
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
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return fsys
}

// --- Copy benchmarks (pure memFS — no backend issues) ---

func BenchmarkCopySmall(b *testing.B) {
	src, err := newMemFS("memory://src")
	if err != nil {
		b.Fatal(err)
	}
	f, err := src.Create("data.bin")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("A"), 1024)); err != nil {
		b.Fatal(err)
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}

	for b.Loop() {
		dst, err := newMemFS(fmt.Sprintf("memory://dst%d", b.N))
		if err != nil {
			b.Fatal(err)
		}
		if err := Copy(src, "data.bin", dst, "out.bin"); err != nil {
			b.Fatal(err)
		}
		if err := dst.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCopyMedium(b *testing.B) {
	src, err := newMemFS("memory://src")
	if err != nil {
		b.Fatal(err)
	}
	f, err := src.Create("data.bin")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("B"), 100<<10)); err != nil { // 100 KB
		b.Fatal(err)
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}

	for b.Loop() {
		dst, err := newMemFS(fmt.Sprintf("memory://dst%d", b.N))
		if err != nil {
			b.Fatal(err)
		}
		if err := Copy(src, "data.bin", dst, "out.bin"); err != nil {
			b.Fatal(err)
		}
		if err := dst.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCopyLarge(b *testing.B) {
	src, err := newMemFS("memory://src")
	if err != nil {
		b.Fatal(err)
	}
	f, err := src.Create("data.bin")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("C"), 10<<20)); err != nil { // 10 MB
		b.Fatal(err)
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}

	for b.Loop() {
		dst, err := newMemFS(fmt.Sprintf("memory://dst%d", b.N))
		if err != nil {
			b.Fatal(err)
		}
		if err := Copy(src, "data.bin", dst, "out.bin"); err != nil {
			b.Fatal(err)
		}
		if err := dst.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// --- Rsync benchmarks (uses localFS for reliable walking with "." root) ---

func BenchmarkRsyncSmall(b *testing.B) {
	src := buildTree(b, 100, 5, 512) // 100 files, ~512B each

	b.ResetTimer()
	for b.Loop() {
		dst, err := newMemFS(fmt.Sprintf("memory://dst%d", b.N))
		if err != nil {
			b.Fatal(err)
		}
		if err := Rsync(src, dst, "."); err != nil {
			b.Fatal(err)
		}
		dst.Close()
	}
}

func BenchmarkRsyncMedium(b *testing.B) {
	src := buildTree(b, 10_000, 128, 128)

	b.ResetTimer()
	for b.Loop() {
		dst, err := newMemFS(fmt.Sprintf("memory://dst%d", b.N))
		if err != nil {
			b.Fatal(err)
		}
		if err := Rsync(src, dst, "."); err != nil {
			b.Fatal(err)
		}
		if err := dst.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRsyncLarge(b *testing.B) {
	src := buildTree(b, 100_000, 128, 128)

	b.ResetTimer()
	for b.Loop() {
		dst, err := newMemFS(fmt.Sprintf("memory://dst%d", b.N))
		if err != nil {
			b.Fatal(err)
		}
		if err := Rsync(src, dst, "."); err != nil {
			b.Fatal(err)
		}
		if err := dst.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// --- List benchmarks (use memFS — ListFiles falls through to WalkDir on memFS) ---

func BenchmarkListFilesSmall(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	for i := range 100 {
		name := fmt.Sprintf("file_%d.dat", i)
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		data := seedData(byte(i%256), 512)
		if _, err := f.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := ListFiles(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesMedium(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	for i := range 10_000 {
		name := fmt.Sprintf("file_%d.dat", i)
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		data := seedData(byte(i%256), 128)
		if _, err := f.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := ListFiles(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesLarge(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	if err := fsys.MkdirAll(".", fs.ModePerm); err != nil {
		b.Fatal(err)
	}
	for i := range 100_000 {
		name := fmt.Sprintf("file_%d.dat", i)
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		data := seedData(byte(i%256), 128)
		if _, err := f.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := ListFiles(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListSmall(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	for i := range 1000 {
		subdir := fmt.Sprintf("sub%d", i%10)
		if i < 10 {
			if err := fsys.MkdirAll(subdir, fs.ModePerm); err != nil {
				b.Fatal(err)
			}
		}
		name := subdir + "/file_" + fmt.Sprintf("%d.dat", i)
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		data := seedData(byte(i%256), 64)
		if _, err := f.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := List(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListMedium(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
	for i := range 10_000 {
		subdir := fmt.Sprintf("sub%d", i%64)
		if i < 64 {
			if err := fsys.MkdirAll(subdir, fs.ModePerm); err != nil {
				b.Fatal(err)
			}
		}
		name := subdir + "/file_" + fmt.Sprintf("%d.dat", i)
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		data := seedData(byte(i%256), 64)
		if _, err := f.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := List(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

// --- ForEachFilename benchmarks (use localFS for reliable walking) ---

func BenchmarkForEachFilenameSmall(b *testing.B) {
	lfs := buildTree(b, 100, 5, 512)

	b.ResetTimer()
	for b.Loop() {
		count := 0
		if err := ForEachFilename(lfs, ".", func(_ string) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilenameMedium(b *testing.B) {
	lfs := buildTree(b, 10_000, 128, 128)

	b.ResetTimer()
	for b.Loop() {
		count := 0
		if err := ForEachFilename(lfs, ".", func(_ string) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// --- Walk benchmarks (use localFS for reliable walking) ---

func BenchmarkWalkSmall(b *testing.B) {
	lfs := buildTree(b, 100, 5, 512)

	b.ResetTimer()
	for b.Loop() {
		count := 0
		if err := Walk(lfs, ".", WalkArgs{}, func(_ string) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWalkMedium(b *testing.B) {
	lfs := buildTree(b, 10_000, 128, 128)

	b.ResetTimer()
	for b.Loop() {
		count := 0
		if err := Walk(lfs, ".", WalkArgs{}, func(_ string) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWalkWithExcludes(b *testing.B) {
	lfs := buildTree(b, 10_000, 128, 128)

	b.ResetTimer()
	for b.Loop() {
		count := 0
		if err := Walk(lfs, ".", WalkArgs{ExcludeDirectory: []string{"d1", "d2"}}, func(_ string) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// --- memFS internal benchmarks ---

func BenchmarkMemFSReadFileSmall(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	fsys.MkdirAll(".", fs.ModePerm)
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
	fsys.MkdirAll(".", fs.ModePerm)
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
	fsys.MkdirAll(".", fs.ModePerm)
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
		data := seedData(byte(i%256), 1)
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
		if _, err := f.Write([]byte(fmt.Sprintf("data-%d", i))); err != nil {
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
	fsys.MkdirAll(".", fs.ModePerm)
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
		if err := RemoveAll(fsys, "."); err != nil {
			b.Fatal(err)
		}
		// Rebuild for next iteration
		for i := range 5000 {
			name := fmt.Sprintf("subdir_%d/file", i)
			f, err := fsys.Create(name)
			if err != nil {
				b.Fatal(err)
			}
			f.Write([]byte("x"))

			if err := f.Close(); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// --- nestFS benchmarks (uses localFS underneath) ---

func BenchmarkNestFSReadDir(b *testing.B) {
	dir := b.TempDir()
	lfs, err := newLocalFS(dir)
	if err != nil {
		b.Fatal(err)
	}
	for i := range 1000 {
		name := fmt.Sprintf("file_%d.dat", i)
		data := bytes.Repeat([]byte("Nest"), 128)
		if err := os.WriteFile(dir+"/"+name, data, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	nfs := makeNestFS(context.Background(), lfs)

	b.ResetTimer()
	for b.Loop() {
		if _, err := nfs.ReadDir("."); err != nil {
			b.Fatal(err)
		}
	}
	if err := nfs.Close(); err != nil {
		b.Errorf("failed to close nfs: %v", err)
	}
}

func BenchmarkNestFSReadFile(b *testing.B) {
	dir := b.TempDir()
	lfs, err := newLocalFS(dir)
	if err != nil {
		b.Fatal(err)
	}
	for i := range 100 {
		name := fmt.Sprintf("file_%d.dat", i)
		data := bytes.Repeat([]byte("Nest"), 128)
		if err := os.WriteFile(dir+"/"+name, data, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	nfs := makeNestFS(context.Background(), lfs)

	b.ResetTimer()
	for b.Loop() {
		if _, err := nfs.ReadFile("file_42.dat"); err != nil {
			b.Fatal(err)
		}
	}
	if err := nfs.Close(); err != nil {
		b.Errorf("failed to close nfs: %v", err)
	}
}

// --- localFS benchmarks ---

func BenchmarkLocalFSReadDir(b *testing.B) {
	dir := b.TempDir()
	lfs, err := newLocalFS(dir)
	if err != nil {
		b.Fatal(err)
	}
	data := bytes.Repeat([]byte("LocData"), 64) // ~600B each
	for i := range 1000 {
		name := fmt.Sprintf("file_%d.dat", i)
		if err := os.WriteFile(dir+"/"+name, data, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	defer func() {
		if err := lfs.Close(); err != nil {
			b.Errorf("failed to close lfs: %v", err)
		}
	}()

	b.ResetTimer()
	for b.Loop() {
		if _, err := lfs.ReadDir("."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLocalFSListFiles(b *testing.B) {
	dir := b.TempDir()
	lfs, err := newLocalFS(dir)
	if err != nil {
		b.Fatal(err)
	}
	data := bytes.Repeat([]byte("L"), 512)
	for i := range 5000 {
		name := fmt.Sprintf("file_%d.dat", i)
		if err := os.WriteFile(dir+"/"+name, data, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	defer func() {
		if err := lfs.Close(); err != nil {
			b.Errorf("failed to close lfs: %v", err)
		}
	}()

	b.ResetTimer()
	for b.Loop() {
		if _, err := ListFiles(lfs, "."); err != nil {
			b.Fatal(err)
		}
	}
}
