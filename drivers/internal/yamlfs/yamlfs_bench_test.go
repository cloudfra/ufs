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

package yamlfs

import (
	"strconv"
	"testing"
)

func BenchmarkNewDocument(b *testing.B) {
	data := []byte(document)

	b.ReportAllocs()
	for b.Loop() {
		fsys, err := New("document.yaml", data)
		if err != nil {
			b.Fatal(err)
		}
		if err := fsys.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNewAliasFanOut builds documents that reuse one subtree through
// aliases, so its cost tracks the document rather than the tree it describes.
func BenchmarkNewAliasFanOut(b *testing.B) {
	for _, levels := range []int{4, 6, 8, 10} {
		data := []byte(fanOut(levels))
		b.Run("levels"+strconv.Itoa(levels), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				fsys, err := New("fan-out.yaml", data)
				if err != nil {
					b.Fatal(err)
				}
				if err := fsys.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkNewWideMapping builds a mapping of distinct keys, which is the entry
// count the budget of a document with aliases has to cover too.
func BenchmarkNewWideMapping(b *testing.B) {
	for _, keys := range []int{1 << 8, 1 << 12} {
		data := []byte(budgetDocument(keys, 0))
		b.Run("keys"+strconv.Itoa(keys), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				fsys, err := New("wide.yaml", data)
				if err != nil {
					b.Fatal(err)
				}
				if err := fsys.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
