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
	"fmt"
	"io/fs"
	"maps"
	"path"
	"strings"
)

var (
	// defaultDeviceInfo is returned when the backing device is not known.
	//
	// This signals to ufs that the FS should be treated as unoptimized for features such as parallel directory walking.
	defaultDeviceInfo = deviceInfo{
		name:        "default",
		deviceType:  "unknown",
		threadCount: 1,
	}

	// defaultDeviceMap is the default response when a device mapping is not explicitly configured for FS.
	defaultDeviceMap = map[string]deviceInfo{
		".": defaultDeviceInfo,
	}
)

// deviceInfo contains platform agnostic information about the backing device of this file system.
type deviceInfo struct {
	// name of the device as specified by the OS or the ufs implementation
	name string
	// deviceType is the type of device that backs the FS.
	deviceType string
	// threadCount is the recommended number of threads to access the device.
	threadCount int
}

// String representation of deviceInfo.
func (info deviceInfo) String() string {
	return fmt.Sprintf("{name: %q, deviceType: %q, threadCount: %d}", info.name, info.deviceType, info.threadCount)
}

// deviceInfoGet provides an interface to obtain the device backend information of a FS.
type deviceInfoGet interface {
	// getDeviceInfo returns a map based on the relative path of the device.
	//
	// The root of the FS has the key ".".
	getDeviceInfo() map[string]deviceInfo
}

func newDeviceInfoMap(rootDeviceInfo deviceInfo) map[string]deviceInfo {
	return map[string]deviceInfo{
		".": rootDeviceInfo,
	}
}

func combineDeviceInfo(src map[string]deviceInfo, mountPath string, incoming map[string]deviceInfo) map[string]deviceInfo {
	if len(incoming) == 0 {
		if len(src) == 0 {
			return map[string]deviceInfo{}
		}
		if len(incoming) == 0 {
			return src
		}
	}

	combined := map[string]deviceInfo{}
	maps.Copy(combined, src)

	for k, nestedDeviceInfo := range incoming {
		fullPath := path.Join(mountPath, k)
		parentDeviceInfo := getParentDeviceInfo(combined, fullPath)
		if nestedDeviceInfo.name != parentDeviceInfo.name {
			combined[fullPath] = nestedDeviceInfo
		}
	}

	return combined
}

func getParentDeviceInfo(m map[string]deviceInfo, mountPath string) deviceInfo {
	longest := "."
	for k := range m {
		if len(k) > len(longest) && strings.HasPrefix(mountPath, k) {
			longest = k
		}
	}
	return m[longest]
}

func getDeviceInfoOrDefault(fsys fs.FS) map[string]deviceInfo {
	if diFsys, ok := fsys.(deviceInfoGet); ok {
		return diFsys.getDeviceInfo()
	}
	return defaultDeviceMap
}
