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

package types

import (
	"io/fs"
	"reflect"
)

// Equals reports whether two file systems are the same. A file system whose
// dynamic type cannot be compared, such as a wrapper holding a func, map or
// slice field, is reported as different rather than panicking.
func Equals(a, b fs.FS) bool {
	aType, bType := reflect.TypeOf(a), reflect.TypeOf(b)
	if aType == nil || bType == nil {
		return a == b
	}
	if aType != bType || !aType.Comparable() {
		return false
	}
	return a == b
}
