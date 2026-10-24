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
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const removeOp = "remove"

// Remove removes the file or empty directory at name, keeping the contract of
// [types.RemoveFS].
//
// fsys's own [types.RemoveFS] implementation is used when it has one. Otherwise
// the removal is done with [types.RemoveAllFS], which takes a whole subtree where
// Remove promises to refuse a directory that still has children, so the guard the
// native method does not need is what the emulation adds: the name is queried, and
// a directory is handed to RemoveAll only after a read has shown it to be empty. A
// file system that implements neither gets an unimplemented error.
//
// The emulation keeps the error contract of the native method: a name that is not
// there reports an error wrapping [fs.ErrNotExist] rather than the no-op RemoveAll
// would return, a directory that still has children reports
// [ufserrors.ErrDirNotEmpty] and is left alone, and the root is refused with
// [fs.ErrPermission] since removing it would take the mount with it. A directory
// whose contents cannot be read is refused too, because a standin that cannot tell
// an empty directory from a full one must not delete either, and those failures
// reach the caller as the file system reports them, with its own Op in the
// [fs.PathError] rather than remove. The extra query and read are the price of the
// guard, so a file system with its own Remove is preferred.
func Remove(fsys fs.FS, name string) error {
	if rFS, ok := fsys.(types.RemoveFS); ok {
		return rFS.Remove(name)
	}
	if err := pathutil.Validate(removeOp, name); err != nil {
		return err
	}
	if pathutil.IsCwd(name) {
		return ufserrors.NewPathError(removeOp, name, fs.ErrPermission)
	}
	if rFS, ok := fsys.(types.RemoveAllFS); ok {
		return removeViaRemoveAllFS(fsys, rFS, name)
	}
	return ufserrors.NewUnimplemented(fsys, removeOp)
}

// removeViaRemoveAllFS emulates Remove with a [types.RemoveAllFS] for a file
// system that has no Remove.
func removeViaRemoveAllFS(fsys fs.FS, wFS types.RemoveAllFS, name string) error {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		// RemoveAll answers nil for a name that is not there, which is the one
		// difference between the two methods that a caller can see.
		return err
	}
	if info.IsDir() {
		entries, err := fs.ReadDir(fsys, name)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			// RemoveAll would take the children with it, which is exactly what
			// Remove promises not to do.
			return ufserrors.NewPathError(removeOp, name, ufserrors.ErrDirNotEmpty)
		}
	}
	return wFS.RemoveAll(name)
}
