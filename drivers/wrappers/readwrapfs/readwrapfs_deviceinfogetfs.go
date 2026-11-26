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

package readwrapfs

// DeviceInfo is the [types.DeviceInfoGet] half of [types.ReadFS]: a
// readWrapFS rests on whatever device the wrapped file system reports, or the
// default when it reports none.

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
)

func (fsys *readWrapFS) DeviceInfo() types.DeviceMap {
	return getDeviceInfoOrDefault(fsys.fsys)
}

func getDeviceInfoOrDefault(fsys fs.FS) types.DeviceMap {
	if diFsys, ok := fsys.(types.DeviceInfoGet); ok {
		return diFsys.DeviceInfo()
	}
	return types.DefaultDeviceMap
}
