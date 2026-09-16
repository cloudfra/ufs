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
	"maps"
	"path"
	"strings"

	"github.com/cloudfra/ufs"
)

var (
	// defaultDeviceInfo is returned when the backing device is not known.
	//
	// This signals to ufs that the FS should be treated as unoptimized for features such as parallel directory walking.
	defaultDeviceInfo = ufs.DeviceInfo{
		Name:        "default",
		DeviceType:  "unknown",
		ThreadCount: 1,
	}

	// defaultDeviceMap is the default response when a device mapping is not explicitly configured for FS.
	defaultDeviceMap = map[string]ufs.DeviceInfo{
		".": defaultDeviceInfo,
	}
)

func newDeviceInfoMap(rootDeviceInfo ufs.DeviceInfo) map[string]ufs.DeviceInfo {
	return map[string]ufs.DeviceInfo{
		".": rootDeviceInfo,
	}
}

func combineDeviceInfo(src map[string]ufs.DeviceInfo, mountPath string, incoming map[string]ufs.DeviceInfo) map[string]ufs.DeviceInfo {
	if len(incoming) == 0 {
		if len(src) == 0 {
			return map[string]ufs.DeviceInfo{}
		}
		if len(incoming) == 0 {
			return src
		}
	}

	combined := map[string]ufs.DeviceInfo{}
	maps.Copy(combined, src)

	for k, nestedDeviceInfo := range incoming {
		fullPath := path.Join(mountPath, k)
		parentDeviceInfo := getParentDeviceInfo(combined, fullPath)
		if nestedDeviceInfo.Name != parentDeviceInfo.Name {
			combined[fullPath] = nestedDeviceInfo
		}
	}

	return combined
}

func getParentDeviceInfo(m map[string]ufs.DeviceInfo, mountPath string) ufs.DeviceInfo {
	longest := "."
	for k := range m {
		if k == "." {
			continue
		}
		if len(k) > len(longest) && strings.HasPrefix(mountPath, k+"/") {
			longest = k
		}
	}
	return m[longest]
}

func getDeviceInfoOrDefault(fsys fs.FS) map[string]ufs.DeviceInfo {
	if diFsys, ok := fsys.(ufs.DeviceInfoGet); ok {
		return diFsys.GetDeviceInfo()
	}
	return defaultDeviceMap
}
