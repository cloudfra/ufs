// Copyright 2026 Jeremy Edwards
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

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/deviceinfo"
)

// deviceInfoGet provides an interface to obtain the device backend information of a FS.
type deviceInfoGet interface {
	// getDeviceInfo returns a map based on the relative path of the device.
	//
	// The root of the FS has the key ".".
	getDeviceInfo() map[string]deviceinfo.Info
}

func getDeviceInfoOrDefault(fsys fs.FS) map[string]deviceinfo.Info {
	if diFsys, ok := fsys.(deviceInfoGet); ok {
		return diFsys.getDeviceInfo()
	}
	return deviceinfo.DefaultMap
}
