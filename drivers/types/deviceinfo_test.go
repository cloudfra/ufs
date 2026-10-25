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
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var (
	fakeNvmeDeviceInfo = DeviceInfo{
		name:        "/dev/nvme0n1",
		deviceType:  "nvme",
		threadCount: 2,
	}
	fakeHddDeviceInfo = DeviceInfo{
		name:        "/dev/hdd1",
		deviceType:  "hdd",
		threadCount: 1,
	}
	fakeUsbDeviceInfo = DeviceInfo{
		name:        "/dev/usb1",
		deviceType:  "usb",
		threadCount: 1,
	}
)

func TestNewDeviceInfoMap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input DeviceInfo
		want  DeviceMap
	}{
		{
			input: DefaultDeviceInfo,
			want: DeviceMap{
				".": DefaultDeviceInfo,
			},
		},
		{
			input: fakeNvmeDeviceInfo,
			want: DeviceMap{
				".": fakeNvmeDeviceInfo,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.input.name, func(t *testing.T) {
			got := NewDeviceMap(tc.input)
			if d := cmp.Diff(tc.want, got, cmpopts.EquateComparable(DeviceInfo{})); d != "" {
				t.Errorf("got %v, want %v diff(-want,+got):\n %v", got, tc.want, d)
			}
		})
	}
}

type mountInfo struct {
	mountPath string
	incoming  DeviceMap
}

func TestDeviceInfoAccessors(t *testing.T) {
	t.Parallel()
	info := NewDeviceInfo("/dev/sda1", "hdd", 2, true)
	if got, want := info.Name(), "/dev/sda1"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	if got, want := info.DeviceType(), "hdd"; got != want {
		t.Errorf("DeviceType() = %q, want %q", got, want)
	}
	if got, want := info.ThreadCount(), 2; got != want {
		t.Errorf("ThreadCount() = %d, want %d", got, want)
	}
	if got, want := info.Remote(), true; got != want {
		t.Errorf("Remote() = %t, want %t", got, want)
	}
}

