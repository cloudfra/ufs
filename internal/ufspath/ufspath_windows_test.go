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

package ufspath

import "testing"

// TestNormalizeFileURIWindowsDriveLetters verifies that NormalizeFileURI
// converts the "/D:/path" form (produced after stripping "file://" from a
// canonical file:///D:/path URI) to the Windows-native "D:\path" form.
func TestNormalizeFileURIWindowsDriveLetters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{`file:///D:/some/path`, `D:\some\path`},
		{`file:///D:/`, `D:\`},
		{`file:///Z:/data`, `Z:\data`},
		{`file:///Z:/deeply/nested/dir`, `Z:\deeply\nested\dir`},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := NormalizeFileURI(tc.input)
			if got != tc.want {
				t.Errorf("NormalizeFileURI(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
