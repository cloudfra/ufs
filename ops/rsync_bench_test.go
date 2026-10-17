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

package ops_test

import (
	"fmt"
	"testing"

	"github.com/cloudfra/ufs/ops"
)

func BenchmarkRsyncSmall(b *testing.B) {
	src := buildTree(b, 100, 5, 512) // 100 files, ~512B each

	b.ResetTimer()
	for b.Loop() {
		dst, err := newMemFS(fmt.Sprintf("memory://dst%d", b.N))
		if err != nil {
			b.Fatal(err)
		}
		if err := ops.Rsync(src, dst, "."); err != nil {
			b.Fatal(err)
		}
		if err := dst.Close(); err != nil {
			b.Fatal(err)
		}
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
		if err := ops.Rsync(src, dst, "."); err != nil {
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
		if err := ops.Rsync(src, dst, "."); err != nil {
			b.Fatal(err)
		}
		if err := dst.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
