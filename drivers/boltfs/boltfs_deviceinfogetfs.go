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

//go:build !wasm

package boltfs

// DeviceInfo is the [types.DeviceInfoGet] half of [types.ReadFS]: every bolt file system rests on the one database file it holds open.

import (
	"github.com/cloudfra/ufs/drivers/types"
)

func (fsys *boltFS) DeviceInfo() types.DeviceMap {
	return types.NewDeviceMap(types.NewDeviceInfo(fsys.absPath, "bolt", 1, false))
}
