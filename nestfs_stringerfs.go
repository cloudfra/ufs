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

// String is the [fmt.Stringer] half of [types.ReadFS]: it describes the
// wrapped file system and what the nest mounts.

import (
	"github.com/cloudfra/ufs/drivers/types"
)

func (fsys *nestFS) String() string {
	// The base description goes first, outside the mount section below:
	// taking fsys.mu inside that section would order this nest's two locks
	// the opposite way from [nestFS.Close], which takes fsys.mu and then the
	// mount map's.
	base := fsys.baseDescription()
	fsys.mounts.mu.RLock()
	defer fsys.mounts.mu.RUnlock()
	parts := make(map[string]any, len(fsys.mounts.m)+1)
	parts["/"] = types.Nested(base)
	for p, mfs := range fsys.mounts.m {
		parts["/"+p] = types.Nested(mountDescription(mfs))
	}
	return types.Description(map[string]any{"nestFS": parts})
}

// baseDescription renders the wrapped file system under the lifetime read
// lock, so the text cannot be taken while Close is tearing the base down. It
// checks no closed flag: a closed file system still describes itself as it
// did open, and the lock only serializes the call against the teardown.
func (fsys *nestFS) baseDescription() string {
	fsys.mu.RLock()
	desc := fsys.fsys.String()
	fsys.mu.RUnlock()
	return desc
}

// mountDescription describes a mounted file system without the nest layer that
// mounting wraps around it. That layer holds no mounts of its own, so it would
// repeat itself at every mount of the description.
func mountDescription(mfs *nestFS) string {
	mfs.mounts.mu.RLock()
	mountless := len(mfs.mounts.m) == 0
	mfs.mounts.mu.RUnlock()
	if mountless {
		return mfs.baseDescription()
	}
	return mfs.String()
}
