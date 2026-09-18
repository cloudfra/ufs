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

package pathutil

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var pathTestCases = []struct {
	input          string
	wantTrimSlash  string
	wantSplitPath  []string
	wantIsCwd      bool
	wantIsDirName  bool
	wantCoerceUnix string
}{
	{
		input:          "",
		wantTrimSlash:  "",
		wantSplitPath:  []string{""},
		wantIsCwd:      true,
		wantIsDirName:  true,
		wantCoerceUnix: "",
	},
	{
		input:          CwdPath,
		wantTrimSlash:  CwdPath,
		wantSplitPath:  []string{CwdPath},
		wantIsCwd:      true,
		wantIsDirName:  true,
		wantCoerceUnix: CwdPath,
	},
	{
		input:          "/",
		wantTrimSlash:  "",
		wantSplitPath:  []string{""},
		wantIsCwd:      false,
		wantIsDirName:  true,
		wantCoerceUnix: "/",
	},
	{
		input:          "abc",
		wantTrimSlash:  "abc",
		wantSplitPath:  []string{"abc"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "abc",
	},
	{
		input:          "/abc/d/",
		wantTrimSlash:  "abc/d",
		wantSplitPath:  []string{"abc", "d"},
		wantIsCwd:      false,
		wantIsDirName:  true,
		wantCoerceUnix: "/abc/d/",
	},
	{
		input:          "abc/d/",
		wantTrimSlash:  "abc/d",
		wantSplitPath:  []string{"abc", "d"},
		wantIsCwd:      false,
		wantIsDirName:  true,
		wantCoerceUnix: "abc/d/",
	},
	{
		input:          "/abc/d",
		wantTrimSlash:  "abc/d",
		wantSplitPath:  []string{"abc", "d"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "/abc/d",
	},
	{
		input:          "abc\\d",
		wantTrimSlash:  "abc\\d",
		wantSplitPath:  []string{"abc\\d"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "abc/d",
	},
	{
		input:          "\\abc\\",
		wantTrimSlash:  "abc",
		wantSplitPath:  []string{"abc"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "/abc/",
	},
}

func TestRemovePrefix(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		path       string
		removePath string
		want       string
		wantOk     bool
	}{
		{
			path:       "",
			removePath: "",
			want:       CwdPath,
			wantOk:     true,
		},
		{
			path:       CwdPath,
			removePath: "",
			want:       CwdPath,
			wantOk:     true,
		},
		{
			path:       "",
			removePath: CwdPath,
			want:       CwdPath,
			wantOk:     true,
		},
		{
			path:       CwdPath,
			removePath: "abc/def",
			want:       CwdPath,
			wantOk:     false,
		},
		{
			path:       "abc/def",
			removePath: "",
			want:       "abc/def",
			wantOk:     true,
		},
		{
			path:       "abc/def",
			removePath: "abc",
			want:       "def",
			wantOk:     true,
		},
		{
			path:       "abc/def",
			removePath: "abc/d",
			want:       "abc/def",
			wantOk:     false,
		},
		{
			path:       "abc/def",
			removePath: "abc/def",
			want:       CwdPath,
			wantOk:     true,
		},
		{
			path:       "a/b/c",
			removePath: "a/b",
			want:       "c",
			wantOk:     true,
		},
		{
			path:       "a/b/c/",
			removePath: "a/b/",
			want:       "c",
			wantOk:     true,
		},
		{
			path:       "a/b/c",
			removePath: "a/b/",
			want:       "c",
			wantOk:     true,
		},
		{
			path:       "a/b/c/",
			removePath: "a/b",
			want:       "c",
			wantOk:     true,
		},
		{
			path:       "a/b",
			removePath: "a/b/c",
			want:       "a/b",
			wantOk:     false,
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s - %s", tc.path, tc.removePath), func(t *testing.T) {
			t.Parallel()
			got, gotOk := RemovePrefix(tc.path, tc.removePath)
			if got != tc.want {
				t.Errorf("path: got: %q, want: %q", got, tc.want)
			}
			if gotOk != tc.wantOk {
				t.Errorf("ok: got: %t, want: %t", gotOk, tc.wantOk)
			}
		})
	}
}

func TestTrimSlash(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := TrimSlash(tc.input); got != tc.wantTrimSlash {
				t.Errorf("TrimSlash(%q) got: %v, want: %v", tc.input, got, tc.wantTrimSlash)
			}
		})
	}
}

func TestSplitPath(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := SplitPath(tc.input)
			if diff := cmp.Diff(got, tc.wantSplitPath); diff != "" {
				t.Errorf("SplitPath(%q) got: %v, want: %v, diff: %s", tc.input, got, tc.wantTrimSlash, diff)
			}
		})
	}
}

func TestIsCwd(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(fmt.Sprintf("%q", tc.input), func(t *testing.T) {
			t.Parallel()
			if got := IsCwd(tc.input); got != tc.wantIsCwd {
				t.Errorf("IsCwd(%q) got: %v, want: %v", tc.input, got, tc.wantIsCwd)
			}
		})
	}
}

func TestIsDirName(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(fmt.Sprintf("%q", tc.input), func(t *testing.T) {
			t.Parallel()
			if got := IsDirName(tc.input); got != tc.wantIsDirName {
				t.Errorf("IsDirName(%q) got: %v, want: %v", tc.input, got, tc.wantIsDirName)
			}
		})
	}
}

func TestValidPath(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		input   string
		wantErr bool
	}{
		{input: CwdPath, wantErr: false},
		{input: "./.", wantErr: true},
		{input: "a\\b\\.\\..\\c", wantErr: false},
		{input: "a/b/./../c", wantErr: true},
		{input: "a/b/../c", wantErr: true},
		{input: "C:/", wantErr: true},
		{input: "C:\\", wantErr: false},
	}
	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			err := ValidPath("open", tc.input)

			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "is not a valid path for") {
					t.Errorf("ValidPath(open, %s) expected to contain 'is not a valid path for', got: %q", tc.input, err)
				}
			} else {
				if err != nil {
					t.Errorf("ValidPath(open, %s) returned error %q, want nil", tc.input, err)
				}
			}
		})
	}
}

func TestCoerceUnix(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := CoerceUnix(tc.input)
			if got != tc.wantCoerceUnix {
				t.Errorf("CoerceUnix(%q) got: %q, want: %q", tc.input, got, tc.wantCoerceUnix)
			}
		})
	}
}
