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
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// Mkdir creates the directory at name without creating any missing parent
// directories, using perm for the new node. A name that is already a directory
// is a no-op.
//
// fsys's own [types.MkdirFS] implementation is used when it has one, and is
// otherwise emulated with [types.MkdirAllFS] restricted to the leaf. A file
// system that implements neither gets an unimplemented error. The emulation
// refuses a name whose path runs through a regular file with an error wrapping
// [fs.ErrExist], and passes on any other reason the parent cannot be inspected.
func Mkdir(fsys fs.FS, name string, perm fs.FileMode) error {
	if cFS, ok := fsys.(types.MkdirFS); ok {
		return cFS.Mkdir(name, perm)
	}
	if isDir(fsys, name) {
		return nil
	}
	if cFS, ok := fsys.(types.MkdirAllFS); ok {
		return mkdirFromMkdirAllFS(cFS, name, perm)
	}
	return ufserrors.NewUnimplemented(fsys, "mkdir")
}

// mkdirFromMkdirAllFS emulates Mkdir with an [types.MkdirAllFS], which would
// otherwise create every missing level behind the caller's back. Requiring the
// parent to exist already keeps the single-level semantics Mkdir promises.
func mkdirFromMkdirAllFS(fsys types.MkdirAllFS, name string, perm fs.FileMode) error {
	parentDir := path.Dir(name)
	stat, err := fs.Stat(fsys, parentDir)
	if err == nil && stat.IsDir() {
		return fsys.MkdirAll(name, perm)
	}
	// The parent cannot hold the new level, so the refusal names the deepest
	// blocking node, which is not always the parent itself.
	if conflict := CheckDirFileConflict(fsys, "mkdir", name, true); conflict != nil {
		return conflict
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		// An unreadable parent is the file system's verdict, not a missing
		// level: inventing a reason here would hide it from the caller.
		return err
	}
	return ufserrors.NewPathError("mkdir", name, fmt.Errorf("%q must be a directory to create %q", parentDir, name))
}

// isDir reports whether name exists in fsys as a directory. Any stat failure,
// be it a missing name or a file system that cannot be queried, is false.
func isDir(fsys fs.FS, name string) bool {
	stat, err := fs.Stat(fsys, name)
	if err != nil {
		return false
	}
	return stat.IsDir()
}
