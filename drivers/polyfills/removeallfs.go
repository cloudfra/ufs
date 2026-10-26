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
	"io/fs"
	"path"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const removeAllOp = "removeall"

// RemoveAll removes name and everything beneath it, keeping the contract of
// [types.RemoveAllFS].
//
// fsys's own [types.RemoveAllFS] implementation is used when it has one, since a
// back end that can drop a whole subtree at once (a prefix delete on an object
// store, an unlink loop it already knows how to do) does it without a round trip
// per node. Otherwise the subtree is taken apart from the bottom up with
// [types.RemoveFS], which removes a single node and refuses a directory that
// still has children. A file system that implements neither gets an
// unimplemented error.
//
// The emulation keeps the error contract of the native method: a name that is not
// there is a no-op, which is why the walk starts by querying it rather than by
// handing the name to Remove, and the root is emptied rather than removed, since
// the root is the mount itself and Remove refuses it with [fs.ErrPermission]. The
// tree has to be listable for the walk to work, so a directory whose contents
// cannot be read is reported instead of deleted, and a name that cannot be
// queried reaches the caller as the file system reports it. The emulation is not
// atomic, so a failure partway through leaves part of the subtree gone, and it
// costs a stat and a read for every directory, which is why a file system with
// its own RemoveAll is preferred.
func RemoveAll(fsys fs.FS, name string) error {
	return removeAll(fsys, name)
}

// removeAll is the internal version of [RemoveAll] that every standin in this
// package uses for a name it has to delete. Only the exported entry point probes
// the optional interface it stands in for, which is what keeps [Remove] and
// [RemoveAll] from recursing into each other: each emulates the other's
// interface from the file system's own method rather than by calling the other's
// entry point.
func removeAll(fsys fs.FS, name string) error {
	if rFS, ok := fsys.(types.RemoveAllFS); ok {
		return rFS.RemoveAll(name)
	}
	if err := pathutil.Validate(removeAllOp, name); err != nil {
		return err
	}
	if rFS, ok := fsys.(types.RemoveFS); ok {
		return removeAllFromRemoveFS(fsys, rFS, name)
	}
	return ufserrors.NewUnimplemented(fsys, removeAllOp)
}

// removeAllFromRemoveFS emulates RemoveAll with a [types.RemoveFS] by walking the
// subtree depth first, so a directory reaches Remove only after its children have
// emptied it.
func removeAllFromRemoveFS(fsys fs.FS, rFS types.RemoveFS, name string) error {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return rFS.Remove(name)
	}
	entries, err := fs.ReadDir(fsys, name)
	if err != nil {
		// A directory that cannot be listed cannot be emptied, and Remove would
		// either refuse it or take its children with it.
		return err
	}
	for _, entry := range entries {
		if err := removeAllFromRemoveFS(fsys, rFS, path.Join(name, entry.Name())); err != nil {
			return err
		}
	}
	if pathutil.IsCwd(name) {
		// The root is the mount rather than a node inside it, so the native method
		// empties it and leaves the directory standing, which is also all Remove
		// would allow.
		return nil
	}
	return rFS.Remove(name)
}
