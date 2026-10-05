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

package osutil

import (
	"path/filepath"
	"testing"
)

func TestIsLocalName(t *testing.T) {
	dir := t.TempDir()
	// A directory whose name looks like a scheme is still a local name.
	schemeDir := filepath.Join(dir, "odd:name")
	if err := MkdirAll(schemeDir); err != nil {
		t.Skipf("cannot create %q on this platform: %v", schemeDir, err)
	}
	testCases := []struct {
		name string
		want bool
	}{
		{name: "file:", want: true},
		{name: "file://", want: true},
		{name: "file:///x/y", want: true},
		{name: ".", want: true},
		{name: "relative/dir", want: true},
		{name: "/abs/dir", want: true},
		{name: "a.zip", want: true},
		{name: `C:\dir`, want: true},
		{name: "C:", want: true},
		{name: "1:x", want: true},
		{name: "dir/with:colon", want: true},
		{name: "memory:", want: false},
		{name: "memory://", want: false},
		{name: "null:", want: false},
		{name: "bolt:/data/db.zip", want: false},
		{name: "filefs://", want: false},
		{name: "gs://bucket/dir", want: false},
		{name: "git+ssh://host/repo", want: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsLocalName(tc.name); got != tc.want {
				t.Errorf("IsLocalName(%q) = %t, want %t", tc.name, got, tc.want)
			}
		})
	}
}

func TestLocalPath(t *testing.T) {
	testCases := []struct {
		name string
		want string
	}{
		{name: "dir/file", want: "dir/file"},
		{name: "/abs/dir", want: "/abs/dir"},
		{name: "file:///abs/dir", want: "/abs/dir"},
		{name: "file:relative", want: "relative"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LocalPath(tc.name); got != tc.want {
				t.Errorf("LocalPath(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
