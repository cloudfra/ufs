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

// OpenFile opens name with the [os.OpenFile] flags in flag and the permission
// bits in perm.
//
// fsys's own [types.OpenFileFS] implementation is used when it has one, since a
// file system knows which flags it can carry out and what mode a new file gets.
// Otherwise the flags are emulated with Open alone, which is all a read-only
// file system offers, and with [types.CreateFileFS] for the parts that write:
//
//   - A read-only open is Open. With [os.O_CREATE] and a missing name, the file
//     is created empty first, which needs Create.
//   - A write of a name that is missing, or one with [os.O_TRUNC], is Create,
//     which replaces the file. [os.O_APPEND] needs nothing extra, since the file
//     it starts from is empty.
//   - A write that keeps what is already there is refused with
//     [errors.ErrUnsupported]. Create replaces a file rather than editing it, so
//     the emulation would have to keep the old content somewhere and write it
//     back on Close, which is a copy the rest of the file system never sees and
//     never agrees with about where a write belongs.
//
// [os.O_RDONLY], [os.O_WRONLY], [os.O_RDWR], [os.O_CREATE], [os.O_EXCL] and
// [os.O_APPEND] are honored the way package os honors them, and the handle
// reports [fs.ErrPermission] for a method its access mode does not allow. A
// read-only open ignores [os.O_TRUNC], as a read-only [os.OpenFile] does, and
// [os.O_EXCL] only says anything together with [os.O_CREATE]. perm is ignored
// when the file is created, because Create takes no permission bits: a file
// system that gives a new file the mode it is asked for is a [types.OpenFileFS]
// and is used directly. A file system with neither interface gets an
// unimplemented error, and one that only writes whole files at once, as
// [types.BatchCreateFS] does, cannot hand back a handle to write through.
//
// The existence checks and the create are separate steps, so none of this is
// atomic.
func OpenFile(fsys fs.FS, name string, flag int, perm fs.FileMode) (types.File, error) {
	if oFS, ok := fsys.(types.OpenFileFS); ok {
		return oFS.OpenFile(name, flag, perm)
	}
	if err := pathutil.ValidateFileName(openFileOp, name); err != nil {
		return nil, err
	}

	access := flag & (os.O_RDONLY | os.O_WRONLY | os.O_RDWR)
	writable := access != os.O_RDONLY

	info, err := fs.Stat(fsys, name)
	exists := err == nil
	switch {
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		// A name that cannot be queried is left to the file system, which
		// reports its own reason when the file is opened.
		return nil, err
	case exists && flag&(os.O_CREATE|os.O_EXCL) == os.O_CREATE|os.O_EXCL:
		return nil, ufserrors.NewPathError(openFileOp, name, fs.ErrExist)
	case !exists && flag&os.O_CREATE == 0:
		return nil, ufserrors.NewPathError(openFileOp, name, fs.ErrNotExist)
	case exists && writable && info.IsDir():
		// A directory cannot hold the content the caller is about to write, so
		// it is refused here rather than returned as a handle that fails when
		// written to.
		isADir := fmt.Errorf("%q is a directory, %w", name, fs.ErrInvalid)
		return nil, ufserrors.NewPathError(openFileOp, name, isADir)
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
		return openReadOnly(f, name), nil
	}

	if exists && flag&os.O_TRUNC == 0 {
		keeps := fmt.Errorf("%q is already there and the flags do not truncate it: %w", name, errors.ErrUnsupported)
		return nil, ufserrors.NewPathError(openFileOp, name, keeps)
	}
	cFS, ok := fsys.(types.CreateFileFS)
	if !ok {
		return nil, ufserrors.NewUnimplemented(fsys, openFileOp)
	}
	f, err := cFS.Create(name)
	if err != nil {
		return nil, err
	}
	return openAccess(f, name, access == os.O_RDWR, true), nil
}

// createEmpty creates name with no content, for a read-only open of a name that
// is missing and has [os.O_CREATE]. The handle is only wanted for the file it
// leaves behind, so it is closed right away.
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

// openReadOnly returns the file Open gave back as a [types.File] that cannot be
// written to, so that a read-only open cannot reach a write through a file that
// happens to implement [io.Writer].
func openReadOnly(f fs.File, name string) types.File {
	return openAccess(wrapFile(&polyfillFile{f: f}), name, true, false)
}

// openAccess returns file, keeping the ReadDir of a directory, so that it
// reports [fs.ErrPermission] for every method outside the access mode the
// caller opened it with. A handle from a file system without [types.OpenFileFS]
// carries no mode of its own: Open may return a file that can be written to and
// Create may return one that can be read from, neither of which is what the
// flags asked for.
func openAccess(file types.File, name string, readable, writable bool) types.File {
	access := &accessFile{File: file, name: name, readable: readable, writable: writable}
	if _, ok := file.(fs.ReadDirFile); ok {
		return &accessDir{accessFile: access}
	}
	return access
}

// accessFile is a [types.File] limited to the access mode it was opened with.
type accessFile struct {
	types.File
	name     string
	readable bool
	writable bool
}

func (f *accessFile) Read(p []byte) (int, error) {
	if !f.readable {
		return 0, ufserrors.NewPathError("read", f.name, fs.ErrPermission)
	}
	return f.File.Read(p)
}

func (f *accessFile) ReadAt(p []byte, off int64) (int, error) {
	if !f.readable {
		return 0, ufserrors.NewPathError("read", f.name, fs.ErrPermission)
	}
	return f.File.ReadAt(p, off)
}

func (f *accessFile) Write(p []byte) (int, error) {
	if !f.writable {
		return 0, ufserrors.NewPathError("write", f.name, fs.ErrPermission)
	}
	return f.File.Write(p)
}

func (f *accessFile) WriteString(s string) (int, error) {
	if !f.writable {
		return 0, ufserrors.NewPathError("write", f.name, fs.ErrPermission)
	}
	return f.File.WriteString(s)
}

// accessDir is an [accessFile] whose file is a directory that can be listed.
// Listing is not a method the access mode limits, so it stays available even
// when the directory cannot be written to.
type accessDir struct {
	*accessFile
}

func (d *accessDir) ReadDir(n int) ([]fs.DirEntry, error) {
	return d.File.(fs.ReadDirFile).ReadDir(n)
}
