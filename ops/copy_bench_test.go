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
	"bytes"
	"fmt"
	"testing"

	"github.com/cloudfra/ufs/ops"
)

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
		if err := ops.Copy(src, "data.bin", dst, "out.bin"); err != nil {
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
		if err := ops.Copy(src, "data.bin", dst, "out.bin"); err != nil {
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
		if err := ops.Copy(src, "data.bin", dst, "out.bin"); err != nil {
			b.Fatal(err)
		}
		if err := dst.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
