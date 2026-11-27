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

// GetAbsPath is [AbsPathGetter], given to the wrapped file system when it
// answers it.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *nestFS) GetAbsPath(name string) (string, error) {
	if err := fsys.validPath(absPathOp, name); err != nil {
		return "", err
	}
	if rfs, ok := fsys.fsys.(AbsPathGetter); ok {
		// The closed check and the lookup in the wrapped file system are one
		// critical section with respect to this nest's Close.
		fsys.mu.RLock()
		if fsys.closed.Load() {
			fsys.mu.RUnlock()
			return "", ufserrors.NewPathError(absPathOp, name, fs.ErrClosed)
		}
		abs, err := rfs.GetAbsPath(name)
		fsys.mu.RUnlock()
		return abs, err
	}
	return "", realAbsPathNotSupported(fsys, name)
}
