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

// The [types.DeviceInfoGetFS] half of a guard.

import (
	"github.com/cloudfra/ufs/drivers/types"
)

// DeviceInfo returns the guarded file system's device map, see
// [types.DeviceInfoGet].
func (fsys *keyGuardFS) DeviceInfo() types.DeviceMap {
	return fsys.inner.DeviceInfo()
}
