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
	"path"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const renameOp = "rename"

// Rename moves the file or directory at oldPath to newPath within one file
// system, keeping the contract of [types.RenameFileFS].
//
// fsys's own [types.RenameFileFS] implementation is used when it has one.
// Otherwise the node is copied and the original removed, which is the only way a
// file system that has never heard of renaming can move one of its own names.
// The copy and the deletion are the package's own standins rather than a second
// implementation of them: a regular file is copied by the same dispatch as
// [CopyFile], a directory is recreated the way [MkdirAll] recreates one and its
// children are copied into it one by one, and a name is deleted the way
// [RemoveAll] deletes a subtree, so a file system that can only Remove has a
// renamed directory emptied from the bottom up. A file system that has nothing to
// delete with gets an unimplemented error before anything is copied, and one that
// has nothing to write through gets it when the copy is attempted.
//
// The emulation keeps the error contract of the native method: a missing oldPath
// reports an error wrapping [fs.ErrNotExist], a newPath that is taken one
// wrapping [fs.ErrExist], a newPath below oldPath one wrapping [fs.ErrInvalid]
// and either end being the root one wrapping [fs.ErrPermission], the last three
// as the [*os.LinkError] values the native method promises. A name the file
// system refuses to answer for is reported as the file system reports it rather
// than read as missing or taken.
//
// Rename is not atomic, and where that shows is the divergence to note: a copy
// that fails partway is taken back node by node so that the original stays
// whole, while a failed removal of the original is not, since some of the
// original may already be gone and the copy is what is left of it. A failed copy
// through [types.CopyFileFS] is left as the file system left it, because only the
// file system knows what its own copy managed to write. A name renamed onto
// itself is refused as the taken destination it is. Modes other
// than regular files and directories cannot be copied and report
// [errors.ErrUnsupported]. Permission bits reach the copy only through
// [types.OpenFileFS] and through the directory arms, since Create takes none.
// The parents of newPath are created for a directory source and not for a file
// source, because Create creates none, and a MkdirAll that had to create them
// leaves them standing when the copy is taken back. Because the file arm is
// [CopyFile]'s emulation, a file destination is never truncated over: a name
// taken between the query that guards it and the copy is refused with
// [fs.ErrExist] rather than written over. The query, the listing and the
// buffer-at-a-time copy make this the slow path that a file system with its own
// Rename avoids.
func Rename(fsys fs.FS, oldPath, newPath string) error {
	if rFS, ok := fsys.(types.RenameFileFS); ok {
		return rFS.Rename(oldPath, newPath)
	}
	if err := pathutil.ValidateRename(oldPath, newPath); err != nil {
		return err
	}
	if !canRemove(fsys) {
		// The deletion half of the move is probed before the copy half is started:
		// a file system that cannot delete cannot have its names moved, and a copy
		// it could not delete again would leave the same content in two places.
		return ufserrors.NewUnimplemented(fsys, renameOp)
	}
	info, err := fs.Stat(fsys, oldPath)
	if err != nil {
		return ufserrors.NewLinkError(renameOp, oldPath, newPath, err)
	}
	if _, err := fs.Stat(fsys, newPath); err == nil {
		// Renaming a name onto itself arrives here too, since the destination is
		// taken by the very name being moved.
		return ufserrors.NewLinkError(renameOp, oldPath, newPath, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		// A destination that cannot be queried is not a free pass to write over it.
		return ufserrors.NewLinkError(renameOp, oldPath, newPath, err)
	}

	c := &copier{fsys: fsys}
	if err := c.copyNode(oldPath, newPath, info); err != nil {
		// Nothing has been taken away yet, so the half-written copy is taken back
		// and the caller's original is left whole.
		copyErr := ufserrors.NewLinkError(renameOp, oldPath, newPath, err)
		return ufserrors.Join(copyErr, c.undo())
	}
	if err := removeAll(fsys, oldPath); err != nil {
		if info.IsDir() {
			// Part of the original may already be gone, so the copy is kept: taking
			// it away as well would lose whatever survived.
			return ufserrors.NewLinkError(renameOp, oldPath, newPath, err)
		}
		// A file cannot be half moved, so the copy goes with the failed removal and
		// only the original is left.
		copyErr := ufserrors.NewLinkError(renameOp, oldPath, newPath, err)
		return ufserrors.Join(copyErr, removeAll(fsys, newPath))
	}
	return nil
}

// canRemove reports whether fsys has anything at all to delete a moved name
// with, which is the pair of interfaces [RemoveAll] stands in for.
func canRemove(fsys fs.FS) bool {
	if _, ok := fsys.(types.RemoveAllFS); ok {
		return true
	}
	_, ok := fsys.(types.RemoveFS)
	return ok
}

// copier writes the copy half of an emulated rename, keeping track of the names
// it created so that a failure partway through a tree can be taken back.
type copier struct {
	fsys fs.FS
	// created lists the names the copy made, in the order it made them.
	created []string
}

// copyNode writes a copy at newPath of the node at oldPath, which info
// describes.
func (c *copier) copyNode(oldPath, newPath string, info fs.FileInfo) error {
	switch {
	case info.IsDir():
		return c.copyDir(oldPath, newPath, info)
	case info.Mode().IsRegular():
		// A name the copy did not create is not one it may take back, which is what
		// the created flag says: a native copy that failed may or may not have left
		// something behind, and only the file system knows.
		created, err := copyFile(c.fsys, oldPath, newPath)
		if created {
			c.created = append(c.created, newPath)
		}
		return err
	default:
		// A socket or a device node has no content that could be read back, and the
		// copy-and-remove in the ops package refuses them for the same reason.
		return ufserrors.NewPathError(renameOp, oldPath, fmt.Errorf("cannot copy a file of type %s, %w", info.Mode().Type(), errors.ErrUnsupported))
	}
}

// copyDir recreates the directory at newPath and copies each of oldPath's
// children into it.
func (c *copier) copyDir(oldPath, newPath string, info fs.FileInfo) error {
	if err := mkdirAll(c.fsys, newPath, info.Mode().Perm()); err != nil {
		return err
	}
	c.created = append(c.created, newPath)
	entries, err := fs.ReadDir(c.fsys, oldPath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if err := c.copyNode(path.Join(oldPath, entry.Name()), path.Join(newPath, entry.Name()), entryInfo); err != nil {
			return err
		}
	}
	return nil
}

// undo takes the copies back, the last one made first, which is the order that
// lets a file system whose Remove refuses a directory with children in it delete
// a directory as soon as the walk has emptied it.
func (c *copier) undo() error {
	errs := make([]error, 0, len(c.created))
	for i := len(c.created) - 1; i >= 0; i-- {
		errs = append(errs, removeAll(c.fsys, c.created[i]))
	}
	return ufserrors.Join(errs...)
}
