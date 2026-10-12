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

package core

import (
	"fmt"
	"testing"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

// BenchmarkMemFileWrite grows a file one fixed-size chunk at a time via
// Write, the common append pattern for a freshly Create'd file.
func BenchmarkMemFileWrite(b *testing.B) {
	for _, chunkSize := range []int{64, 4 << 10, 64 << 10} {
		b.Run(fmt.Sprintf("chunk-%dB", chunkSize), func(b *testing.B) {
			fsys := makeMemFS("memory://bench")
			b.Cleanup(ufsTesting.ValidateClose(b, fsys))
			f, err := fsys.Create("write.bin")
			if err != nil {
				b.Fatal(err)
			}
			data := ufsTesting.SeedData('W', chunkSize)
			b.SetBytes(int64(chunkSize))
			b.ResetTimer()
			for b.Loop() {
				if _, err := f.Write(data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkMemFileWriteString mirrors BenchmarkMemFileWrite but through the
// io.StringWriter path, to compare against Write's []byte path.
func BenchmarkMemFileWriteString(b *testing.B) {
	for _, chunkSize := range []int{64, 4 << 10, 64 << 10} {
		b.Run(fmt.Sprintf("chunk-%dB", chunkSize), func(b *testing.B) {
			fsys := makeMemFS("memory://bench")
			b.Cleanup(ufsTesting.ValidateClose(b, fsys))
			f, err := fsys.Create("writestring.bin")
			if err != nil {
				b.Fatal(err)
			}
			data := string(ufsTesting.SeedData('S', chunkSize))
			b.SetBytes(int64(chunkSize))
			b.ResetTimer()
			for b.Loop() {
				if _, err := f.WriteString(data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
