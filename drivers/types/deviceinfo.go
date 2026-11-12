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
	"slices"
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
// that backs it. The root of the FS has the key ".". The map is minimal: it
// names a path only where the backing device changes.
type DeviceMap map[string]DeviceInfo

// NewDeviceMap returns a DeviceMap holding only the root's DeviceInfo.
func NewDeviceMap(root DeviceInfo) DeviceMap {
	return DeviceMap{".": root}
}

// Combine merges dm with incoming, whose paths are relative to mountPath, and
// returns the result. An incoming path is dropped as redundant when it names
// the same device as the one that already backs that path. The result is
// minimal whatever order its inputs arrive in.
func (dm DeviceMap) Combine(mountPath string, incoming DeviceMap) DeviceMap {
	if len(incoming) == 0 {
		if len(dm) == 0 {
			return DeviceMap{}
		}
		return dm
	}

	combined := DeviceMap{}
	maps.Copy(combined, dm)

	// A path is combined after the path above it so that it is compared
	// against its parent's device instead of against the root's.
	for _, k := range deviceMapPaths(incoming) {
		fullPath := path.Join(mountPath, k)
		backing := combined.Backing(fullPath)
		if nested := incoming[k]; nested.name != backing.name {
			combined[fullPath] = nested
		}
	}

	// An entry combined later can make an entry that was already in dm
	// redundant, so the whole map is pruned rather than only what was added.
	for _, k := range deviceMapPaths(combined) {
		if k == "." {
			continue
		}
		if combined.Backing(path.Dir(k)).name == combined[k].name {
			delete(combined, k)
		}
	}

	return combined
}

// deviceMapPaths returns the keys of dm ordered so that a path comes before any
// path below it, which is the order [DeviceMap.Backing] resolves them in. Keys
// of the same depth are unrelated paths, so they are ordered by name to keep
// the result of combining the same maps the same every run.
func deviceMapPaths(dm DeviceMap) []string {
	keys := make([]string, 0, len(dm))
	for key := range dm {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b string) int {
		if depth := strings.Count(a, "/") - strings.Count(b, "/"); depth != 0 {
			return depth
		}
		return strings.Compare(a, b)
	})
	return keys
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
	// backs them. A key is a path that [io/fs.ValidPath] accepts, and the root
	// of the FS has the key ".".
	//
	// The map is minimal: it names a path only where the backing device
	// changes. A root backed by a hard disk with a memory file system mounted
	// at dev/memory needs the two entries "." and "dev/memory", because the
	// hard disk also backs dev. When the memory file system is mounted at dev
	// and a hard disk returns at dev/hdd, the map needs three entries because
	// the hard disk is back in the chain.
	//
	// Every entry names a real device: it has a name, a type, and a thread
	// count of at least one. Entries are compared by device name. Asking
	// repeatedly reports the same devices.
	DeviceInfo() DeviceMap
}

// DeviceInfoGetFS is a file system that reports the devices backing its paths.
// It combines [SimpleFS] with [DeviceInfoGet], so callers holding a mounted
// volume can ask which [DeviceInfo] backs each path. See [DeviceInfoGet] and
// [DeviceInfoGet.DeviceInfo] for the layout of the returned [DeviceMap].
type DeviceInfoGetFS interface {
	SimpleFS
	DeviceInfoGet
}
