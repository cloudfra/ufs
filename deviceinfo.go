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
	defaultDeviceInfo = DeviceInfo{
		name:        "default",
		deviceType:  "unknown",
		threadCount: 1,
	}

	// defaultDeviceMap is the default response when a device mapping is not explicitly configured for FS.
	defaultDeviceMap = map[string]DeviceInfo{
		".": defaultDeviceInfo,
	}
)

// DeviceInfo contains platform agnostic information about the backing device
// of this file system. It embeds [fmt.Stringer] to declare that conformance
// at the type level; the implementation is the String method below.
type DeviceInfo struct {
	fmt.Stringer

	// name of the device as specified by the OS or the ufs implementation
	name string
	// deviceType is the type of device that backs the FS.
	deviceType string
	// threadCount is the recommended number of threads to access the device.
	threadCount int
	// remote indicates that the device is located on a remote machine and frequent IO calls may be slow.
	remote bool
}

func newDeviceInfo(name string, deviceType string, threadCount int, remote bool) DeviceInfo {
	return DeviceInfo{
		name:        name,
		deviceType:  deviceType,
		threadCount: threadCount,
		remote:      remote,
	}
}

// String representation of DeviceInfo.
func (info DeviceInfo) String() string {
	return fmt.Sprintf("{name: %q, deviceType: %q, threadCount: %d, remote: %t}", info.name, info.deviceType, info.threadCount, info.remote)
}

// DeviceInfoGet provides an interface to obtain the device backend information of a FS.
type DeviceInfoGet interface {
	// getDeviceInfo returns a map based on the relative path of the device.
	//
	// The root of the FS has the key ".".
	getDeviceInfo() map[string]DeviceInfo
}

// TODO: Create a deviceMap that encapsulates the map[string]DeviceInfo

func newDeviceInfoMap(rootDeviceInfo DeviceInfo) map[string]DeviceInfo {
	return map[string]DeviceInfo{
		".": rootDeviceInfo,
	}
}

func combineDeviceInfo(src map[string]DeviceInfo, mountPath string, incoming map[string]DeviceInfo) map[string]DeviceInfo {
	if len(incoming) == 0 {
		if len(src) == 0 {
			return map[string]DeviceInfo{}
		}
		if len(incoming) == 0 {
			return src
		}
	}

	combined := map[string]DeviceInfo{}
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

func getParentDeviceInfo(m map[string]DeviceInfo, mountPath string) DeviceInfo {
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

func getDeviceInfoOrDefault(fsys fs.FS) map[string]DeviceInfo {
	if diFsys, ok := fsys.(DeviceInfoGet); ok {
		return diFsys.getDeviceInfo()
	}
	return defaultDeviceMap
}
