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
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// CheckDirFileConflict reports whether the node being placed at name by op, a
// directory when isDir is true, is blocked by a node the file system already
// reports at or above name. The returned [*fs.PathError] names the blocking
// node and wraps [fs.ErrInvalid] where a directory stands or [fs.ErrExist]
// where a regular file stands.
func CheckDirFileConflict(fsys fs.FS, op, name string, isDir bool) error {
	closest, closestIsDir, err := getClosestParent(fsys, name)
	if err != nil {
		return nil
	}

	if !closestIsDir {
		if closest != name || isDir {
			return underFileError(op, name, closest)
		}
		return nil
	}

	if closest == name && !isDir {
		return ufserrors.NewPathError(op, name, fmt.Errorf("%q is a directory: %w", name, fs.ErrInvalid))
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
		// A refused level says nothing about itself, so the walk goes past it: a
		// real file system reports ENOTDIR for a name below a regular file, and
		// the file above is the conflict being looked for.
		parent := path.Dir(node)
		if pathutil.IsCwd(node) || parent == node {
			// Nothing was found at or above name, so a name that is not an
			// [fs.ValidPath] would leave the walk up shrinking it forever.
			return "", false, err
		}
		node = parent
	}
}

// underFileError reports that file blocks name being created at or below it.
func underFileError(op, name, file string) error {
	return ufserrors.NewPathError(op, name, fmt.Errorf("%q is not a directory: %w", file, fs.ErrExist))
}
