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

// Chtimes is [types.ChtimesFS], which [types.WriteFS] does not ask for: a caller asks for it with a type assertion.

import (
	"io/fs"
	"time"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// A nest holds no times of its own: the node belongs to the file system it is
// mounted on, so the request goes to [polyfills.Chtimes], which asks that file
// system and answers with what it says. A mount whose file system cannot change
// times, such as the content of an archive, is reported as unable rather than
// answered by rewriting the file, which would move its modification time to now
// and leave its access time where it was.
func (fsys *nestFS) Chtimes(name string, atime time.Time, mtime time.Time) error {
	if err := fsys.validPath("chtimes", name); err != nil {
		return err
	}
	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return err
	}
	// The closed check and the change on the file system holding name are one
	// critical section with respect to that nest's Close.
	mountFS.mu.RLock()
	if mountFS.closed.Load() {
		mountFS.mu.RUnlock()
		return ufserrors.NewPathError("chtimes", name, fs.ErrClosed)
	}
	err = polyfills.Chtimes(mountFS.fsys, subName, atime, mtime)
	mountFS.mu.RUnlock()
	if err != nil {
		return rewritePathError(err, name, subName)
	}
	return nil
}
