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

// RemoveAll is the [types.RemoveAllFS] half of [types.WriteFS].

import (
	"io/fs"
	"strings"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *memFS) RemoveAll(name string) error {
	if fsys.isClosed() {
		return ufserrors.NewPathError("removeall", name, fs.ErrClosed)
	}
	if name == pathutil.CwdPath {
		fsys.mu.Lock()
		var removed []string
		for key := range fsys.nodes {
			if key != pathutil.CwdPath {
				removed = append(removed, key)
				delete(fsys.nodes, key)
			}
		}
		fsys.mu.Unlock()
		for _, p := range removed {
			fsys.notify(types.NotifyRemove, p)
		}
		return nil
	}
	if err := pathutil.Validate("removeall", name); err != nil {
		return err
	}
	fsys.mu.Lock()
	var removed []string
	prefix := name + "/"
	for key := range fsys.nodes {
		if key == name || strings.HasPrefix(key, prefix) {
			removed = append(removed, key)
			delete(fsys.nodes, key)
		}
	}
	fsys.mu.Unlock()
	for _, p := range removed {
		fsys.notify(types.NotifyRemove, p)
	}
	return nil
}
