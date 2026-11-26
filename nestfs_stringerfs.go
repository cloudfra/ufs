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
	fsys.mounts.mu.RLock()
	defer fsys.mounts.mu.RUnlock()
	parts := make(map[string]any, len(fsys.mounts.m)+1)
	parts["/"] = types.Nested(fsys.fsys.String())
	for p, mfs := range fsys.mounts.m {
		parts["/"+p] = types.Nested(mountDescription(mfs))
	}
	return types.Description(map[string]any{"nestFS": parts})
}

// mountDescription describes a mounted file system without the nest layer that
// mounting wraps around it. That layer holds no mounts of its own, so it would
// repeat itself at every mount of the description.
func mountDescription(mfs *nestFS) string {
	mfs.mounts.mu.RLock()
	base := mfs.fsys
	mountless := len(mfs.mounts.m) == 0
	mfs.mounts.mu.RUnlock()
	if mountless {
		return base.String()
	}
	return mfs.String()
}
