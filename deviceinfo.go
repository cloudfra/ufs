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
	defaultDeviceMap = DeviceMap{
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

// NewDeviceInfo returns a DeviceInfo describing a backing device by name, type,
// recommended thread count, and whether it is remote.
func NewDeviceInfo(name string, deviceType string, threadCount int, remote bool) DeviceInfo {
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

// DeviceMap maps a device-relative path to the [DeviceInfo] of the device
// that backs it. The root of the FS has the key ".".
type DeviceMap map[string]DeviceInfo

// NewDeviceMap returns a DeviceMap holding only the root's DeviceInfo.
func NewDeviceMap(root DeviceInfo) DeviceMap {
	return DeviceMap{".": root}
}

// combine merges dm with incoming, whose paths are relative to mountPath, and
// returns the result. An incoming path is dropped as redundant when it names
// the same device as its nearest ancestor already in dm.
func (dm DeviceMap) combine(mountPath string, incoming DeviceMap) DeviceMap {
	if len(incoming) == 0 {
		if len(dm) == 0 {
			return DeviceMap{}
		}
		return dm
	}

	combined := DeviceMap{}
	maps.Copy(combined, dm)

	for k, nested := range incoming {
		fullPath := path.Join(mountPath, k)
		parent := combined.parent(fullPath)
		if nested.name != parent.name {
			combined[fullPath] = nested
		}
	}

	return combined
}

// parent returns the DeviceInfo of mountPath's nearest ancestor in dm.
func (dm DeviceMap) parent(mountPath string) DeviceInfo {
	longest := "."
	for k := range dm {
		if k == "." {
			continue
		}
		if len(k) > len(longest) && strings.HasPrefix(mountPath, k+"/") {
			longest = k
		}
	}
	return dm[longest]
}

// DeviceInfoGetter provides an interface to obtain the device backend information of a FS.
type DeviceInfoGetter interface {
	// GetDeviceInfo returns a map based on the relative path of the device.
	//
	// The root of the FS has the key ".".
	GetDeviceInfo() DeviceMap
}

func getDeviceInfoOrDefault(fsys fs.FS) DeviceMap {
	if diFsys, ok := fsys.(DeviceInfoGetter); ok {
		return diFsys.GetDeviceInfo()
	}
	return defaultDeviceMap
}
