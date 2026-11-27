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

// Glob is [fs.GlobFS], given to the wrapped file system when it answers it and
// walked here when it does not.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/globutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *nestFS) Glob(pattern string) ([]string, error) {
	if cFsys, ok := fsys.fsys.(fs.GlobFS); ok {
		// The closed check and the match against the wrapped file system are
		// one critical section with respect to this nest's Close.
		fsys.mu.RLock()
		if fsys.closed.Load() {
			fsys.mu.RUnlock()
			return nil, ufserrors.NewPathError("glob", pattern, fs.ErrClosed)
		}
		matches, err := cFsys.Glob(pattern)
		fsys.mu.RUnlock()
		return matches, err
	}
	// The walk goes through this nest's ReadDir and Stat, which guard the
	// base at their own use sites; no lock may be held across it.
	return globutil.GlobFS(fsys, pattern)
}
