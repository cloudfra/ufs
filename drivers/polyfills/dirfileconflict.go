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

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// NodeKind is the kind of node an operation wants at a name, which decides what
// an existing node there must be for the operation to go ahead.
type NodeKind int

const (
	// WantFile is what [Create] and [OpenFile] ask for: an existing file is
	// replaced and an existing directory is refused.
	WantFile NodeKind = iota
	// WantDir is what [Mkdir] and [MkdirAll] ask for: an existing directory is
	// reused and an existing file is refused.
	WantDir
)

// CheckDirFileConflict reports whether placing a node of kind at name would
// overwrite a node of the other kind or land inside a regular file, which
// nothing can be created under. An implementation runs it before its own create
// when its back end does not refuse the conflict itself.
//
// It usually costs one stat, stopping at the first level that exists as a
// directory since a file system holds a directory only under directories. A name
// with missing levels above a file is the exception, and a level that cannot be
// queried is accepted and left to the file system. The errors carry the sentinels
// memFS reports for the same conflict: [fs.ErrInvalid] where a directory stands,
// [fs.ErrExist] where a regular file stands.
func CheckDirFileConflict(fsys fs.FS, op, name string, kind NodeKind) error {
	info, err := fs.Stat(fsys, name)
	if err == nil && info.IsDir() != (kind == WantDir) {
		return wrongKindError(op, name, kind)
	}
	for parent := path.Dir(name); !pathutil.IsCwd(parent); parent = path.Dir(parent) {
		stat, err := fs.Stat(fsys, parent)
		switch {
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			// Nothing is known about this level, so nothing may be concluded
			// from it: a file system that cannot be queried decides for itself.
			return nil
		case err == nil && !stat.IsDir():
			return ufserrors.NewPathError(op, name, underFileError(parent))
		case err == nil:
			return nil
		}
		if path.Dir(parent) == parent {
			// name is not an [fs.ValidPath], so the walk up has stopped
			// shrinking it and would otherwise never reach the root.
			break
		}
	}
	return nil
}

// wrongKindError reports the node already at name standing in the way of the
// node kind wants.
func wrongKindError(op, name string, kind NodeKind) error {
	if kind == WantDir {
		return ufserrors.NewPathError(op, name, underFileError(name))
	}
	return ufserrors.NewPathError(op, name, fmt.Errorf("%q is a directory: %w", name, fs.ErrInvalid))
}

// underFileError reports that file blocks anything being created below it.
func underFileError(file string) error {
	return fmt.Errorf("%q is not a directory: %w", file, fs.ErrExist)
}
