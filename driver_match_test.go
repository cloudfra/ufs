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
)

// TestArchiveDriverMatch verifies which driver each name is dispatched to: a
// local path that names an archive goes to the archive driver, and a name
// that another driver owns by scheme stays with that driver whatever its
// extension.
func TestArchiveDriverMatch(t *testing.T) {
	testCases := []struct {
		name string
		want string
	}{
		{name: "a.zip", want: "local-archive"},
		{name: "A.ZIP", want: "local-archive"},
		{name: "dir/a.tar.gz", want: "local-archive"},
		{name: "file:///x/a.zip", want: "local-archive"},
		{name: "archive:///x/a.zip", want: "archive"},
		{name: "archive:///x/plain", want: "archive"},
		{name: "memory:a.zip", want: "memory"},
		{name: "null:a.tar", want: "null"},
		{name: "angry:x.7z", want: "angry"},
		{name: "http://example.com/a.zip", want: "http-archive"},
		{name: "dir", want: "local"},
		{name: ".", want: "local"},
		{name: "file:///x/dir", want: "local"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := getRegistrar().matchDriver(tc.name)
			if err != nil {
				t.Fatalf("matchDriver(%q) = %v, want driver %q", tc.name, err, tc.want)
			}
			if got.Name != tc.want {
				t.Errorf("matchDriver(%q) = %q, want %q", tc.name, got.Name, tc.want)
			}
		})
	}
}

// TestUnknownSchemeMatchesNoDriver verifies that a name with a scheme that no
// registered driver serves is not claimed by the local driver, so the caller
// is told about the missing driver instead of a missing directory.
func TestUnknownSchemeMatchesNoDriver(t *testing.T) {
	for _, name := range []string{"nosuchscheme:", "nosuchscheme://", "nosuchscheme:a.zip", "gs://bucket/dir"} {
		if got, err := getRegistrar().matchDriver(name); err == nil {
			t.Errorf("matchDriver(%q) = %q, want error", name, got.Name)
		}
	}
}

// TestNewFailureReturnsNil verifies that a failed New returns a nil
// interface, not a nil pointer inside a non-nil interface.
func TestNewFailureReturnsNil(t *testing.T) {
	fsys, err := New(t.Context(), "nosuchscheme://")
	if err == nil {
		t.Fatal("New() = nil error, want error")
	}
	if fsys != nil {
		t.Errorf("New() = %v, want a nil file system on error", fsys)
	}
}
