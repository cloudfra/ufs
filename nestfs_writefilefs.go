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

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var _ types.WriteFileFS = (*nestFS)(nil)

// WriteFile writes data to name whole on the file system holding name,
// through that file system's own WriteFile when it has one, so a whole-file
// write that is atomic there stays atomic through the nest. Without this, a
// whole-file write through the nest would be an open with os.O_TRUNC and a
// write, which shows readers an empty file in between.
func (fsys *nestFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	if err := fsys.validPath("writefile", name); err != nil {
		return err
	}
	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return err
	}
	// The closed check and the write on the file system holding name are one
	// critical section with respect to that nest's Close.
	mountFS.mu.RLock()
	if mountFS.closed.Load() {
		mountFS.mu.RUnlock()
		return ufserrors.NewPathError("writefile", name, fs.ErrClosed)
	}
	err = polyfills.WriteFile(mountFS.fsys, subName, data, perm)
	mountFS.mu.RUnlock()
	if err != nil {
		return rewritePathError(err, name, subName)
	}
	return nil
}
