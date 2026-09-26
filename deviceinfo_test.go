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

package ufs

import (
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
			input: defaultDeviceInfo,
			want: DeviceMap{
				".": defaultDeviceInfo,
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
			src:  NewDeviceMap(defaultDeviceInfo),
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  nil,
				},
			},
			want: DeviceMap{
				".": defaultDeviceInfo,
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
			src:  NewDeviceMap(defaultDeviceInfo),
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  NewDeviceMap(fakeNvmeDeviceInfo),
				},
			},
			want: DeviceMap{
				".":    defaultDeviceInfo,
				"nvme": fakeNvmeDeviceInfo,
			},
		},
		{
			name: "import nested",
			src:  NewDeviceMap(defaultDeviceInfo),
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
					incoming:  NewDeviceMap(defaultDeviceInfo),
				},
				{
					mountPath: "nvme/usb",
					incoming:  NewDeviceMap(fakeUsbDeviceInfo),
				},
				{
					mountPath: "default",
					incoming:  NewDeviceMap(defaultDeviceInfo),
				},
				{
					mountPath: "default/default",
					incoming:  NewDeviceMap(defaultDeviceInfo),
				},
				{
					mountPath: "default/default/default",
					incoming:  NewDeviceMap(defaultDeviceInfo),
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
				".":                   defaultDeviceInfo,
				"default/default/usb": fakeUsbDeviceInfo,
				"default/nvme":        fakeNvmeDeviceInfo,
				"nvme":                fakeNvmeDeviceInfo,
				"nvme/default":        defaultDeviceInfo,
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
				got = got.combine(mi.mountPath, mi.incoming)
			}
			if d := cmp.Diff(tc.want, got, cmpopts.EquateComparable(DeviceInfo{})); d != "" {
				t.Errorf("got %v, want %v diff(-want,+got):\n %v", got, tc.want, d)
			}
		})
	}
}
