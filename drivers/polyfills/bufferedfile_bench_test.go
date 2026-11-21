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
	"fmt"
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

// The stand-ins below match the test files of the same name except that they
// take the content as it is already stored. The test constructors convert a
// string, which at 10 MB would put a copy of the fixture inside the timed loop
// and report it as the polyfill's own allocation. Sharing one slice is safe
// because reading a file does not change it.
func newBenchBareFile(name string, content []byte) *testBareFile {
	return &testBareFile{r: bytes.NewReader(content), name: name}
}

func newBenchSeekerOnlyFile(name string, content []byte) *testSeekerOnlyFile {
	return &testSeekerOnlyFile{r: bytes.NewReader(content), name: name}
}

func newBenchReaderAtFile(name string, content []byte) *testReaderAtFile {
	return &testReaderAtFile{r: bytes.NewReader(content), name: name}
}

func newBenchSeekerFile(name string, content []byte) *testSeekerFile {
	return &testSeekerFile{r: bytes.NewReader(content), name: name}
}

// BenchmarkToBufferedFile measures what it costs to make a stream-only file
// seekable. The allocation reported is the polyfill's own: a memory store copies
// the content, so it grows with the file, and a temporary file store copies it
// out instead. BufferAuto is benched against both thresholds so its selection
// can be compared with the mode it picks.
func BenchmarkToBufferedFile(b *testing.B) {
	modes := []struct {
		name string
		args BufferArgs
	}{
		{"Memory", BufferArgs{Mode: BufferMemory}},
		{"Disk", BufferArgs{Mode: BufferDisk}},
		// The threshold is below every size, so Auto takes the disk branch.
		{"AutoDisk", BufferArgs{Mode: BufferAuto, AutoThreshold: 1}},
		// The threshold is above every size, so Auto takes the memory branch.
		{"AutoMemory", BufferArgs{Mode: BufferAuto, AutoThreshold: 1 << 30}},
	}
	for _, mode := range modes {
		b.Run(mode.name, func(b *testing.B) {
			for _, sz := range bufferedFileBenchSizes {
				b.Run(sz.name, func(b *testing.B) {
					content := ufsTesting.SeedData('R', sz.n)
					b.SetBytes(int64(sz.n))
					b.ReportAllocs()
					for b.Loop() {
						file, err := ToBufferedFile(newBenchBareFile("bench.bin", content), true, mode.args)
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

// BenchmarkToBufferedFileAutoCrossover sweeps sizes across AutoBufferThreshold,
// which the Small/Medium/Large sizes above never cross. The threshold trades
// memory against a temporary file, so the allocation column is the portable part
// of the measurement: a memory store grows with the file while a temporary file
// store stays flat.
func BenchmarkToBufferedFileAutoCrossover(b *testing.B) {
	sizes := []int{
		256 << 10,
		512 << 10,
		1 << 20,
		2 << 20,
		4 << 20,
		8 << 20,
	}
	modes := []struct {
		name string
		args BufferArgs
	}{
		{"Memory", BufferArgs{Mode: BufferMemory}},
		{"Disk", BufferArgs{Mode: BufferDisk}},
		// No threshold, so Auto decides with AutoBufferThreshold.
		{"AutoDefault", BufferArgs{Mode: BufferAuto}},
	}
	for _, sz := range sizes {
		b.Run(fmt.Sprintf("%dKiB", sz>>10), func(b *testing.B) {
			for _, mode := range modes {
				b.Run(mode.name, func(b *testing.B) {
					content := ufsTesting.SeedData('R', sz)
					b.SetBytes(int64(sz))
					b.ReportAllocs()
					for b.Loop() {
						file, err := ToBufferedFile(newBenchBareFile("bench.bin", content), true, mode.args)
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
// that already implements Seek, ReadAt, or both, where no content is copied: the
// missing method is derived from the one the file has. The cost stays flat
// against the file sizes BenchmarkToBufferedFile uses, because nothing is read.
func BenchmarkToBufferedFileDerivedMethods(b *testing.B) {
	shapes := []struct {
		name string
		new  func(name string, content []byte) fs.File
	}{
		{"SeekOnly", func(name string, content []byte) fs.File { return newBenchSeekerOnlyFile(name, content) }},
		{"ReadAtOnly", func(name string, content []byte) fs.File { return newBenchReaderAtFile(name, content) }},
		{"SeekAndReadAt", func(name string, content []byte) fs.File { return newBenchSeekerFile(name, content) }},
	}
	for _, shape := range shapes {
		b.Run(shape.name, func(b *testing.B) {
			for _, sz := range bufferedFileBenchSizes {
				b.Run(sz.name, func(b *testing.B) {
					content := ufsTesting.SeedData('R', sz.n)
					b.SetBytes(int64(sz.n))
					b.ReportAllocs()
					for b.Loop() {
						file, err := ToBufferedFile(shape.new("bench.bin", content), true, BufferArgs{})
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
