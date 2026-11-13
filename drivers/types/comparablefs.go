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

// ComparableFS reports whether two file systems hold one set of data. It answers
// a different question than [Equals], which compares locations: two file systems
// can name one location while holding separate data, and two file systems can
// hold the same data under different locations.
type ComparableFS interface {
	SimpleFS

	// IsSame reports whether other and this file system hold the same data, so
	// an operation between the two stays inside one file system. A file system
	// whose data can overlap other data without containing all of it, such as a
	// cache or a mount point, must report false unless it can prove that its
	// data is exactly other's data.
	//
	// other may present its data through a [WrapperFS], which holds none of it,
	// so comparing the data under that wrapper keeps a wrapper and the file
	// system it wraps one set of data.
	IsSame(other fs.FS) bool
}

// SameFS reports whether a and b hold the same data, as defined by
// [ComparableFS.IsSame]. A file system that does not implement
// [ComparableFS] is only the same as itself, and a value that cannot be
// compared, such as a map, slice or func, is never the same as anything.
func SameFS(a, b fs.FS) bool {
	if sameNil, otherNil := isNil(a), isNil(b); sameNil || otherNil {
		return sameNil && otherNil
	}
	if aComparable, ok := a.(ComparableFS); ok {
		return aComparable.IsSame(b)
	}
	if bComparable, ok := b.(ComparableFS); ok {
		return bComparable.IsSame(a)
	}
	return samePointer(a, b)
}

// isNil reports whether value holds no file system, whether the interface
// itself is unset or it holds a nil pointer.
func isNil(value fs.FS) bool {
	if value == nil {
		return true
	}
	rValue := reflect.ValueOf(value)
	return rValue.Kind() == reflect.Pointer && rValue.IsNil()
}

// samePointer reports whether a and b are one value, which is the only same
// relation available to file systems that cannot compare their data.
func samePointer(a, b fs.FS) bool {
	aValue, bValue := reflect.ValueOf(a), reflect.ValueOf(b)
	if aValue.Kind() != reflect.Pointer || bValue.Kind() != aValue.Kind() {
		return false
	}
	return aValue.Type() == bValue.Type() && aValue.Pointer() == bValue.Pointer()
}
