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

package keyguardfs

// The [types.MountedArchiveDirFS] half of a guard, which forwards rather than
// deciding: whether a directory is an archive's virtual one is a fact about the
// guarded file system, not about the policy over it.

import (
	"github.com/cloudfra/ufs/drivers/types"
)

// IsMountedArchiveDir forwards to the guarded file system, see
// [types.MountedArchiveDirFS].
func (fsys *keyGuardFS) IsMountedArchiveDir(name string) bool {
	if inner, ok := fsys.inner.(types.MountedArchiveDirFS); ok {
		return inner.IsMountedArchiveDir(name)
	}

	return false
}
