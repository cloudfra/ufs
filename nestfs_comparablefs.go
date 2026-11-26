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

// IsSame is the [types.ComparableFS] half of [types.ReadFS]: two nests are one
// file system when they wrap the same one and mount the same file systems at
// the same paths.

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
)

// IsSame reports whether other nests one file system under one set of mounts,
// which is a comparison the URI cannot make because two file systems can report
// the same URI while holding separate data. A read-only view or other wrapper
// around a nest is compared as the nest underneath it.
func (fsys *nestFS) IsSame(other fs.FS) bool {
	otherNest, ok := types.InnermostFS(other).(*nestFS)
	if !ok || otherNest == nil {
		return false
	}
	if fsys == otherNest {
		return true
	}
	if !types.SameFS(fsys.fsys, otherNest.fsys) {
		return false
	}
	return fsys.sameMounts(otherNest)
}

// sameMounts reports whether fsys and other mount the same file systems at the
// same paths, ignoring archive mounts, which ufs opens for itself and are not
// part of how the nest was built.
func (fsys *nestFS) sameMounts(other *nestFS) bool {
	fsys.mounts.mu.RLock()
	defer fsys.mounts.mu.RUnlock()
	other.mounts.mu.RLock()
	defer other.mounts.mu.RUnlock()

	mounts, otherMounts := fsys.mounts.mounted(), other.mounts.mounted()
	if len(mounts) != len(otherMounts) {
		return false
	}
	for mountPath, mount := range mounts {
		otherMount, ok := otherMounts[mountPath]
		if !ok || !types.SameFS(mount, otherMount) {
			return false
		}
	}
	return true
}
