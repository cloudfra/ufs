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
	"os"
	"testing"
)

// TestNameToURIWindowsDriveLetters verifies that nameToURI handles both
// "file://X:\path" strings (which url.Parse rejects) and bare Windows paths
// ("X:", "X:\") for drive letters other than C:.
func TestNameToURIWindowsDriveLetters(t *testing.T) {
	tests := []struct {
		input      string
		wantScheme string
		wantPath   string
	}{
		// With file:// prefix — url.Parse fails; our fix recovers these into
		// a canonical file:/// URL.
		{`file://D:\`, "file", `/D:/`},
		{`file://D:\some\path`, "file", `/D:/some/path`},
		{`file://Z:\data`, "file", `/Z:/data`},
		{`file://Z:\deeply\nested\dir`, "file", `/Z:/deeply/nested/dir`},
		// Without file:// prefix — url.Parse handles these as opaque URIs
		// (scheme = lowercase drive letter, path = "").
		{`D:`, "d", ""},
		{`D:\`, "d", ""},
		{`Z:`, "z", ""},
		{`Z:\`, "z", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			u, err := nameToURI(tt.input)
			if err != nil {
				t.Fatalf("nameToURI(%q) = %v, want nil", tt.input, err)
			}
			if u.Scheme != tt.wantScheme {
				t.Errorf("scheme = %q, want %q", u.Scheme, tt.wantScheme)
			}
			if u.Path != tt.wantPath {
				t.Errorf("path = %q, want %q", u.Path, tt.wantPath)
			}
		})
	}
}

// TestLocalFSNormalizePathWindowsDriveLetters verifies that localFSNormalizePath
// converts the "/D:/path" form (produced after stripping "file://" from a
// canonical file:///D:/path URI) to the Windows-native "D:\path" form.
func TestLocalFSNormalizePathWindowsDriveLetters(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`file:///D:/some/path`, `D:\some\path`},
		{`file:///D:/`, `D:\`},
		{`file:///Z:/data`, `Z:\data`},
		{`file:///Z:/deeply/nested/dir`, `Z:\deeply\nested\dir`},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := localFSNormalizePath(tt.input)
			if got != tt.want {
				t.Errorf("localFSNormalizePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestNewWindowsDriveLetters verifies that New() can mount drives other than C:\
// in all supported forms: bare drive ("D:"), drive root ("D:\"), and
// file://-prefixed ("file://D:\"). Each drive letter is skipped entirely when
// its root is not present on the host.
func TestNewWindowsDriveLetters(t *testing.T) {
	for _, letter := range []string{"D", "Z"} {
		letter := letter
		t.Run(letter+":", func(t *testing.T) {
			root := letter + `:\`
			if _, err := os.Stat(root); err != nil {
				t.Skipf("drive %s: not available: %v", letter, err)
			}
			for _, name := range []string{
				letter + `:`,              // D:  — current directory of the drive
				letter + `:\`,             // D:\ — root of the drive
				`file://` + letter + `:\`, // file://D:\
			} {
				fsys, err := New(t.Context(), name)
				if err != nil {
					t.Errorf("New(%q) = %v, want nil", name, err)
					continue
				}
				if err := fsys.Close(); err != nil {
					t.Errorf("fsys.Close() = %v, want nil", err)
				}
			}
		})
	}
}
