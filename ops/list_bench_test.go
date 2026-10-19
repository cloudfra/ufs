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

package ops_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/ops"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func BenchmarkListFilesSmall(b *testing.B) {
	fsys := mustMemFS(b, "memory://")
	if err := fsys.MkdirAll(".", fs.ModePerm); err != nil {
		b.Fatal(err)
	}
	for i := range 100 {
		name := fmt.Sprintf("file_%d.dat", i)
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		data := ufsTesting.SeedData(byte(i%256), 512)
		if _, err := f.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := ops.ListFiles(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFilesMedium(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	if err := fsys.MkdirAll(".", fs.ModePerm); err != nil {
		b.Fatal(err)
	}
	for i := range 10_000 {
		name := fmt.Sprintf("file_%d.dat", i)
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		data := ufsTesting.SeedData(byte(i%256), 128)
		if _, err := f.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := ops.ListFiles(fsys, "."); err != nil {
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
		data := ufsTesting.SeedData(byte(i%256), 128)
		if _, err := f.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := ops.ListFiles(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListSmall(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	if err := fsys.MkdirAll(".", fs.ModePerm); err != nil {
		b.Fatal(err)
	}
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
		data := ufsTesting.SeedData(byte(i%256), 64)
		if _, err := f.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := ops.List(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListMedium(b *testing.B) {
	fsys := mustMemFS(b, "memory://bench")
	if err := fsys.MkdirAll(".", fs.ModePerm); err != nil {
		b.Fatal(err)
	}
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
		data := ufsTesting.SeedData(byte(i%256), 64)
		if _, err := f.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := ops.List(fsys, "."); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLocalFSListFiles(b *testing.B) {
	dir := b.TempDir()
	lfs, err := ufs.New(b.Context(), dir)
	if err != nil {
		b.Fatal(err)
	}
	data := bytes.Repeat([]byte("L"), 512)
	for i := range 5000 {
		name := fmt.Sprintf("file_%d.dat", i)
		if err := osutil.WriteFile(filepath.Join(dir, name), data); err != nil {
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
		if _, err := ops.ListFiles(lfs, "."); err != nil {
			b.Fatal(err)
		}
	}
}
