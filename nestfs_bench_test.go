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
	"path/filepath"
	"testing"

	"github.com/cloudfra/ufs/internal/osutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// --- nestFS benchmarks (uses localFS underneath) ---

func BenchmarkNestFSReadDir(b *testing.B) {
	dir := b.TempDir()
	lfs := mustBaseFS(b, dir)
	for i := range 1000 {
		name := fmt.Sprintf("file_%d.dat", i)
		data := bytes.Repeat([]byte("Nest"), 128)
		if err := osutil.WriteFile(filepath.Join(dir, name), data); err != nil {
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
	lfs := mustBaseFS(b, dir)
	for i := range 100 {
		name := fmt.Sprintf("file_%d.dat", i)
		data := bytes.Repeat([]byte("Nest"), 128)
		if err := osutil.WriteFile(filepath.Join(dir, name), data); err != nil {
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

// BenchmarkNestFSGlob measures Glob through the nested file system that New
// returns, which lists every directory itself to include mounts and archive
// directories.
func BenchmarkNestFSGlob(b *testing.B) {
	fsys, err := New(b.Context(), "memory://bench")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			b.Fatal(err)
		}
	})
	for i := range 10_000 {
		name := fmt.Sprintf("d%d/file_%d.dat", i%256, i)
		f, err := fsys.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := f.Write(ufsTesting.SeedData(byte(i%256), 1)); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	for _, pattern := range []string{"d*/file_*", "d7/file_*", "*.dat"} {
		b.Run(pattern, func(b *testing.B) {
			for b.Loop() {
				if _, err := fsys.(fs.GlobFS).Glob(pattern); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
