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

package buffile

import (
	"io"
	"testing"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

// fileBenchSizes mirrors the Small/Medium/Large convention used by the
// benchmarks of the file systems.
var fileBenchSizes = []struct {
	name string
	n    int
}{
	{"Small", 1 << 10},    // 1 KB
	{"Medium", 100 << 10}, // 100 KB
	{"Large", 10 << 20},   // 10 MB
}

func BenchmarkFileStat(b *testing.B) {
	for _, sz := range fileBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			f := newTestFile("stat.bin", string(ufsTesting.SeedData('S', sz.n)))
			b.ResetTimer()
			for b.Loop() {
				if _, err := f.Stat(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkFileReadSequential reads the whole content in fixed-size
// chunks, re-seeking to the start whenever it hits EOF, so every iteration
// does the same amount of copying.
func BenchmarkFileReadSequential(b *testing.B) {
	const chunk = 4 << 10 // 4 KB reads
	for _, sz := range fileBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			f := newTestFile("read.bin", string(ufsTesting.SeedData('R', sz.n)))
			buf := make([]byte, chunk)
			b.SetBytes(chunk)
			b.ResetTimer()
			for b.Loop() {
				if _, err := f.Read(buf); err == io.EOF {
					if _, err := f.Seek(0, io.SeekStart); err != nil {
						b.Fatal(err)
					}
				} else if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkFileReadAt issues fixed-size reads at increasing offsets,
// wrapping back to the start, exercising the random-access path without
// disturbing any shared read cursor.
func BenchmarkFileReadAt(b *testing.B) {
	const chunk = 4 << 10 // 4 KB reads
	for _, sz := range fileBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			f := newTestFile("readat.bin", string(ufsTesting.SeedData('A', sz.n)))
			buf := make([]byte, chunk)
			maxOff := max(int64(sz.n-chunk), 1)
			var off int64
			b.SetBytes(chunk)
			b.ResetTimer()
			for b.Loop() {
				if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
					b.Fatal(err)
				}
				off = (off + chunk) % maxOff
			}
		})
	}
}

// BenchmarkFileReadAtParallel runs concurrent ReadAt calls from multiple
// goroutines against one File, to surface mutex contention: ReadAt never
// mutates the shared offset, so it's a candidate for a read lock even though
// File currently guards it with a plain sync.Mutex.
func BenchmarkFileReadAtParallel(b *testing.B) {
	const chunk = 4 << 10 // 4 KB reads
	sz := 1 << 20         // 1 MB content
	f := newTestFile("readat-parallel.bin", string(ufsTesting.SeedData('P', sz)))
	maxOff := int64(sz - chunk)

	b.SetBytes(chunk)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		buf := make([]byte, chunk)
		var off int64
		for pb.Next() {
			if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
				b.Fatal(err)
			}
			off = (off + chunk) % maxOff
		}
	})
}

// BenchmarkFileSeek cycles through SeekStart, SeekCurrent and SeekEnd with
// offsets chosen to always be valid regardless of the file's current
// position, so the loop never hits the error path.
func BenchmarkFileSeek(b *testing.B) {
	f := newTestFile("seek.bin", string(ufsTesting.SeedData('K', 1<<20))) // 1 MB
	seeks := []struct {
		offset int64
		whence int
	}{
		{100, io.SeekStart},
		{0, io.SeekCurrent},
		{-100, io.SeekEnd},
	}
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		s := seeks[i%len(seeks)]
		if _, err := f.Seek(s.offset, s.whence); err != nil {
			b.Fatal(err)
		}
	}
}
