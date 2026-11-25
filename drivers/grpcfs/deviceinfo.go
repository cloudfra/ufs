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

package grpcfs

import (
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
)

// toProtoDeviceMap writes the path-keyed device map a file system reports. The
// answer stays minimal, as types.DeviceMap is: a path with no entry of its own is
// backed by the entry of its nearest ancestor.
func toProtoDeviceMap(devices types.DeviceMap) map[string]*proto.DeviceInfo {
	if len(devices) == 0 {
		return nil
	}
	out := make(map[string]*proto.DeviceInfo, len(devices))
	for name, info := range devices {
		out[name] = &proto.DeviceInfo{
			Name:        info.Name(),
			DeviceType:  info.DeviceType(),
			ThreadCount: int32(info.ThreadCount()), //nolint:gosec // G115: the count is a small number of threads, the proto field is int32
		}
	}
	return out
}
