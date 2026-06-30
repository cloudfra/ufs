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

//go:build !windows

package ufs

import "testing"

func TestIsPathCoveredBy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		target string
		prefix string
		want   bool
	}{
		{"/home/user", "/", true},
		{"/home/user", "/home", true},
		{"/home/user", "/home/user", true},
		{"/home/user", "/home/user/sub", false},
		{"/home/user", "/homeother", false},
		{"/", "/", true},
		{"/mnt/cdrom", "/mnt", true},
		{"/mnt/cdrom", "/mnt/cdrom", true},
		{"/mnt/cdromy", "/mnt/cdrom", false},
	}
	for _, tc := range tests {
		t.Run(tc.prefix+"->"+tc.target, func(t *testing.T) {
			got := isPathCoveredBy(tc.target, tc.prefix)
			if got != tc.want {
				t.Errorf("isPathCoveredBy(%q, %q) = %v, want %v", tc.target, tc.prefix, got, tc.want)
			}
		})
	}
}

func TestIsPathUnder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		target string
		root   string
		want   bool
	}{
		{"/home/user/sub", "/home/user", true},
		{"/home/user", "/home/user", false},
		{"/home/user", "/home", true},
		{"/home/user", "/", true},
		{"/homeother", "/home", false},
		{"/mnt/cdrom", "/mnt", true},
		{"/mnt", "/mnt/cdrom", false},
	}
	for _, tc := range tests {
		t.Run(tc.root+"->"+tc.target, func(t *testing.T) {
			got := isPathUnder(tc.target, tc.root)
			if got != tc.want {
				t.Errorf("isPathUnder(%q, %q) = %v, want %v", tc.target, tc.root, got, tc.want)
			}
		})
	}
}

func TestRelPathUnder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		target string
		root   string
		want   string
	}{
		{"/home/user/sub", "/home/user", "sub"},
		{"/home/user/a/b", "/home/user", "a/b"},
		{"/mnt/cdrom", "/", "mnt/cdrom"},
		{"/mnt/cdrom", "/mnt", "cdrom"},
	}
	for _, tc := range tests {
		t.Run(tc.root+"->"+tc.target, func(t *testing.T) {
			got := relPathUnder(tc.target, tc.root)
			if got != tc.want {
				t.Errorf("relPathUnder(%q, %q) = %q, want %q", tc.target, tc.root, got, tc.want)
			}
		})
	}
}
