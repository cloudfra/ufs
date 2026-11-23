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

package polyfills

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
)

// DeviceInfo returns the map of paths to the devices that back them, keeping
// the contract of [types.DeviceInfoGetFS].
//
// fsys's own [types.DeviceInfoGetFS] implementation is used when it has one.
// The fallback is not implemented yet and returns [types.DefaultDeviceMap], the
// map used when no device mapping is configured, so a caller reads the backing
// device as unknown rather than getting nothing.
func DeviceInfo(fsys fs.FS) types.DeviceMap {
	if cFS, ok := fsys.(types.DeviceInfoGetFS); ok {
		return cFS.DeviceInfo()
	}
	// TODO: Populate this with the best effort implementation of this method based on any combinations of methods that are available to reconstruct this method. Also include tests.
	return types.DefaultDeviceMap
}
