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

package polyfills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const openFileOp = "open"

// OpenFile opens name with the [os.OpenFile] flags in flag, buffering any copy
// it needs with [BufferAuto]. See [OpenFileBuffered].
func OpenFile(fsys fs.FS, name string, flag int, perm fs.FileMode) (types.File, error) {
	return OpenFileBuffered(fsys, name, flag, perm, BufferAuto)
}

// OpenFileBuffered opens name with the [os.OpenFile] flags in flag.
//
// fsys's own [types.OpenFileFS] implementation is used when it has one.
// Otherwise the flags are emulated with Open and, for anything that writes,
// [types.CreateFileFS]:
//
//   - A read-only open uses Open. A file that cannot seek or read at an offset
//     is copied into a buffer chosen by mode the first time one of those is
//     called, so a sequential read never makes a copy. With [os.O_CREATE] a
//     missing file is first created empty.
//   - A write to a new file, or with [os.O_TRUNC], uses Create directly, which
//     replaces the file.
//   - A write that keeps an existing file's content copies the file into a
//     buffer chosen by mode, and Close writes the whole buffer back with Create
//     if anything was written. Other readers do not see the writes until then.
//
// [os.O_EXCL], [os.O_APPEND] and the access mode are honored; [os.O_SYNC] is
// ignored. The checks for whether name exists and the write back are separate
// operations, so none of this is atomic, and since Create takes no permissions
// perm is ignored. A write to a file system without Create gets an
// unimplemented error.
func OpenFileBuffered(fsys fs.FS, name string, flag int, perm fs.FileMode, mode BufferMode) (types.File, error) {
	if cFS, ok := fsys.(types.OpenFileFS); ok {
		return cFS.OpenFile(name, flag, perm)
	}
	if err := pathutil.Validate(openFileOp, name); err != nil {
		return nil, err
	}
	access := flag & (os.O_RDONLY | os.O_WRONLY | os.O_RDWR)
	writable := access != os.O_RDONLY

	info, err := fs.Stat(fsys, name)
	exists := err == nil
	switch {
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return nil, err
	case exists && flag&(os.O_CREATE|os.O_EXCL) == os.O_CREATE|os.O_EXCL:
		return nil, ufserrors.NewPathError(openFileOp, name, fs.ErrExist)
	case !exists && flag&os.O_CREATE == 0:
		return nil, ufserrors.NewPathError(openFileOp, name, fs.ErrNotExist)
	case exists && writable && info.IsDir():
		return nil, ufserrors.NewPathError(openFileOp, name, fmt.Errorf("%q is a directory, %w", name, fs.ErrInvalid))
	}

	if !writable {
		if !exists {
			if err := createEmpty(fsys, name); err != nil {
				return nil, err
			}
		}
		f, err := fsys.Open(name)
		if err != nil {
			return nil, err
		}
		return toReadOnlyFile(fsys, name, f, mode), nil
	}

	cFS, ok := fsys.(types.CreateFileFS)
	if !ok {
		return nil, ufserrors.NewUnimplemented(fsys, openFileOp)
	}
	if !exists || flag&os.O_TRUNC != 0 {
		f, err := cFS.Create(name)
		if err != nil {
			return nil, err
		}
		readable := access == os.O_RDWR
		appendMode := flag&os.O_APPEND != 0
		if readable && !appendMode {
			return f, nil
		}
		return &accessFile{File: f, name: name, readable: readable, append: appendMode}, nil
	}
	return openForUpdate(cFS, name, access == os.O_RDWR, flag&os.O_APPEND != 0, mode)
}

// openForUpdate opens the existing file at name for writing without losing its
// content: the file is copied into a buffer that Close writes back with Create.
func openForUpdate(fsys types.CreateFileFS, name string, readable, appendMode bool, mode BufferMode) (types.File, error) {
	src, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	f, err := loadBufferedFile(name, src, mode)
	if err != nil {
		return nil, err
	}
	f.readable = readable
	f.writable = true
	f.append = appendMode
	f.create = func() (types.File, error) {
		return fsys.Create(name)
	}
	return f, nil
}

// createEmpty creates name as an empty file, for a read-only open with
// [os.O_CREATE] of a file that does not exist yet.
func createEmpty(fsys fs.FS, name string) error {
	cFS, ok := fsys.(types.CreateFileFS)
	if !ok {
		return ufserrors.NewUnimplemented(fsys, openFileOp)
	}
	f, err := cFS.Create(name)
	if err != nil {
		return err
	}
	return f.Close()
}
