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

// MkdirAll creates the directory at name and any missing parent directories,
// using perm for each newly created node. A name that is already a directory is
// a no-op.
//
// fsys's own [types.MkdirAllFS] implementation is used when it has one, and is
// otherwise emulated by creating each missing level from the top down with
// [types.MkdirFS]. A file system that implements neither gets an unimplemented
// error.
func MkdirAll(fsys fs.FS, name string, perm fs.FileMode) error {
	return mkdirAll(fsys, name, perm)
}

// mkdirAll is the internal version of [MkdirAll] that every standin in this
// package uses for a directory it has to recreate. Only the exported entry point
// probes the optional interface it stands in for, which is what keeps [Mkdir]
// and [MkdirAll] from recursing into each other: each emulates the other's
// interface from the file system's own method rather than by calling the other's
// entry point.
func mkdirAll(fsys fs.FS, name string, perm fs.FileMode) error {
	if cFS, ok := fsys.(types.MkdirAllFS); ok {
		return cFS.MkdirAll(name, perm)
	}
	if isDir(fsys, name) {
		return nil
	}
	if cFS, ok := fsys.(types.MkdirFS); ok {
		return mkdirAllFromMkdirFS(cFS, name, perm)
	}
	return ufserrors.NewUnimplemented(fsys, "mkdirall")
}

// mkdirAllFromMkdirFS emulates MkdirAll with an [types.MkdirFS] by recursing up
// to the nearest existing ancestor and creating the levels above it on the way
// back down. An ancestor that exists but is not a directory is an error, since
// no directory can be created inside a file.
func mkdirAllFromMkdirFS(fsys types.MkdirFS, name string, perm fs.FileMode) error {
	parent := path.Dir(name)
	if !pathutil.IsCwd(parent) {
		stat, err := fs.Stat(fsys, parent)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if err := mkdirAllFromMkdirFS(fsys, parent, perm); err != nil {
				return err
			}
		} else if !stat.IsDir() {
			return ufserrors.NewPathError("mkdir", name, fmt.Errorf("%q is not a directory", parent))
		}
	}
	return fsys.Mkdir(name, perm)
}
