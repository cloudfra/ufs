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

package yamlfs

// DeviceInfo is the [types.DeviceInfoGet] half of [types.ReadFS]: a document
// in memory is one device.

import (
	"github.com/cloudfra/ufs/drivers/types"
)

func (fsys *yamlFS) DeviceInfo() types.DeviceMap {
	return types.NewDeviceMap(types.NewDeviceInfo(fsys.name, "memory", 1, false))
}
