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

package ufs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const renameOp = "rename"

// Rename moves the file or directory at oldPath to newPath within fsys, as
// described by [RenameFileFS]. It calls fsys.Rename when fsys implements
// [RenameFileFS]. Otherwise it copies oldPath to newPath and then removes
// oldPath, which is not atomic and does not keep file modes or modification
// times. If the copy fails, the partial copy is removed. Symbolic links and
// other special files cannot be copied and fail with [errors.ErrUnsupported].
func Rename(fsys types.WriteFS, oldPath, newPath string) error {
	if r, ok := fsys.(types.RenameFileFS); ok {
		return r.Rename(oldPath, newPath)
	}
	if err := pathutil.ValidateRename(oldPath, newPath); err != nil {
		return err
	}
	return renameByCopy(fsys, oldPath, fsys, newPath)
}

// renameByCopy moves oldPath in src to newPath in dst by copying it and then
// removing it from src. The paths must have passed pathutil.ValidateRename
// when src and dst are the same file system.
//
// A failed copy is removed. When removing a file from src fails, the copy is
// removed too, so only the original remains. When removing a directory from
// src fails, the copy is kept, as part of the original may already be gone.
func renameByCopy(src types.WriteFS, oldPath string, dst types.WriteFS, newPath string) error {
	info, err := src.Lstat(oldPath)
	if err != nil {
		return ufserrors.NewLinkError(renameOp, oldPath, newPath, err)
	}
	if _, err := dst.Lstat(newPath); err == nil {
		return ufserrors.NewLinkError(renameOp, oldPath, newPath, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ufserrors.NewLinkError(renameOp, oldPath, newPath, err)
	}

	if err := copyTree(src, oldPath, dst, newPath, info); err != nil {
		return ufserrors.Join(ufserrors.NewLinkError(renameOp, oldPath, newPath, err), dst.RemoveAll(newPath))
	}
	if info.IsDir() {
		if err := src.RemoveAll(oldPath); err != nil {
			return ufserrors.NewLinkError(renameOp, oldPath, newPath, err)
		}
		return nil
	}
	if err := src.Remove(oldPath); err != nil {
		return ufserrors.Join(ufserrors.NewLinkError(renameOp, oldPath, newPath, err), dst.Remove(newPath))
	}
	return nil
}

// copyTree copies the file or directory oldPath in src, described by info, to
// newPath in dst.
func copyTree(src types.WriteFS, oldPath string, dst types.WriteFS, newPath string, info fs.FileInfo) error {
	switch {
	case info.Mode().IsRegular():
		return copyFile(src, oldPath, dst, newPath)
	case info.IsDir():
		if err := dst.MkdirAll(newPath, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := src.ReadDir(oldPath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			entryInfo, err := entry.Info()
			if err != nil {
				return err
			}
			if err := copyTree(src, path.Join(oldPath, entry.Name()), dst, path.Join(newPath, entry.Name()), entryInfo); err != nil {
				return err
			}
		}
		return nil
	default:
		return ufserrors.NewPathError(renameOp, oldPath, fmt.Errorf("cannot copy a file of type %s, %w", info.Mode().Type(), errors.ErrUnsupported))
	}
}

// copyFile copies the regular file oldPath in src to newPath in dst.
func copyFile(src types.WriteFS, oldPath string, dst types.WriteFS, newPath string) (err error) {
	r, err := src.Open(oldPath)
	if err != nil {
		return err
	}
	defer func() {
		err = ufserrors.Join(err, r.Close())
	}()
	w, err := dst.Create(newPath)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	return ufserrors.Join(err, w.Close())
}
