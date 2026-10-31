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
	"path"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
)

// NodeKind is the kind of node the operation is about to place at the name. The
// two kinds refuse different neighbours: a file may replace a file but not a
// directory, and a directory may reuse a directory but not a file.
type NodeKind int

const (
	// FileNode is a regular file, the node [Create] and [OpenFile] place.
	FileNode NodeKind = iota
	// DirNode is a directory, the node [Mkdir] and [MkdirAll] place.
	DirNode
)

// CheckDirFileConflict reports whether placing a node of kind at name would
// overwrite a node of the other kind or land below a regular file, which nothing
// can be created under. An implementation runs it before its own create when its
// back end does not refuse the conflict itself.
//
// The name and its ancestors are judged as the file system reports them, so a
// symlink counts as what it points at. A file system that cannot be queried is
// accepted and left to decide for itself. The errors carry the sentinels memFS
// reports for the same conflict: [fs.ErrInvalid] where a directory stands and
// [fs.ErrExist] where a regular file stands, naming the node that stands in the
// way so that the caller can report it as the path of its own [fs.PathError].
func CheckDirFileConflict(fsys fs.FS, name string, kind NodeKind) error {
	closest, isDir, err := getClosestParent(fsys, name)
	if err != nil {
		// Nothing is known about the tree, so nothing may be concluded from it:
		// the file system has to refuse the write or accept it itself.
		return nil
	}

	if !isDir {
		// A regular file blocks the name below it even when the levels between
		// it and name are missing, because an operation that creates levels
		// would create them and reach the file. The file itself only blocks a
		// directory, since a file may be replaced by a file.
		if closest != name || kind == DirNode {
			return underFileError(closest)
		}
		return nil
	}

	if closest == name && kind == FileNode {
		// The directory at name cannot hold the file being written into it.
		return fmt.Errorf("%q is a directory: %w", name, fs.ErrInvalid)
	}
	return nil
}

// getClosestParent returns the closest node that exists at or above name, name
// itself included, and reports whether that node is a directory. A reported
// error means no node at or above name could be confirmed, not that the levels
// are missing.
func getClosestParent(fsys fs.FS, name string) (string, bool, error) {
	if closest, ok := fsys.(types.ClosestParentFS); ok {
		return closest.ClosestParent(name)
	}

	node := name
	for {
		info, err := fs.Stat(fsys, node)
		if err == nil {
			return node, info.IsDir(), nil
		}
		// A refused level says nothing about itself, so the walk goes past it
		// rather than giving up: a real file system reports ENOTDIR for a name
		// below a regular file, and on Windows an OS code that matches no
		// sentinel, and the file above is the conflict being looked for.
		parent := path.Dir(node)
		if pathutil.IsCwd(node) || parent == node {
			// Nothing was found at or above name, so a name that is not an
			// [fs.ValidPath] would leave the walk up shrinking it forever.
			return "", false, err
		}
		node = parent
	}
}

// underFileError reports that file blocks anything being created below it.
func underFileError(file string) error {
	return fmt.Errorf("%q is not a directory: %w", file, fs.ErrExist)
}
