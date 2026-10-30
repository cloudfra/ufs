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

// FSUnwrapper is implemented by a file system that is only a wrapper around
// another file system, so that [Equals] can compare the file systems being
// wrapped instead of the wrappers. A wrapper that is not equivalent to what it
// holds — because it adds state or behavior that changes the answer — returns
// nil and keeps its own identity.
type FSUnwrapper interface {
	// UnwrapFS returns the wrapped file system, or nil when the wrapper is not
	// equivalent to it. It must not return the wrapper itself, which would
	// make [Equals] loop.
	UnwrapFS() fs.FS
}

// Equals reports whether two file systems are the same. A wrapper is compared
// through what it wraps, see [FSUnwrapper]. A file system whose dynamic type
// cannot be compared, such as one holding a func, map or slice field, is
// reported as different rather than panicking.
func Equals(a, b fs.FS) bool {
	a, b = unwrap(a), unwrap(b)
	aType, bType := reflect.TypeOf(a), reflect.TypeOf(b)
	if aType == nil || bType == nil {
		return a == b
	}
	if aType != bType || !comparableType(aType) {
		return false
	}
	return a == b
}

// unwrap returns the file system fsys stands for, following the chain of
// wrappers until one refuses to give up its identity.
func unwrap(fsys fs.FS) fs.FS {
	for {
		unwrapper, ok := fsys.(FSUnwrapper)
		if !ok {
			return fsys
		}
		inner := unwrapper.UnwrapFS()
		if inner == nil {
			return fsys
		}
		fsys = inner
	}
}

// comparableType reports whether == is safe on two values of type t.
// reflect.Type.Comparable reports a struct holding an interface field as
// comparable even though the interface may hold a slice, a map or a func, whose
// comparison panics, so interfaces are refused and structs and arrays are
// checked member by member.
func comparableType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface, reflect.Func, reflect.Map, reflect.Slice:
		return false
	case reflect.Array:
		return comparableType(t.Elem())
	case reflect.Struct:
		for i := range t.NumField() {
			if !comparableType(t.Field(i).Type) {
				return false
			}
		}
		return true
	default:
		return t.Comparable()
	}
}
