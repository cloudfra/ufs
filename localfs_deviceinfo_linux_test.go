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
	"strings"
	"testing"
)

// mockMounts is a synthetic /proc/self/mounts-style file for testing.
// Uses /dev/hda (old IDE) for HDD because those sysfs paths are absent on modern systems,
// causing linuxRotational to return -1 which defaults to "hdd".
const mockLinuxMounts = `sysfs /sys sysfs rw,nosuid,nodev,noexec,relatime 0 0
proc /proc proc rw,nosuid,nodev,noexec,relatime 0 0
devtmpfs /dev devtmpfs rw,nosuid,size=4096k 0 0
/dev/nvme0n1p1 / ext4 rw,relatime 0 0
/dev/hda1 /mnt/c ext4 rw,relatime 0 0
/dev/sr0 /mnt/cdrom iso9660 ro,relatime 0 0
/dev/nvme0n2p1 /mnt/d ext4 rw,relatime 0 0
tmpfs /tmp tmpfs rw,nosuid,nodev 0 0
`

func TestLinuxDeviceMapFromReader_RootAtSlash(t *testing.T) {
	t.Parallel()
	got := linuxDeviceMapFromReader("/", strings.NewReader(mockLinuxMounts))

	if di, ok := got["."]; !ok {
		t.Error("missing root entry")
	} else if di.deviceType != "nvme" {
		t.Errorf("root deviceType = %q, want %q", di.deviceType, "nvme")
	}

	if di, ok := got["mnt/c"]; !ok {
		t.Error("missing mnt/c entry")
	} else if di.deviceType != "hdd" {
		t.Errorf("mnt/c deviceType = %q, want %q", di.deviceType, "hdd")
	}

	if di, ok := got["mnt/cdrom"]; !ok {
		t.Error("missing mnt/cdrom entry")
	} else if di.deviceType != "cdrom" {
		t.Errorf("mnt/cdrom deviceType = %q, want %q", di.deviceType, "cdrom")
	}

	// /mnt/d is a different NVMe device from /, so it appears despite same type.
	if di, ok := got["mnt/d"]; !ok {
		t.Error("missing mnt/d entry")
	} else if di.deviceType != "nvme" {
		t.Errorf("mnt/d deviceType = %q, want %q", di.deviceType, "nvme")
	}

	// /tmp is tmpfs (memory), but same name check: tmpfs is added with name "tmpfs".
	if di, ok := got["tmp"]; !ok {
		t.Error("missing tmp entry")
	} else if di.deviceType != "memory" {
		t.Errorf("tmp deviceType = %q, want %q", di.deviceType, "memory")
	}
}

func TestLinuxDeviceMapFromReader_SubRoot(t *testing.T) {
	t.Parallel()
	// When localFS is rooted at /mnt/c, only that entry and its children matter.
	got := linuxDeviceMapFromReader("/mnt/c", strings.NewReader(mockLinuxMounts))

	if di, ok := got["."]; !ok {
		t.Error("missing root entry")
	} else if di.deviceType != "hdd" {
		t.Errorf("root deviceType = %q, want %q", di.deviceType, "hdd")
	}

	// No sub-mounts under /mnt/c in mock data.
	if len(got) != 1 {
		t.Errorf("expected 1 entry, got %d: %v", len(got), got)
	}
}

func TestLinuxDeviceMapFromReader_NoSameDeviceRedundancy(t *testing.T) {
	t.Parallel()
	// Two mounts on the same NVMe device: the sub-mount should be excluded
	// since its name matches the parent.
	const mounts = `/dev/nvme0n1p1 / ext4 rw 0 0
/dev/nvme0n1p1 /opt ext4 rw 0 0
`
	got := linuxDeviceMapFromReader("/", strings.NewReader(mounts))
	if _, ok := got["opt"]; ok {
		t.Errorf("opt should not appear (same device as root): %v", got)
	}
}

func TestLinuxBaseDevice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"/dev/nvme0n1p1", "nvme0n1"},
		{"/dev/nvme0n1", "nvme0n1"},
		{"/dev/nvme1n2p3", "nvme1n2"},
		{"/dev/sda1", "sda"},
		{"/dev/sda", "sda"},
		{"/dev/hdb2", "hdb"},
		{"/dev/vda3", "vda"},
		{"/dev/mmcblk0p1", "mmcblk0"},
		{"/dev/mmcblk0", "mmcblk0"},
		{"/dev/sr0", "sr0"},
		{"/dev/loop0", "loop0"},
		{"/dev/dm-0", "dm-0"},
		{"tmpfs", ""},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := linuxBaseDevice(tc.input)
			if got != tc.want {
				t.Errorf("linuxBaseDevice(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestLinuxDeviceTypeAndThreads(t *testing.T) {
	t.Parallel()
	tests := []struct {
		m          linuxMountEntry
		wantType   string
		wantThread int
	}{
		{linuxMountEntry{device: "/dev/nvme0n1p1", fsType: "ext4"}, "nvme", 2},
		{linuxMountEntry{device: "/dev/sr0", fsType: "iso9660"}, "cdrom", 1},
		{linuxMountEntry{device: "/dev/mmcblk0p1", fsType: "vfat"}, "sdcard", 1},
		{linuxMountEntry{device: "/dev/loop0", fsType: "squashfs"}, "loop", 1},
		{linuxMountEntry{device: "tmpfs", fsType: "tmpfs"}, "memory", 4},
		{linuxMountEntry{device: "none", fsType: "nfs"}, "network", 1},
		{linuxMountEntry{device: "none", fsType: "nfs4"}, "network", 1},
		{linuxMountEntry{device: "none", fsType: "cifs"}, "network", 1},
		// hda devices have no /sys/block/hda entry on modern systems → defaults to hdd.
		{linuxMountEntry{device: "/dev/hda1", fsType: "ext4"}, "hdd", 1},
	}
	for _, tc := range tests {
		t.Run(tc.m.device+":"+tc.m.fsType, func(t *testing.T) {
			gotType, gotThread := linuxDeviceTypeAndThreads(tc.m)
			if gotType != tc.wantType || gotThread != tc.wantThread {
				t.Errorf("linuxDeviceTypeAndThreads(%+v) = (%q, %d), want (%q, %d)",
					tc.m, gotType, gotThread, tc.wantType, tc.wantThread)
			}
		})
	}
}
