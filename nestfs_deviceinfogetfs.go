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

// DeviceInfo is the [types.DeviceInfoGet] half of [types.ReadFS]: the device
// of the wrapped file system with one device per mount added.

import (
	"github.com/cloudfra/ufs/drivers/types"
)

func (fsys *nestFS) DeviceInfo() types.DeviceMap {
	// The base device goes first: the call takes fsys.mu and releases it,
	// and the mounts below are asked afterwards, so the nest's lifetime lock
	// is never taken from inside a mount map section. That keeps the order
	// of the locks the same as [nestFS.Close] takes them.
	base := fsys.baseDeviceInfo()
	return base.Combine("", fsys.mounts.DeviceInfo())
}

// baseDeviceInfo asks the wrapped file system for its device under the
// lifetime read lock, the barrier that keeps Close from tearing the base down
// mid-call. A closed nest reports no device of its own; the mounts, if any
// survive, still report theirs.
func (fsys *nestFS) baseDeviceInfo() types.DeviceMap {
	if fsys.closed.Load() {
		return types.DeviceMap{}
	}
	fsys.mu.RLock()
	if fsys.closed.Load() {
		fsys.mu.RUnlock()
		return types.DeviceMap{}
	}
	info := fsys.fsys.DeviceInfo()
	fsys.mu.RUnlock()
	return info
}
