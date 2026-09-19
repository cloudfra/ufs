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

	"github.com/cloudfra/ufs/internal/device"
)

func (fsys *localFS) getDeviceInfo() device.Map {
	rootPath := fsys.osFS.Name()
	return windowsDeviceMap(rootPath)
}

func windowsDeviceMap(rootPath string) device.Map {
	vol := filepath.VolumeName(rootPath)
	if vol == "" {
		return device.DefaultMap
	}
	volumeRoot := vol + string(filepath.Separator)
	// NTFS volume mount points (volumes mounted at arbitrary subdirectories) are not
	// detected here; FindFirstVolumeMountPoint / GetVolumeNameForVolumeMountPoint
	// could enumerate them in a future implementation.
	return device.NewMap(windowsDriveInfo(volumeRoot))
}

func windowsDriveInfo(volumeRoot string) device.Info {
	ptr, err := syscall.UTF16PtrFromString(volumeRoot)
	if err != nil {
		return device.Default
	}
	dt := windows.GetDriveType(ptr)
	name := filepath.VolumeName(volumeRoot)
	switch dt {
	case windows.DRIVE_REMOVABLE:
		return device.New(name, "removable", 1, false)
	case windows.DRIVE_FIXED:
		return device.New(name, "fixed", 1, false)
	case windows.DRIVE_REMOTE:
		return device.New(name, "network", 1, true)
	case windows.DRIVE_CDROM:
		return device.New(name, "cdrom", 1, false)
	case windows.DRIVE_RAMDISK:
		return device.New(name, "memory", 4, false)
	}
	return device.Default
}
