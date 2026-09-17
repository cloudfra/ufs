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

package mathutil

import (
	"math"
	"testing"
)

func TestClampToUint32(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   int
		want uint32
	}{
		{"zero", 0, 0},
		{"positive", 42, 42},
		{"negative", -1, 0},
		{"large_negative", -1000, 0},
		{"max_int32", math.MaxInt32, math.MaxInt32},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ClampToUint32(tc.in); got != tc.want {
				t.Errorf("ClampToUint32(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestClampToUint64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   int64
		want uint64
	}{
		{"zero", 0, 0},
		{"positive", 42, 42},
		{"negative", -1, 0},
		{"large_negative", -1000, 0},
		{"max_int64", math.MaxInt64, math.MaxInt64},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ClampToUint64(tc.in); got != tc.want {
				t.Errorf("ClampToUint64(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestClampToInt64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   uint64
		want int64
	}{
		{"zero", 0, 0},
		{"positive", 42, 42},
		{"max_int64", uint64(math.MaxInt64), math.MaxInt64},
		{"above_max_int64", uint64(math.MaxInt64) + 1, math.MaxInt64},
		{"max_uint64", math.MaxUint64, math.MaxInt64},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ClampToInt64(tc.in); got != tc.want {
				t.Errorf("ClampToInt64(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
