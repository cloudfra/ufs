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

//go:build linux

package ufs

import (
	"bufio"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const procMountsPath = "/proc/self/mounts"

func (fsys *localFS) getDeviceInfo() map[string]deviceInfo {
	rootPath := fsys.osFS.Name()
	if realPath, err := filepath.EvalSymlinks(rootPath); err == nil {
		rootPath = realPath
	}
	f, err := os.Open(procMountsPath)
	if err != nil {
		return defaultDeviceMap
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("failed to close mount info", "path", procMountsPath, "error", err)
		}
	}()
	return linuxDeviceMapFromReader(rootPath, f)
}

type linuxMountEntry struct {
	device     string
	mountPoint string
	fsType     string
}

func linuxDeviceMapFromReader(rootPath string, r io.Reader) map[string]deviceInfo {
	entries := parseLinuxMounts(r)
	return buildLinuxDeviceMap(rootPath, entries)
}

func parseLinuxMounts(r io.Reader) []linuxMountEntry {
	var entries []linuxMountEntry
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		entries = append(entries, linuxMountEntry{
			device:     fields[0],
			mountPoint: fields[1],
			fsType:     fields[2],
		})
	}
	if scanner.Err() != nil {
		return nil
	}
	return entries
}

func buildLinuxDeviceMap(rootPath string, entries []linuxMountEntry) map[string]deviceInfo {
	result := map[string]deviceInfo{".": defaultDeviceInfo}

	// Find the mount that best covers rootPath (longest prefix match).
	bestMatchLen := -1
	for _, m := range entries {
		if isPathCoveredBy(rootPath, m.mountPoint) && len(m.mountPoint) > bestMatchLen {
			bestMatchLen = len(m.mountPoint)
			result["."] = linuxMakeDeviceInfo(m)
		}
	}

	// Add mount points that are under rootPath with a different device.
	for _, m := range entries {
		if !isPathUnder(m.mountPoint, rootPath) {
			continue
		}
		rel := relPathUnder(m.mountPoint, rootPath)
		if rel == "" {
			continue
		}
		di := linuxMakeDeviceInfo(m)
		if di.name != getParentDeviceInfo(result, rel).name {
			result[rel] = di
		}
	}

	return result
}

func linuxMakeDeviceInfo(m linuxMountEntry) deviceInfo {
	dt, tc := linuxDeviceTypeAndThreads(m)
	name := m.device
	if name == "none" || name == "" {
		name = m.fsType
	}
	return deviceInfo{name: name, deviceType: dt, threadCount: tc}
}

func linuxDeviceTypeAndThreads(m linuxMountEntry) (string, int) {
	switch m.fsType {
	case "tmpfs", "ramfs", "devtmpfs":
		return "memory", 4
	case "nfs", "nfs4", "cifs", "smbfs", "sshfs", "fuse.sshfs", "fuse.s3fs", "fuse.gcsfuse":
		return "network", 1
	case "iso9660", "udf":
		return "cdrom", 1
	}

	dev := linuxBaseDevice(m.device)
	switch {
	case strings.HasPrefix(dev, "nvme"):
		return "nvme", 2
	case strings.HasPrefix(dev, "sr") || strings.HasPrefix(dev, "scd"):
		return "cdrom", 1
	case strings.HasPrefix(dev, "mmcblk"):
		return "sdcard", 1
	case strings.HasPrefix(dev, "loop"):
		return "loop", 1
	case strings.HasPrefix(dev, "dm-"):
		return "dm", 1
	case strings.HasPrefix(dev, "sd") || strings.HasPrefix(dev, "hd") || strings.HasPrefix(dev, "vd"):
		if linuxRotational(dev) == 0 {
			return "ssd", 2
		}
		return "hdd", 1
	}

	return "unknown", 1
}

// linuxBaseDevice strips partition suffixes from /dev/ device paths.
// Examples: /dev/nvme0n1p1 → nvme0n1, /dev/sda1 → sda, /dev/mmcblk0p1 → mmcblk0.
func linuxBaseDevice(devicePath string) string {
	if !strings.HasPrefix(devicePath, "/dev/") {
		return ""
	}
	dev := strings.TrimPrefix(devicePath, "/dev/")

	// NVMe (nvme0n1p1 → nvme0n1) and eMMC/SD (mmcblk0p1 → mmcblk0) use 'p' as partition separator.
	if strings.HasPrefix(dev, "nvme") || strings.HasPrefix(dev, "mmcblk") {
		if idx := strings.LastIndex(dev, "p"); idx > 0 {
			suffix := dev[idx+1:]
			if len(suffix) > 0 && suffix[0] >= '0' && suffix[0] <= '9' {
				return dev[:idx]
			}
		}
		return dev
	}

	// sda1 → sda, hdb2 → hdb, vda3 → vda: strip trailing partition digits.
	// Only applicable to sd/hd/vd/xvd device families; other devices (sr0, loop0, dm-0)
	// include digits as part of the device name itself.
	if strings.HasPrefix(dev, "sd") || strings.HasPrefix(dev, "hd") ||
		strings.HasPrefix(dev, "vd") || strings.HasPrefix(dev, "xvd") {
		i := len(dev) - 1
		for i >= 0 && dev[i] >= '0' && dev[i] <= '9' {
			i--
		}
		return dev[:i+1]
	}

	return dev
}

// linuxRotational reads the kernel's rotational flag for a block device.
// Returns 0 for SSD/NVMe, 1 for spinning HDD, -1 if the value cannot be determined.
func linuxRotational(dev string) int {
	data, err := os.ReadFile("/sys/block/" + dev + "/queue/rotational")
	if err != nil {
		return -1
	}
	if strings.TrimSpace(string(data)) == "0" {
		return 0
	}
	return 1
}

// isPathCoveredBy returns true if pathPrefix is a directory ancestor of or equal to targetPath.
func isPathCoveredBy(targetPath, pathPrefix string) bool {
	target := filepath.ToSlash(filepath.Clean(targetPath))
	prefix := filepath.ToSlash(filepath.Clean(pathPrefix))
	if target == prefix {
		return true
	}
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(target, prefix)
	}
	return strings.HasPrefix(target, prefix+"/")
}

// isPathUnder returns true if targetPath is strictly under rootPath.
func isPathUnder(targetPath, rootPath string) bool {
	target := filepath.ToSlash(filepath.Clean(targetPath))
	root := filepath.ToSlash(filepath.Clean(rootPath))
	if strings.HasSuffix(root, "/") {
		stripped, _ := strings.CutSuffix(root, "/")
		return strings.HasPrefix(target, root) && target != stripped
	}
	return strings.HasPrefix(target, root+"/")
}

// relPathUnder returns targetPath relative to rootPath.
// Assumes isPathUnder(targetPath, rootPath) is true.
func relPathUnder(targetPath, rootPath string) string {
	target := filepath.ToSlash(filepath.Clean(targetPath))
	root := filepath.ToSlash(filepath.Clean(rootPath))
	if strings.HasSuffix(root, "/") {
		return strings.TrimPrefix(target, root)
	}
	return strings.TrimPrefix(target, root+"/")
}
