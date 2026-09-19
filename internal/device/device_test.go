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

package device

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var (
	fakeNvmeInfo = New("/dev/nvme0n1", "nvme", 2, false)
	fakeHddInfo  = New("/dev/hdd1", "hdd", 1, false)
	fakeUsbInfo  = New("/dev/usb1", "usb", 1, false)
	fakeGCSInfo  = New("gs://bucket", "network", 1, true)
)

func TestNew(t *testing.T) {
	t.Parallel()

	info := New("gs://bucket", "network", 1, true)
	if got := info.Name(); got != "gs://bucket" {
		t.Errorf("Name() = %q, want %q", got, "gs://bucket")
	}
	if got := info.DeviceType(); got != "network" {
		t.Errorf("DeviceType() = %q, want %q", got, "network")
	}
	if got := info.ThreadCount(); got != 1 {
		t.Errorf("ThreadCount() = %d, want %d", got, 1)
	}
	if got := info.Remote(); got != true {
		t.Errorf("Remote() = %t, want %t", got, true)
	}
}

func TestNewMap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input Info
		want  map[string]Info
	}{
		{
			input: Default,
			want: map[string]Info{
				".": Default,
			},
		},
		{
			input: fakeNvmeInfo,
			want: map[string]Info{
				".": fakeNvmeInfo,
			},
		},
		{
			input: fakeGCSInfo,
			want: map[string]Info{
				".": fakeGCSInfo,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.input.Name(), func(t *testing.T) {
			got := NewMap(tc.input)
			if d := cmp.Diff(tc.want, got.byPath, cmpopts.EquateComparable(Info{})); d != "" {
				t.Errorf("got %v, want %v diff(-want,+got):\n %v", got, tc.want, d)
			}
		})
	}
}

type mountInfo struct {
	mountPath string
	incoming  Map
}

func TestCombine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		src      Map
		incoming []mountInfo
		want     map[string]Info
	}{
		{
			name: "src and incoming nil",
			src:  Map{},
			incoming: []mountInfo{
				{
					mountPath: "nil",
					incoming:  Map{},
				},
			},
			want: map[string]Info{},
		},
		{
			name: "src populated, incoming nil",
			src:  NewMap(Default),
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  Map{},
				},
			},
			want: map[string]Info{
				".": Default,
			},
		},
		{
			name: "src nil, incoming populated",
			src:  Map{},
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  NewMap(fakeNvmeInfo),
				},
			},
			want: map[string]Info{
				"nvme": fakeNvmeInfo,
			},
		},
		{
			name: "import nested",
			src:  NewMap(Default),
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  NewMap(fakeNvmeInfo),
				},
			},
			want: map[string]Info{
				".":    Default,
				"nvme": fakeNvmeInfo,
			},
		},
		{
			name: "import nested",
			src:  NewMap(Default),
			incoming: []mountInfo{
				{
					mountPath: "nvme",
					incoming:  NewMap(fakeNvmeInfo),
				},
				{
					mountPath: "nvme/hdd",
					incoming:  NewMap(fakeHddInfo),
				},
				{
					mountPath: "nvme/nvme",
					incoming:  NewMap(fakeNvmeInfo),
				},
				{
					mountPath: "nvme/default",
					incoming:  NewMap(Default),
				},
				{
					mountPath: "nvme/usb",
					incoming:  NewMap(fakeUsbInfo),
				},
				{
					mountPath: "default",
					incoming:  NewMap(Default),
				},
				{
					mountPath: "default/default",
					incoming:  NewMap(Default),
				},
				{
					mountPath: "default/default/default",
					incoming:  NewMap(Default),
				},
				{
					mountPath: "default/default/usb",
					incoming:  NewMap(fakeUsbInfo),
				},
				{
					mountPath: "default/nvme",
					incoming:  NewMap(fakeNvmeInfo),
				},
			},
			want: map[string]Info{
				".":                   Default,
				"default/default/usb": fakeUsbInfo,
				"default/nvme":        fakeNvmeInfo,
				"nvme":                fakeNvmeInfo,
				"nvme/default":        Default,
				"nvme/hdd":            fakeHddInfo,
				"nvme/usb":            fakeUsbInfo,
			},
		},
		{
			name: "path boundary not confused by shared prefix",
			src:  NewMap(fakeHddInfo),
			incoming: []mountInfo{
				{
					mountPath: "fast",
					incoming:  NewMap(fakeNvmeInfo),
				},
				{
					mountPath: "fast/slow",
					incoming:  NewMap(fakeHddInfo),
				},
				{
					mountPath: "faster",
					incoming:  NewMap(fakeUsbInfo),
				},
			},
			want: map[string]Info{
				".":         fakeHddInfo,
				"fast":      fakeNvmeInfo,
				"fast/slow": fakeHddInfo,
				"faster":    fakeUsbInfo,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.src
			for _, mi := range tc.incoming {
				got = got.Combine(mi.mountPath, mi.incoming)
			}
			if d := cmp.Diff(tc.want, got.byPath, cmpopts.EquateComparable(Info{})); d != "" {
				t.Errorf("got %v, want %v diff(-want,+got):\n %v", got, tc.want, d)
			}
		})
	}
}