func TestDeviceInfoString(t *testing.T) {
	t.Parallel()
	// DeviceInfo must stay a fmt.Stringer after dropping the embedded
	// interface from the struct; the String method is the implementation.
	var stringer fmt.Stringer = NewDeviceInfo("/dev/sda1", "hdd", 2, false)
	want := `{name: "/dev/sda1", deviceType: "hdd", threadCount: 2, remote: false}`
	if got := stringer.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestDefaultDeviceInfoAccessors(t *testing.T) {
	t.Parallel()
	if got, want := DefaultDeviceInfo.Name(), "default"; got != want {
		t.Errorf("DefaultDeviceInfo.Name() = %q, want %q", got, want)
	}
	if got, want := DefaultDeviceInfo.DeviceType(), "unknown"; got != want {
		t.Errorf("DefaultDeviceInfo.DeviceType() = %q, want %q", got, want)
	}
	if got, want := DefaultDeviceInfo.ThreadCount(), 1; got != want {
		t.Errorf("DefaultDeviceInfo.ThreadCount() = %d, want %d", got, want)
	}
	if got, want := DefaultDeviceInfo.Remote(), false; got != want {
		t.Errorf("DefaultDeviceInfo.Remote() = %t, want %t", got, want)
	}
}

func TestDeviceMapBacking(t *testing.T) {
	t.Parallel()
	dm := DeviceMap{
		".":         fakeHddDeviceInfo,
		"fast":      fakeNvmeDeviceInfo,
		"fast/a":    fakeUsbDeviceInfo,
		"fast/a/b":  fakeNvmeDeviceInfo,
		"faster":    fakeUsbDeviceInfo,
		"a":         fakeUsbDeviceInfo,
		"no-root/a": fakeNvmeDeviceInfo,
	}
	tests := []struct {
		name       string
		dm         DeviceMap
		mountPath  string
		wantDevice DeviceInfo
	}{
		{
			name:       "root",
			dm:         dm,
			mountPath:  ".",
			wantDevice: fakeHddDeviceInfo,
		},
		{
			name:       "path below the root entry",
			dm:         dm,
			mountPath:  "slow/deep/file.txt",
			wantDevice: fakeHddDeviceInfo,
		},
		{
			// A path that is itself a key must resolve to that key, not to
			// its ancestor, or the device of a mount point is wrong.
			name:       "path equal to a key",
			dm:         dm,
			mountPath:  "fast",
			wantDevice: fakeNvmeDeviceInfo,
		},
		{
			name:       "path equal to a nested key",
			dm:         dm,
			mountPath:  "fast/a",
			wantDevice: fakeUsbDeviceInfo,
		},
		{
			name:       "single character key equal to the path",
			dm:         dm,
			mountPath:  "a",
			wantDevice: fakeUsbDeviceInfo,
		},
		{
			name:       "longest matching ancestor",
			dm:         dm,
			mountPath:  "fast/a/b/c/file.txt",
			wantDevice: fakeNvmeDeviceInfo,
		},
		{
			// "faster" shares a string prefix with "fast" but is not below it.
			name:       "shared prefix is not an ancestor",
			dm:         dm,
			mountPath:  "faster/x",
			wantDevice: fakeUsbDeviceInfo,
		},
		{
			name:       "separator boundary keeps the shorter key out",
			dm:         dm,
			mountPath:  "fastx/file.txt",
			wantDevice: fakeHddDeviceInfo,
		},
		{
			// A map without a "." entry has nothing to fall back to, so the
			// caller gets the zero DeviceInfo.
			name:       "no root entry to fall back on",
			dm:         DeviceMap{"no-root/a": fakeNvmeDeviceInfo},
			mountPath:  "elsewhere",
			wantDevice: DeviceInfo{},
		},
		{
			name:       "empty map",
			dm:         DeviceMap{},
			mountPath:  "anything",
			wantDevice: DeviceInfo{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.dm.Backing(tc.mountPath)
			if got != tc.wantDevice {
				t.Errorf("Backing(%q) = %v, want %v", tc.mountPath, got, tc.wantDevice)
			}
		})
	}
}

func TestCombineDeviceInfo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		src      DeviceMap
		incoming []mountInfo
		want     DeviceMap
	}{
		{
			name: "src and incoming nil",
			src:  nil,
			incoming: []mountInfo{
				{
					mountPath: "nil",
					incoming:  nil,
				},
			},
			want: DeviceMap{},
		},
		{
			name: "src populated, incoming nil",
			src:  NewDeviceMap(DefaultDeviceInfo),
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  nil,
				},
			},
			want: DeviceMap{
				".": DefaultDeviceInfo,
			},
		},
		{
			name: "src nil, incoming populated",
			src:  nil,
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  NewDeviceMap(fakeNvmeDeviceInfo),
				},
			},
			want: DeviceMap{
				"nvme": fakeNvmeDeviceInfo,
			},
		},
		{
			name: "import nested",
			src:  NewDeviceMap(DefaultDeviceInfo),
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  NewDeviceMap(fakeNvmeDeviceInfo),
				},
			},
			want: DeviceMap{
				".":    DefaultDeviceInfo,
				"nvme": fakeNvmeDeviceInfo,
			},
		},
		{
			name: "import nested",
			src:  NewDeviceMap(DefaultDeviceInfo),
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  NewDeviceMap(fakeNvmeDeviceInfo),
				},
				{
					mountPath: "nvme/hdd",
					incoming:  NewDeviceMap(fakeHddDeviceInfo),
				},
				{
					mountPath: "nvme/nvme",
					incoming:  NewDeviceMap(fakeNvmeDeviceInfo),
				},
				{
					mountPath: "nvme/default",
					incoming:  NewDeviceMap(DefaultDeviceInfo),
				},
				{
					mountPath: "nvme/usb",
					incoming:  NewDeviceMap(fakeUsbDeviceInfo),
				},
				{
					mountPath: "default",
					incoming:  NewDeviceMap(DefaultDeviceInfo),
				},
				{
					mountPath: "default/default",
					incoming:  NewDeviceMap(DefaultDeviceInfo),
				},
				{
					mountPath: "default/default/default",
					incoming:  NewDeviceMap(DefaultDeviceInfo),
				},
				{
					mountPath: "default/default/usb",
					incoming:  NewDeviceMap(fakeUsbDeviceInfo),
				},
				{
					mountPath: "default/nvme",
					incoming:  NewDeviceMap(fakeNvmeDeviceInfo),
				},
			},
			want: DeviceMap{
				".":                   DefaultDeviceInfo,
				"default/default/usb": fakeUsbDeviceInfo,
				"default/nvme":        fakeNvmeDeviceInfo,
				"nvme":                fakeNvmeDeviceInfo,
				"nvme/default":        DefaultDeviceInfo,
				"nvme/hdd":            fakeHddDeviceInfo,
				"nvme/usb":            fakeUsbDeviceInfo,
			},
		},
		{
			name: "path boundary not confused by shared prefix",
			src:  NewDeviceMap(fakeHddDeviceInfo),
			incoming: []mountInfo{
				{
					mountPath: "fast",
					incoming:  NewDeviceMap(fakeNvmeDeviceInfo),
				},
				{
					mountPath: "fast/slow",
					incoming:  NewDeviceMap(fakeHddDeviceInfo),
				},
				{
					mountPath: "faster",
					incoming:  NewDeviceMap(fakeUsbDeviceInfo),
				},
			},
			want: DeviceMap{
				".":         fakeHddDeviceInfo,
				"fast":      fakeNvmeDeviceInfo,
				"fast/slow": fakeHddDeviceInfo,
				"faster":    fakeUsbDeviceInfo,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.src
			for _, mi := range tc.incoming {
				got = got.Combine(mi.mountPath, mi.incoming)
			}
			if d := cmp.Diff(tc.want, got, cmpopts.EquateComparable(DeviceInfo{})); d != "" {
				t.Errorf("got %v, want %v diff(-want,+got):\n %v", got, tc.want, d)
			}
		})
	}
}
