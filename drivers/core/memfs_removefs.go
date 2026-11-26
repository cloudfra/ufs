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

package core

// Remove is the [types.RemoveFS] half of [types.WriteFS].

import (
	"io/fs"
	"strings"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *memFS) Remove(name string) error {
	if fsys.isClosed() {
		return ufserrors.NewPathError("remove", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("remove", name); err != nil {
		return err
	}
	if name == pathutil.CwdPath {
		return ufserrors.NewPathError("remove", name, fs.ErrPermission)
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	node, ok := fsys.nodes[name]
	if !ok {
		return ufserrors.NewPathError("remove", name, fs.ErrNotExist)
	}
	if node.isDir {
		prefix := name + "/"
		for key := range fsys.nodes {
			if strings.HasPrefix(key, prefix) {
				return ufserrors.NewPathError("remove", name, ufserrors.ErrDirNotEmpty)
			}
		}
	}
	delete(fsys.nodes, name)
	fsys.notify(types.NotifyRemove, name)
	return nil
}
