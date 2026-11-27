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

package ufs

// Remove is the [types.RemoveFS] half of [types.WriteFS]: it refuses a path
// the nest itself needs, such as the directory of a mounted archive.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *nestFS) Remove(name string) error {
	if err := fsys.validPath("remove", name); err != nil {
		return err
	}
	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return err
	}
	// The closed check and the remove on the file system holding name are one
	// critical section with respect to that nest's Close.
	mountFS.mu.RLock()
	if mountFS.closed.Load() {
		mountFS.mu.RUnlock()
		return ufserrors.NewPathError("remove", name, fs.ErrClosed)
	}
	err = mountFS.fsys.Remove(subName)
	mountFS.mu.RUnlock()
	if err != nil {
		return rewritePathError(err, name, subName)
	}
	return nil
}
