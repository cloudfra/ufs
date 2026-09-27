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

// Package fsutil provides helpers for working with file system values of
// arbitrary type, such as the inner file systems held by ufs wrappers.
package fsutil

import (
	"fmt"
	"os"
	"reflect"
)

// String returns a human-readable description of fsys for use in String
// methods and error messages. It prefers fsys.String when fsys implements
// [fmt.Stringer] and is not a typed nil, falls back to the root directory name
// for an [*os.Root], and otherwise reports the dynamic type and value.
func String(fsys any) string {
	if cFsys, ok := fsys.(fmt.Stringer); ok && !isNil(cFsys) {
		return cFsys.String()
	}
	if cFsys, ok := fsys.(*os.Root); ok {
		if cFsys != nil {
			return cFsys.Name()
		}
	}
	return fmt.Sprintf("unknown [%T] %+v", fsys, fsys)
}

// isNil reports whether v holds a typed nil, which would panic if a method
// dereferences its receiver.
func isNil(v any) bool {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return rv.IsNil()
	default:
		return false
	}
}
