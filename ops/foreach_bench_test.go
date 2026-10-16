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

package ops

import (
	"testing"
)

func BenchmarkForEachFilenameSmall(b *testing.B) {
	lfs := buildTree(b, 100, 5, 512)

	b.ResetTimer()
	for b.Loop() {
		count := 0
		if err := ForEachFilename(lfs, ".", func(_ string) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForEachFilenameMedium(b *testing.B) {
	lfs := buildTree(b, 10_000, 128, 128)

	b.ResetTimer()
	for b.Loop() {
		count := 0
		if err := ForEachFilename(lfs, ".", func(_ string) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}
