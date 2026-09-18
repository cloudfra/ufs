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

//go:build windows

package ufs

import (
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

func (fsys *localFS) getDeviceInfo() map[string]deviceInfo {
	rootPath := fsys.osFS.Name()
	return windowsDeviceMap(rootPath)
}

func windowsDeviceMap(rootPath string) map[string]deviceInfo {
	vol := filepath.VolumeName(rootPath)
	if vol == "" {
		return defaultDeviceMap
	}
	volumeRoot := vol + string(filepath.Separator)
	// NTFS volume mount points (volumes mounted at arbitrary subdirectories) are not
	// detected here; FindFirstVolumeMountPoint / GetVolumeNameForVolumeMountPoint
	// could enumerate them in a future implementation.
	return map[string]deviceInfo{
		".": windowsDriveInfo(volumeRoot),
	}
}

func windowsDriveInfo(volumeRoot string) deviceInfo {
	ptr, err := syscall.UTF16PtrFromString(volumeRoot)
	if err != nil {
		return defaultDeviceInfo
	}
	dt := windows.GetDriveType(ptr)
	name := filepath.VolumeName(volumeRoot)
	switch dt {
	case windows.DRIVE_REMOVABLE:
		return deviceInfo{name: name, deviceType: "removable", threadCount: 1}
	case windows.DRIVE_FIXED:
		return deviceInfo{name: name, deviceType: "fixed", threadCount: 1}
	case windows.DRIVE_REMOTE:
		return deviceInfo{name: name, deviceType: "network", threadCount: 1}
	case windows.DRIVE_CDROM:
		return deviceInfo{name: name, deviceType: "cdrom", threadCount: 1}
	case windows.DRIVE_RAMDISK:
		return deviceInfo{name: name, deviceType: "memory", threadCount: 4}
	}
	return defaultDeviceInfo
}
