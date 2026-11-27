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

// ReadLink is the [fs.ReadLinkFS] half of [types.ReadFS].

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *nestFS) ReadLink(name string) (string, error) {
	if err := fsys.validPath("readlink", name); err != nil {
		return "", err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return "", err
	}

	if cFsys, ok := mountFS.fsys.(fs.ReadLinkFS); ok {
		// The closed check and the readlink on the file system holding name
		// are one critical section with respect to that nest's Close.
		mountFS.mu.RLock()
		if mountFS.closed.Load() {
			mountFS.mu.RUnlock()
			return "", ufserrors.NewPathError("readlink", name, fs.ErrClosed)
		}
		target, err := cFsys.ReadLink(subName)
		mountFS.mu.RUnlock()
		return target, rewritePathError(err, name, subName)
	}

	return "", ufserrors.NewPathError("readlink", name, fs.ErrInvalid)
}
