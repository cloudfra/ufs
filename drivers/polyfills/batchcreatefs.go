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
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const batchCreateOp = "batchcreate"

// BatchCreate writes every file in files, replacing any that already exist, and
// does nothing for an empty batch. Every name is checked with
// [types.ValidateBatchFiles] before the first write, so a bad or repeated name
// at the end of the batch cannot leave the files before it written.
//
// fsys's own [types.BatchCreateFS] implementation is used when it has one, so
// the batch keeps its atomicity. Otherwise the files are written one at a time
// with [types.OpenFileFS] or [types.CreateFileFS], which is not atomic: an
// error partway through leaves the files before it written. Only the
// [types.OpenFileFS] path can apply [types.BatchFile.Perm], since Create takes
// no permissions. A file system that implements none of them gets an
// unimplemented error.
//
// Unlike [types.BatchCreateFS], the emulated paths are not atomic and create a
// missing parent directory with [MkdirAll] rather than in the same operation as
// the file, so a file system that can create files but no directories needs its
// parents to exist already.
func BatchCreate(fsys fs.FS, files []types.BatchFile) error {
	if cFS, ok := fsys.(types.BatchCreateFS); ok {
		return cFS.BatchCreate(files)
	}
	if err := types.ValidateBatchFiles(batchCreateOp, files); err != nil {
		return err
	}
	if cFS, ok := fsys.(types.OpenFileFS); ok {
		return batchCreateFromOpenFileFS(cFS, files)
	}
	if cFS, ok := fsys.(types.CreateFileFS); ok {
		return batchCreateFromCreateFileFS(cFS, files)
	}
	return ufserrors.NewUnimplemented(fsys, batchCreateOp)
}

// batchCreateFromCreateFileFS writes the batch one file at a time with Create,
// which takes no permission bits, so BatchFile.Perm is left alone.
func batchCreateFromCreateFileFS(fsys types.CreateFileFS, files []types.BatchFile) error {
	for _, file := range files {
		if err := mkdirParent(fsys, file.Name); err != nil {
			return err
		}
		f, err := fsys.Create(file.Name)
		if err != nil {
			return err
		}
		if err := writeBatchFile(f, file); err != nil {
			return err
		}
	}
	return nil
}

// batchCreateFromOpenFileFS writes the batch one file at a time with OpenFile,
// creating a missing file and truncating an existing one so that the whole
// batch content is written and Perm applies.
func batchCreateFromOpenFileFS(fsys types.OpenFileFS, files []types.BatchFile) error {
	for _, file := range files {
		if err := mkdirParent(fsys, file.Name); err != nil {
			return err
		}
		f, err := fsys.OpenFile(file.Name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Perm)
		if err != nil {
			return err
		}
		if err := writeBatchFile(f, file); err != nil {
			return err
		}
	}
	return nil
}

// mkdirParent creates the directory that holds name when it is missing, which is
// what [types.BatchCreateFS] promises for a name whose parents do not exist yet.
// The check keeps a file system that can write files but cannot make directories
// working for the names it can already place, and costs one stat per file.
func mkdirParent(fsys fs.FS, name string) error {
	dir := path.Dir(name)
	if pathutil.IsCwd(dir) || isDir(fsys, dir) {
		return nil
	}
	return mkdirAll(fsys, dir, fs.ModePerm)
}

// writeBatchFile writes the whole of file.Data to f and closes f whether or not
// the write succeeded, since a handle left open is a leak in a file system the
// caller still holds. The write error is returned, joined with the close error
// if there is one too.
func writeBatchFile(f types.File, file types.BatchFile) (err error) {
	defer func() {
		err = ufserrors.Join(err, f.Close())
	}()
	written, err := f.Write(file.Data)
	if err != nil {
		return err
	}
	if written != len(file.Data) {
		return fmt.Errorf("%q: wrote %d of %d bytes", file.Name, written, len(file.Data))
	}
	return nil
}
