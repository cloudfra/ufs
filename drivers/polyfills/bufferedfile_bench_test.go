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

package polyfills

import (
	"io/fs"
	"testing"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

// bufferedFileBenchSizes mirrors the Small/Medium/Large convention used by the
// benchmarks of the file systems.
var bufferedFileBenchSizes = []struct {
	name string
	n    int
}{
	{"Small", 1 << 10},    // 1 KB
	{"Medium", 100 << 10}, // 100 KB
	{"Large", 10 << 20},   // 10 MB
}

// BenchmarkToBufferedFile measures what it costs to make a stream-only file
// seekable. The memory mode holds the content in a bytes.Reader, so its
// allocation grows with the file; the disk mode copies it to a temporary file,
// which keeps the allocation flat.
func BenchmarkToBufferedFile(b *testing.B) {
	modes := []struct {
		name string
		mode BufferMode
	}{
		{"Memory", BufferMemory},
		{"Disk", BufferDisk},
	}
	for _, mode := range modes {
		b.Run(mode.name, func(b *testing.B) {
			for _, sz := range bufferedFileBenchSizes {
				b.Run(sz.name, func(b *testing.B) {
					content := string(ufsTesting.SeedData('R', sz.n))
					b.SetBytes(int64(sz.n))
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						file, err := ToBufferedFile(newTestBareFile("bench.bin", content), true, mode.mode)
						if err != nil {
							b.Fatal(err)
						}
						if err := file.Close(); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

// BenchmarkToBufferedFileDerivedMethods measures the cost of wrapping a file
// that already implements Seek, ReadAt, or both, where no content is copied:
// the missing method is derived from the one the file has. The cost stays flat
// against the file sizes BenchmarkToBufferedFile uses, because nothing is read.
func BenchmarkToBufferedFileDerivedMethods(b *testing.B) {
	shapes := []struct {
		name string
		new  func(name, content string) fs.File
	}{
		{"SeekOnly", func(name, content string) fs.File { return newTestSeekerOnlyFile(name, content) }},
		{"ReadAtOnly", func(name, content string) fs.File { return newTestReaderAtFile(name, content) }},
		{"SeekAndReadAt", func(name, content string) fs.File { return newTestSeekerFile(name, content) }},
	}
	for _, shape := range shapes {
		b.Run(shape.name, func(b *testing.B) {
			for _, sz := range bufferedFileBenchSizes {
				b.Run(sz.name, func(b *testing.B) {
					content := string(ufsTesting.SeedData('R', sz.n))
					b.SetBytes(int64(sz.n))
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						file, err := ToBufferedFile(shape.new("bench.bin", content), true, BufferMemory)
						if err != nil {
							b.Fatal(err)
						}
						if err := file.Close(); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
