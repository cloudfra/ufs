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

//go:build darwin

package ufs

import (
	"path/filepath"
	"syscall"
)

// mntNowait is the BSD/Darwin Getfsstat flag to return cached stats without forcing a sync.
const mntNowait = 2

func (fsys *localFS) getDeviceInfo() map[string]deviceInfo {
	rootPath := fsys.osFS.Name()
	if realPath, err := filepath.EvalSymlinks(rootPath); err == nil {
		rootPath = realPath
	}
	entries, err := darwinGetMounts()
	if err != nil {
		return defaultDeviceMap
	}
	return darwinDeviceMapFromEntries(rootPath, entries)
}

type darwinMountEntry struct {
	device     string
	mountPoint string
	fsType     string
}

func darwinGetMounts() ([]darwinMountEntry, error) {
	count, err := syscall.Getfsstat(nil, mntNowait)
	if count <= 0 || err != nil {
		return nil, err
	}
	buf := make([]syscall.Statfs_t, count)
	n, err := syscall.Getfsstat(buf, mntNowait)
	if err != nil {
		return nil, err
	}
	entries := make([]darwinMountEntry, 0, n)
	for _, fs := range buf[:n] {
		entries = append(entries, darwinMountEntry{
			device:     darwinInt8ToString(fs.Mntfromname[:]),
			mountPoint: darwinInt8ToString(fs.Mntonname[:]),
			fsType:     darwinInt8ToString(fs.Fstypename[:]),
		})
	}
	return entries, nil
}

// darwinInt8ToString converts a null-terminated int8 C string to a Go string.
func darwinInt8ToString(s []int8) string {
	b := make([]byte, 0, len(s))
	for _, c := range s {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

func darwinDeviceMapFromEntries(rootPath string, entries []darwinMountEntry) map[string]deviceInfo {
	result := map[string]deviceInfo{".": defaultDeviceInfo}

	bestMatchLen := -1
	for _, m := range entries {
		if isPathCoveredBy(rootPath, m.mountPoint) && len(m.mountPoint) > bestMatchLen {
			bestMatchLen = len(m.mountPoint)
			result["."] = darwinMakeDeviceInfo(m)
		}
	}

	for _, m := range entries {
		if !isPathUnder(m.mountPoint, rootPath) {
			continue
		}
		rel := relPathUnder(m.mountPoint, rootPath)
		if rel == "" {
			continue
		}
		di := darwinMakeDeviceInfo(m)
		if di.name != getParentDeviceInfo(result, rel).name {
			result[rel] = di
		}
	}

	return result
}

func darwinMakeDeviceInfo(m darwinMountEntry) deviceInfo {
	dt, tc := darwinDeviceTypeAndThreads(m)
	name := m.device
	if name == "" {
		name = m.fsType
	}
	return deviceInfo{name: name, deviceType: dt, threadCount: tc}
}

func darwinDeviceTypeAndThreads(m darwinMountEntry) (string, int) {
	switch m.fsType {
	case "apfs":
		// APFS is used exclusively on flash storage on Apple hardware.
		return "ssd", 2
	case "hfs":
		return "hdd", 1
	case "nfs", "smbfs", "afpfs", "cifs", "webdav", "osxfuse", "macfuse":
		return "network", 1
	case "cd9660", "udf":
		return "cdrom", 1
	case "msdos", "exfat":
		return "removable", 1
	case "devfs", "autofs":
		return "virtual", 1
	case "tmpfs":
		return "memory", 4
	}
	return "unknown", 1
}
