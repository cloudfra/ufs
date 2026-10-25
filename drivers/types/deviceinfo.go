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

package types

import (
	"fmt"
	"maps"
	"path"
	"strings"
)

var (
	// DefaultDeviceInfo is returned when the backing device is not known.
	//
	// This signals to ufs that the FS should be treated as unoptimized for features such as parallel directory walking.
	DefaultDeviceInfo = DeviceInfo{
		name:        "default",
		deviceType:  "unknown",
		threadCount: 1,
	}

	// DefaultDeviceMap is the default response when a device mapping is not explicitly configured for FS.
	DefaultDeviceMap = DeviceMap{
		".": DefaultDeviceInfo,
	}

	// DeviceInfo implements String below, so it is a fmt.Stringer. The
	// assertion declares that conformance at the type level without adding an
	// always-nil interface field that every DeviceInfo value carries and that
	// makes info.Stringer.String() panic.
	_ fmt.Stringer = DeviceInfo{}
)

// DeviceInfo contains platform agnostic information about the backing device
// of this file system.
type DeviceInfo struct {
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

// Name returns the name of the device as specified by the OS or the ufs
// implementation.
func (info DeviceInfo) Name() string {
	return info.name
}

// DeviceType returns the type of device that backs the file system.
func (info DeviceInfo) DeviceType() string {
	return info.deviceType
}

// ThreadCount returns the recommended number of threads to access the device.
func (info DeviceInfo) ThreadCount() int {
	return info.threadCount
}

// Remote reports whether the device is located on a remote machine, so that
// frequent IO calls are slow.
func (info DeviceInfo) Remote() bool {
	return info.remote
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

// Combine merges dm with incoming, whose paths are relative to mountPath, and
// returns the result. An incoming path is dropped as redundant when it names
// the same device as the one that already backs that path in dm.
func (dm DeviceMap) Combine(mountPath string, incoming DeviceMap) DeviceMap {
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
		backing := combined.Backing(fullPath)
		if nested.name != backing.name {
			combined[fullPath] = nested
		}
	}

	return combined
}

// Backing returns the DeviceInfo of the device that backs mountPath: dm's own
// entry for mountPath when it has one, otherwise the entry of mountPath's
// nearest ancestor. dm["."] is returned for a path with no closer match, and
// the zero [DeviceInfo] when dm has no root entry.
func (dm DeviceMap) Backing(mountPath string) DeviceInfo {
	longest := ""
	for k := range dm {
		if k == "." {
			continue
		}
		if len(k) > len(longest) && (k == mountPath || strings.HasPrefix(mountPath, k+"/")) {
			longest = k
		}
	}
	if longest == "" {
		return dm["."]
	}
	return dm[longest]
}

// DeviceInfoGet is an optional interface implemented by file systems that report
// which device backs them, named like [URIGet] and [ExternalPathGet].
type DeviceInfoGet interface {
	// DeviceInfo returns a map of device-relative paths to the device that
	// backs them.
	//
	// The root of the FS has the key ".".
	DeviceInfo() DeviceMap
}
