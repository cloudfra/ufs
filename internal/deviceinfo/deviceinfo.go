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

// Package deviceinfo describes the backing device of a file system backend
// (its name, type, and recommended thread count) and provides the merge
// logic nestFS uses to combine a base FS's device map with each of its
// mounts' device maps into one map keyed by mount-relative path.
package deviceinfo

import (
	"fmt"
	"maps"
	"path"
	"strings"
)

// Info contains platform agnostic information about the backing device of a
// file system.
type Info struct {
	// Name of the device as specified by the OS or the ufs implementation.
	Name string
	// DeviceType is the type of device that backs the FS.
	DeviceType string
	// ThreadCount is the recommended number of threads to access the device.
	ThreadCount int
}

// String returns a human-readable representation of info.
func (info Info) String() string {
	return fmt.Sprintf("{name: %q, deviceType: %q, threadCount: %d}", info.Name, info.DeviceType, info.ThreadCount)
}

// Default is returned when the backing device is not known.
//
// This signals to ufs that the FS should be treated as unoptimized for
// features such as parallel directory walking.
var Default = Info{Name: "default", DeviceType: "unknown", ThreadCount: 1}

// DefaultMap is the default response when a device mapping is not
// explicitly configured for an FS.
var DefaultMap = map[string]Info{".": Default}

// NewMap returns a single-entry map — the root (".") — reporting root as the
// whole FS's device.
func NewMap(root Info) map[string]Info {
	return map[string]Info{".": root}
}

// Combine merges incoming (a mount's device map, keyed relative to the
// mount's own root) into src (the base FS's device map) at mountPath,
// dropping any incoming entry that reports the same device as its closest
// ancestor already in src (so identical adjoining devices don't produce
// redundant entries).
func Combine(src map[string]Info, mountPath string, incoming map[string]Info) map[string]Info {
	if len(incoming) == 0 {
		if len(src) == 0 {
			return map[string]Info{}
		}
		if len(incoming) == 0 {
			return src
		}
	}

	combined := map[string]Info{}
	maps.Copy(combined, src)

	for k, nestedInfo := range incoming {
		fullPath := path.Join(mountPath, k)
		parentInfo := GetParent(combined, fullPath)
		if nestedInfo.Name != parentInfo.Name {
			combined[fullPath] = nestedInfo
		}
	}

	return combined
}

// GetParent returns the Info of the longest key in m that is an ancestor of
// mountPath, or the root (".") entry if none is closer.
func GetParent(m map[string]Info, mountPath string) Info {
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
