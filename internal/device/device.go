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

// Package device describes how a file system backend interacts with its
// underlying storage device.
package device

import (
	"fmt"
	"maps"
	"path"
	"strings"
)

// Info contains platform agnostic information about the backing device of a
// file system.
type Info struct {
	name        string
	deviceType  string
	threadCount int
	// remote indicates that the device is not physically local, such as
	// cloud storage or a network file share.
	remote bool
}

// New returns an Info describing a device.
func New(name, deviceType string, threadCount int, remote bool) Info {
	return Info{name: name, deviceType: deviceType, threadCount: threadCount, remote: remote}
}

// Name returns the name of the device as specified by the OS or the ufs implementation.
func (info Info) Name() string {
	return info.name
}

// DeviceType returns the type of device that backs the FS.
func (info Info) DeviceType() string {
	return info.deviceType
}

// ThreadCount returns the recommended number of threads to access the device.
func (info Info) ThreadCount() int {
	return info.threadCount
}

// Remote reports whether the device is not physically local, such as cloud
// storage or a network file share.
func (info Info) Remote() bool {
	return info.remote
}

// String returns a human-readable representation of info.
func (info Info) String() string {
	return fmt.Sprintf("{name: %q, deviceType: %q, threadCount: %d, remote: %t}", info.name, info.deviceType, info.threadCount, info.remote)
}

// Default is returned when the backing device is not known.
//
// This signals to ufs that the FS should be treated as unoptimized for
// features such as parallel directory walking.
var Default = New("default", "unknown", 1, false)

// DefaultMap is the default response when a device mapping is not
// explicitly configured for an FS.
var DefaultMap = NewMap(Default)

// Map holds device Info keyed by mount-relative path, with "." as the root.
type Map struct {
	byPath map[string]Info
}

// NewMap returns a Map with a single root (".") entry reporting root as the
// whole FS's device.
func NewMap(root Info) Map {
	return Map{byPath: map[string]Info{".": root}}
}

// Len returns the number of entries in m.
func (m Map) Len() int {
	return len(m.byPath)
}

// Get returns the Info stored at path and whether it was present.
func (m Map) Get(path string) (Info, bool) {
	info, ok := m.byPath[path]
	return info, ok
}

// WithEntry returns m with info stored at path.
func (m Map) WithEntry(path string, info Info) Map {
	if m.byPath == nil {
		m.byPath = map[string]Info{}
	}
	m.byPath[path] = info
	return m
}

// Combine merges incoming (a mount's device map, keyed relative to the
// mount's own root) into m (the base FS's device map) at mountPath,
// dropping any incoming entry that reports the same device as its closest
// ancestor already in m (so identical adjoining devices don't produce
// redundant entries).
func (m Map) Combine(mountPath string, incoming Map) Map {
	if incoming.Len() == 0 {
		if m.Len() == 0 {
			return Map{byPath: map[string]Info{}}
		}
		return m
	}

	combined := map[string]Info{}
	maps.Copy(combined, m.byPath)
	combinedMap := Map{byPath: combined}

	for k, nestedInfo := range incoming.byPath {
		fullPath := path.Join(mountPath, k)
		parentInfo := combinedMap.GetParent(fullPath)
		if nestedInfo.name != parentInfo.name {
			combined[fullPath] = nestedInfo
		}
	}

	return combinedMap
}

// GetParent returns the Info of the longest key in m that is an ancestor of
// mountPath, or the root (".") entry if none is closer.
func (m Map) GetParent(mountPath string) Info {
	longest := "."
	for k := range m.byPath {
		if k == "." {
			continue
		}
		if len(k) > len(longest) && strings.HasPrefix(mountPath, k+"/") {
			longest = k
		}
	}
	return m.byPath[longest]
}
