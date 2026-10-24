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
	"io"
	"io/fs"
	"os"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const copyFileOp = "copyfile"

// CopyFile copies the file at srcPath to dstPath within one file system,
// keeping the contract of [types.CopyFileFS].
//
// fsys's own [types.CopyFileFS] implementation is used when it has one, so a
// back end that can copy without moving the bytes (a reflink, a server-side
// copy) keeps that efficiency. Otherwise the content is moved a buffer at a time
// with [io.Copy], through [types.CreateFileFS] or, if that is absent,
// [types.OpenFileFS]. A file system that implements none of them gets an
// unimplemented error.
//
// The emulation keeps the error contract of the native method: a srcPath that
// does not exist reports an error wrapping [fs.ErrNotExist], and a dstPath that
// is taken already reports an error wrapping [fs.ErrExist]. It is not atomic, so
// a failure partway through can leave a partial file at dstPath. Only the
// [types.OpenFileFS] path carries the source's permission bits, since Create
// takes none, and the emulation refuses a root path with [fs.ErrInvalid] where a
// native method would report [fs.ErrPermission].
func CopyFile(fsys fs.FS, srcPath, dstPath string) error {
	if cFS, ok := fsys.(types.CopyFileFS); ok {
		return cFS.CopyFile(srcPath, dstPath)
	}
	for _, name := range [2]string{srcPath, dstPath} {
		if err := pathutil.ValidateFileName(copyFileOp, name); err != nil {
			return err
		}
	}
	// The write capability is probed before anything is read, so a file system
	// with nothing to write into reports unimplemented without touching the tree.
	switch wFS := fsys.(type) {
	case types.CreateFileFS:
		return copyViaCreateFileFS(fsys, wFS, srcPath, dstPath)
	case types.OpenFileFS:
		return copyViaOpenFileFS(fsys, wFS, srcPath, dstPath)
	}
	return ufserrors.NewUnimplemented(fsys, copyFileOp)
}

// copyViaCreateFileFS emulates the copy with a [types.CreateFileFS], the
// interface a write file system is required to have, so it is the arm most file
// systems reach. Create truncates whatever is at dstPath, so a destination that
// is taken already is refused with a stat first, and Create takes no permission
// bits, so the source's mode is not carried over.
func copyViaCreateFileFS(fsys fs.FS, wFS types.CreateFileFS, srcPath, dstPath string) error {
	if _, err := fs.Stat(fsys, dstPath); err == nil {
		return ufserrors.NewPathError(copyFileOp, dstPath, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		// A destination that cannot be queried is not a free pass to overwrite
		// it, so its failure is reported instead.
		return err
	}

	src, _, err := openSource(fsys, srcPath)
	if err != nil {
		return err
	}
	dst, err := wFS.Create(dstPath)
	if err != nil {
		return ufserrors.Join(err, src.Close())
	}
	return copyInto(dst, src)
}

// copyViaOpenFileFS emulates the copy with an [types.OpenFileFS] for a file
// system that has no Create. The source is opened before the destination, so a
// source that is missing or is a directory creates nothing. The exclusive flag
// makes a dstPath that is taken already fail when the destination is created,
// which is the contract [types.CopyFileFS] sets and closes the window that a
// separate stat of dstPath would leave, and the mode of the source can be given
// to the new file.
func copyViaOpenFileFS(fsys fs.FS, wFS types.OpenFileFS, srcPath, dstPath string) error {
	src, info, err := openSource(fsys, srcPath)
	if err != nil {
		return err
	}
	dst, err := wFS.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return ufserrors.Join(err, src.Close())
	}
	return copyInto(dst, src)
}

// openSource opens the source of an emulated copy together with its FileInfo,
// which the copy needs for its own round trips: one open tells the caller that
// the source exists, that it is a file and what its mode is. When it returns an
// error the handle it opened is already closed.
//
// A directory is refused, because a file system without a way to tell a file
// from a directory would otherwise create the destination and fail reading the
// source into it, leaving an empty file where the copy promised the content of
// the source.
func openSource(fsys fs.FS, srcPath string) (fs.File, fs.FileInfo, error) {
	src, err := fsys.Open(srcPath)
	if err != nil {
		return nil, nil, err
	}
	info, err := src.Stat()
	if err != nil {
		return nil, nil, ufserrors.Join(err, src.Close())
	}
	if info.IsDir() {
		notAFile := ufserrors.NewPathError(copyFileOp, srcPath, fmt.Errorf("%q is not a file", srcPath))
		return nil, nil, ufserrors.Join(notAFile, src.Close())
	}
	return src, info, nil
}

// copyInto moves src into a destination handle that is already open, then closes
// both handles now that the copy is over, whether or not it worked. A handle
// left open is a leak in a file system the caller still holds, and back ends
// that buffer writes (object stores, databases) only persist the content on
// Close, so a failed Close is a failed copy: its error is joined with the copy
// error rather than dropped.
func copyInto(dst types.File, src fs.File) error {
	_, copyErr := io.Copy(dst, src)
	return ufserrors.Join(copyErr, dst.Close(), src.Close())
}
