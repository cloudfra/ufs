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

package localfs

import (
	"testing"
)

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
