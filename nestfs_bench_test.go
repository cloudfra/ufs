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
	"path/filepath"
	"testing"

	"github.com/cloudfra/ufs/internal/osutil"
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
