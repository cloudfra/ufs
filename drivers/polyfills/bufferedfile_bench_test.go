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
	"bytes"
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

// The stand-ins below wrap one shared content slice per size rather than taking
// the string the tests pass. The file types are bytes.Reader-backed, so each
// iteration gets a fresh file over the same backing array and a benchmark row
// measures the polyfill instead of the fixture.
func newBenchBareFile(content []byte) fs.File {
	return &testBareFile{r: bytes.NewReader(content), name: "bench.bin"}
}

func newBenchSeekerOnlyFile(content []byte) fs.File {
	return &testSeekerOnlyFile{r: bytes.NewReader(content), name: "bench.bin"}
}

func newBenchReaderAtFile(content []byte) fs.File {
	return &testReaderAtFile{r: bytes.NewReader(content), name: "bench.bin"}
}

func newBenchSeekerFile(content []byte) fs.File {
	return &testSeekerFile{r: bytes.NewReader(content), name: "bench.bin"}
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
					content := ufsTesting.SeedData('R', sz.n)
					b.SetBytes(int64(sz.n))
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						file, err := ToBufferedFile(newBenchBareFile(content), true, mode.mode)
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
// the missing method is derived from the one the file has. Because nothing is
// read, its rows stay flat across the sizes BenchmarkToBufferedFile uses.
func BenchmarkToBufferedFileDerivedMethods(b *testing.B) {
	shapes := []struct {
		name string
		new  func(content []byte) fs.File
	}{
		{"SeekOnly", newBenchSeekerOnlyFile},
		{"ReadAtOnly", newBenchReaderAtFile},
		{"SeekAndReadAt", newBenchSeekerFile},
	}
	for _, shape := range shapes {
		b.Run(shape.name, func(b *testing.B) {
			for _, sz := range bufferedFileBenchSizes {
				b.Run(sz.name, func(b *testing.B) {
					content := ufsTesting.SeedData('R', sz.n)
					b.SetBytes(int64(sz.n))
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						file, err := ToBufferedFile(shape.new(content), true, BufferMemory)
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
