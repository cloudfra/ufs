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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// AccessModeMask selects the access mode (O_RDONLY, O_WRONLY or O_RDWR) of
// the flag of an OpenFile call.
const AccessModeMask = os.O_RDONLY | os.O_WRONLY | os.O_RDWR

// IsWriteFlag reports whether flag, the flag of an OpenFile call, asks for
// more than reading an existing file: write access, or creating, truncating
// or appending to the file.
func IsWriteFlag(flag int) bool {
	return flag&AccessModeMask != os.O_RDONLY || flag&(os.O_CREATE|os.O_TRUNC|os.O_APPEND|os.O_EXCL) != 0
}

// OpenFilePolyfill opens name in fsys with flag using only Open, Stat and the
// Create method of W, the writable form of the file system, which returns an
// F. It is the implementation behind the public polyfill and is generic so
// that this package does not depend on the types of the base package. See
// polyfill.OpenFile for the flags it supports.
func OpenFilePolyfill[F fs.File, W interface{ Create(name string) (F, error) }](fsys fs.StatFS, name string, flag int) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	if !IsWriteFlag(flag) {
		return fsys.Open(name)
	}
	if flag&AccessModeMask == os.O_RDONLY || flag&os.O_TRUNC == 0 || flag&(os.O_APPEND|os.O_EXCL) != 0 {
		return nil, ufserrors.NewPathError("open", name, errors.ErrUnsupported)
	}
	wfs, ok := fsys.(W)
	if !ok {
		return nil, ufserrors.NewPathError("open", name, fs.ErrPermission)
	}
	if flag&os.O_CREATE == 0 {
		if _, err := fsys.Stat(name); err != nil {
			return nil, err
		}
	}
	f, err := wfs.Create(name)
	if err != nil {
		return nil, err
	}
	return f, nil
}

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
