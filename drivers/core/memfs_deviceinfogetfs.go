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

package core

// DeviceInfo is the [types.DeviceInfoGet] half of [types.ReadFS]: every memory file system rests on one in-memory device.

import (
	"github.com/cloudfra/ufs/drivers/types"
)

func (fsys *memFS) DeviceInfo() types.DeviceMap {
	info := types.NewDeviceInfo(fsys.name, "memory", 2, false)
	return types.NewDeviceMap(info)
}
