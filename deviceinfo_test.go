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
	fakeNvmeDeviceInfo = deviceInfo{
		name:        "/dev/nvme0n1",
		deviceType:  "nvme",
		threadCount: 2,
	}
	fakeHddDeviceInfo = deviceInfo{
		name:        "/dev/hdd1",
		deviceType:  "hdd",
		threadCount: 1,
	}
	fakeUsbDeviceInfo = deviceInfo{
		name:        "/dev/usb1",
		deviceType:  "usb",
		threadCount: 1,
	}
)

func TestNewDeviceInfoMap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input deviceInfo
		want  map[string]deviceInfo
	}{
		{
			input: defaultDeviceInfo,
			want: map[string]deviceInfo{
				".": defaultDeviceInfo,
			},
		},
		{
			input: fakeNvmeDeviceInfo,
			want: map[string]deviceInfo{
				".": fakeNvmeDeviceInfo,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.input.name, func(t *testing.T) {
			got := newDeviceInfoMap(tc.input)
			if d := cmp.Diff(tc.want, got, cmpopts.EquateComparable(deviceInfo{})); d != "" {
				t.Errorf("got %v, want %v diff(-want,+got):\n %v", got, tc.want, d)
			}
		})
	}
}

type mountInfo struct {
	mountPath string
	incoming  map[string]deviceInfo
}

func TestCombineDeviceInfo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		src      map[string]deviceInfo
		incoming []mountInfo
		want     map[string]deviceInfo
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
			want: map[string]deviceInfo{},
		},
		{
			name: "src populated, incoming nil",
			src:  newDeviceInfoMap(defaultDeviceInfo),
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  nil,
				},
			},
			want: map[string]deviceInfo{
				".": defaultDeviceInfo,
			},
		},
		{
			name: "src nil, incoming populated",
			src:  nil,
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  newDeviceInfoMap(fakeNvmeDeviceInfo),
				},
			},
			want: map[string]deviceInfo{
				"nvme": fakeNvmeDeviceInfo,
			},
		},
		{
			name: "import nested",
			src:  newDeviceInfoMap(defaultDeviceInfo),
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  newDeviceInfoMap(fakeNvmeDeviceInfo),
				},
			},
			want: map[string]deviceInfo{
				".":    defaultDeviceInfo,
				"nvme": fakeNvmeDeviceInfo,
			},
		},
		{
			name: "import nested",
			src:  newDeviceInfoMap(defaultDeviceInfo),
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  newDeviceInfoMap(fakeNvmeDeviceInfo),
				},
				{
					mountPath: "nvme/hdd",
					incoming:  newDeviceInfoMap(fakeHddDeviceInfo),
				},
				{
					mountPath: "nvme/nvme",
					incoming:  newDeviceInfoMap(fakeNvmeDeviceInfo),
				},
				{
					mountPath: "nvme/default",
					incoming:  newDeviceInfoMap(defaultDeviceInfo),
				},
				{
					mountPath: "nvme/usb",
					incoming:  newDeviceInfoMap(fakeUsbDeviceInfo),
				},
				{
					mountPath: "default",
					incoming:  newDeviceInfoMap(defaultDeviceInfo),
				},
				{
					mountPath: "default/default",
					incoming:  newDeviceInfoMap(defaultDeviceInfo),
				},
				{
					mountPath: "default/default/default",
					incoming:  newDeviceInfoMap(defaultDeviceInfo),
				},
				{
					mountPath: "default/default/usb",
					incoming:  newDeviceInfoMap(fakeUsbDeviceInfo),
				},
				{
					mountPath: "default/nvme",
					incoming:  newDeviceInfoMap(fakeNvmeDeviceInfo),
				},
			},
			want: map[string]deviceInfo{
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
			src:  newDeviceInfoMap(fakeHddDeviceInfo),
			incoming: []mountInfo{
				{
					mountPath: "fast",
					incoming:  newDeviceInfoMap(fakeNvmeDeviceInfo),
				},
				{
					mountPath: "fast/slow",
					incoming:  newDeviceInfoMap(fakeHddDeviceInfo),
				},
				{
					mountPath: "faster",
					incoming:  newDeviceInfoMap(fakeUsbDeviceInfo),
				},
			},
			want: map[string]deviceInfo{
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
				got = combineDeviceInfo(got, mi.mountPath, mi.incoming)
			}
			if d := cmp.Diff(tc.want, got, cmpopts.EquateComparable(deviceInfo{})); d != "" {
				t.Errorf("got %v, want %v diff(-want,+got):\n %v", got, tc.want, d)
			}
		})
	}
}
