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
	"testing"

	"github.com/cloudfra/ufs/ops"
)

func BenchmarkWalkSmall(b *testing.B) {
	lfs := buildTree(b, 100, 5, 512)

	b.ResetTimer()
	for b.Loop() {
		count := 0
		if err := ops.Walk(lfs, ".", ops.WalkArgs{}, func(_ string) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWalkMedium(b *testing.B) {
	lfs := buildTree(b, 10_000, 128, 128)

	b.ResetTimer()
	for b.Loop() {
		count := 0
		if err := ops.Walk(lfs, ".", ops.WalkArgs{}, func(_ string) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWalkWithExcludes(b *testing.B) {
	lfs := buildTree(b, 10_000, 128, 128)

	b.ResetTimer()
	for b.Loop() {
		count := 0
		if err := ops.Walk(lfs, ".", ops.WalkArgs{ExcludeDirectory: []string{"d1", "d2"}}, func(_ string) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}
