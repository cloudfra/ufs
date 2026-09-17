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

// Package mathutil provides a small math library.
package mathutil

import "math"

// ClampToUint32 converts n to uint32, clamping negative values to 0 and
// values above math.MaxUint32 to math.MaxUint32.
func ClampToUint32(n int) uint32 {
	if n < 0 {
		return 0
	}
	if uint64(n) > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(n)
}

// ClampToUint64 converts n to uint64, clamping negative values to 0.
func ClampToUint64(n int64) uint64 {
	if n < 0 {
		return 0
	}
	return uint64(n)
}

// ClampToInt64 converts a non-negative uint64 to int64, clamping any value
// above math.MaxInt64 to math.MaxInt64 so the conversion cannot overflow.
func ClampToInt64(n uint64) int64 {
	if n > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(n)
}
