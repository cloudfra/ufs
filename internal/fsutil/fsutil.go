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

// Package fsutil provides helpers for working with file system values of
// arbitrary type, such as the inner file systems held by ufs wrappers.
package fsutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/polyfill"
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

// OpenFilePolyfill opens name in fsys with flag using only Open, Stat and,
// for a [types.WriteFS], Create. It is the implementation behind
// ufs.OpenFile and polyfill.OpenFile; see polyfill.OpenFile for the flags it
// supports. A file opened for reading that lacks Seek or ReadAt is buffered
// in memory, and its Write and WriteString fail, as for a read-only
// [polyfill.WrapFile].
func OpenFilePolyfill(fsys types.ReadFS, name string, flag int) (types.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	if !IsWriteFlag(flag) {
		f, err := fsys.Open(name)
		if err != nil {
			return nil, err
		}
		return WrapOpened(f, name, polyfill.BufferMemory)
	}
	if flag&AccessModeMask == os.O_RDONLY || flag&os.O_TRUNC == 0 || flag&(os.O_APPEND|os.O_EXCL) != 0 {
		return nil, ufserrors.NewPathError("open", name, errors.ErrUnsupported)
	}
	wfs, ok := fsys.(types.WriteFS)
	if !ok {
		return nil, ufserrors.NewPathError("open", name, fs.ErrPermission)
	}
	if flag&os.O_CREATE == 0 {
		if _, err := fsys.Stat(name); err != nil {
			return nil, err
		}
	}
	return wfs.Create(name)
}

// errIsDirectory is the error of a file-content method of a directory.
var errIsDirectory = errors.New("is a directory")

// WrapOpened returns f, the file at name opened for reading, as a
// [types.File]. A file that lacks Seek or ReadAt is buffered as mode says,
// and its Write and WriteString fail, as for a read-only [polyfill.WrapFile].
// A directory keeps its ReadDir, and like an [*os.File] of a directory its
// Read, ReadAt, Write and WriteString fail. On failure f is closed.
func WrapOpened(f fs.File, name string, mode polyfill.BufferMode) (types.File, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	if info.IsDir() {
		return &dirFile{File: f, name: name}, nil
	}
	return polyfill.WrapFile(f, true, mode)
}

// dirFile is the [types.File] of a directory opened with OpenFile.
type dirFile struct {
	fs.File
	name string
}

func (d *dirFile) Read([]byte) (int, error) {
	return 0, ufserrors.NewPathError("read", d.name, errIsDirectory)
}

func (d *dirFile) ReadAt([]byte, int64) (int, error) {
	return 0, ufserrors.NewPathError("read", d.name, errIsDirectory)
}

func (d *dirFile) Write([]byte) (int, error) {
	return 0, ufserrors.NewPathError("write", d.name, errIsDirectory)
}

func (d *dirFile) WriteString(string) (int, error) {
	return 0, ufserrors.NewPathError("write", d.name, errIsDirectory)
}

// Seek succeeds without moving, as it does for an [*os.File] of a directory
// on most systems; only ReadDir reads a directory.
func (d *dirFile) Seek(int64, int) (int64, error) {
	return 0, nil
}

// ReadDir forwards to the directory's own ReadDir.
func (d *dirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if rd, ok := d.File.(fs.ReadDirFile); ok {
		return rd.ReadDir(n)
	}
	return nil, ufserrors.NewPathError("readdir", d.name, errors.ErrUnsupported)
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
