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
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

var pathTestCases = []struct {
	input                      string
	wantTrimSlash              string
	wantSplitPath              []string
	wantIsCwd                  bool
	wantIsDirName              bool
	wantIsMountableArchivePath bool
}{
	{
		input:                      "",
		wantTrimSlash:              "",
		wantSplitPath:              []string{""},
		wantIsCwd:                  true,
		wantIsDirName:              true,
		wantIsMountableArchivePath: false,
	},
	{
		input:                      CwdPath,
		wantTrimSlash:              CwdPath,
		wantSplitPath:              []string{CwdPath},
		wantIsCwd:                  true,
		wantIsDirName:              true,
		wantIsMountableArchivePath: false,
	},
	{
		input:                      "/",
		wantTrimSlash:              "",
		wantSplitPath:              []string{""},
		wantIsCwd:                  false,
		wantIsDirName:              true,
		wantIsMountableArchivePath: false,
	},
	{
		input:                      "abc",
		wantTrimSlash:              "abc",
		wantSplitPath:              []string{"abc"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: false,
	},
	{
		input:                      "/abc/d/",
		wantTrimSlash:              "abc/d",
		wantSplitPath:              []string{"abc", "d"},
		wantIsCwd:                  false,
		wantIsDirName:              true,
		wantIsMountableArchivePath: false,
	},
	{
		input:                      "abc/d/",
		wantTrimSlash:              "abc/d",
		wantSplitPath:              []string{"abc", "d"},
		wantIsCwd:                  false,
		wantIsDirName:              true,
		wantIsMountableArchivePath: false,
	},
	{
		input:                      "/abc/d",
		wantTrimSlash:              "abc/d",
		wantSplitPath:              []string{"abc", "d"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: false,
	},
	{
		input:                      "abc\\d",
		wantTrimSlash:              "abc\\d",
		wantSplitPath:              []string{"abc\\d"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: false,
	},
	{
		input:                      "\\abc\\",
		wantTrimSlash:              "abc",
		wantSplitPath:              []string{"abc"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: false,
	},
	{
		input:                      "ok.tar",
		wantTrimSlash:              "ok.tar",
		wantSplitPath:              []string{"ok.tar"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: true,
	},
	{
		input:                      "ok.tar.gz",
		wantTrimSlash:              "ok.tar.gz",
		wantSplitPath:              []string{"ok.tar.gz"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: true,
	},
	{
		input:                      "ok.tar.bz2",
		wantTrimSlash:              "ok.tar.bz2",
		wantSplitPath:              []string{"ok.tar.bz2"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: true,
	},
	{
		input:                      "ok.tar.xz",
		wantTrimSlash:              "ok.tar.xz",
		wantSplitPath:              []string{"ok.tar.xz"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: true,
	},
	{
		input:                      "ok.tar.lz4",
		wantTrimSlash:              "ok.tar.lz4",
		wantSplitPath:              []string{"ok.tar.lz4"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: true,
	},
	{
		input:                      "ok.tar.br",
		wantTrimSlash:              "ok.tar.br",
		wantSplitPath:              []string{"ok.tar.br"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: true,
	},
	{
		input:                      "ok.tar.zst",
		wantTrimSlash:              "ok.tar.zst",
		wantSplitPath:              []string{"ok.tar.zst"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: true,
	},
	{
		input:                      "ok.zip",
		wantTrimSlash:              "ok.zip",
		wantSplitPath:              []string{"ok.zip"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: true,
	},
	{
		input:                      "ok.tar.lzma",
		wantTrimSlash:              "ok.tar.lzma",
		wantSplitPath:              []string{"ok.tar.lzma"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: false,
	},
	{
		input:                      "ok.7z",
		wantTrimSlash:              "ok.7z",
		wantSplitPath:              []string{"ok.7z"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: true,
	},
	{
		input:                      "ok.7Z",
		wantTrimSlash:              "ok.7Z",
		wantSplitPath:              []string{"ok.7Z"},
		wantIsCwd:                  false,
		wantIsDirName:              false,
		wantIsMountableArchivePath: true,
	},
}

func TestRemovePathPrefix(t *testing.T) {
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
			got, gotOk := removePathPrefix(tc.path, tc.removePath)
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
			if got := trimSlash(tc.input); got != tc.wantTrimSlash {
				t.Errorf("trimSlash(%q) got: %v, want: %v", tc.input, got, tc.wantTrimSlash)
			}
		})
	}
}

func TestSplitPath(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := splitPath(tc.input)
			if diff := cmp.Diff(got, tc.wantSplitPath); diff != "" {
				t.Errorf("splitPath(%q) got: %v, want: %v, diff: %s", tc.input, got, tc.wantTrimSlash, diff)
			}
		})
	}
}

func TestIsCwd(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(fmt.Sprintf("%q", tc.input), func(t *testing.T) {
			t.Parallel()
			if got := isCwd(tc.input); got != tc.wantIsCwd {
				t.Errorf("isCwd(%q) got: %v, want: %v", tc.input, got, tc.wantIsCwd)
			}
		})
	}
}

func TestIsDirName(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(fmt.Sprintf("%q", tc.input), func(t *testing.T) {
			t.Parallel()
			if got := isDirName(tc.input); got != tc.wantIsDirName {
				t.Errorf("isDirName(%q) got: %v, want: %v", tc.input, got, tc.wantIsDirName)
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
			err := validPath("open", tc.input)

			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "is not a valid path for") {
					t.Errorf("validPath(open, %s) expected to contain 'is not a valid path for', got: %q", tc.input, err)
				}
			} else {
				if err != nil {
					t.Errorf("validPath(open, %s) returned error %q, want nil", tc.input, err)
				}
			}
		})
	}
}

func TestAbsPath(t *testing.T) {
	t.Parallel()

	supportedFS := map[string]bool{
		"localFS":        true,
		"tempMountFS":    true,
		"nestFS.localFS": true,
	}

	for _, tc := range getAllExceptAngryTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			defer ufsTesting.ValidateClose(t, fsys)()

			got, err := AbsPath(fsys, "file.txt")
			if supportedFS[tc.name] {
				if err != nil {
					t.Fatalf("AbsPath() returned unexpected error: %v", err)
				}
				if !filepath.IsAbs(got) {
					t.Errorf("AbsPath() = %q, want absolute path", got)
				}

				baseDir, err := AbsPath(fsys, ".")
				if err != nil {
					t.Fatalf("AbsPath(., ) = %v", err)
				}
				want := baseDir + string(filepath.Separator) + "file.txt"
				if got != want {
					t.Errorf("AbsPath() = %q, want %q", got, want)
				}

				subPath, err := AbsPath(fsys, "sub/dir/deep.txt")
				if err != nil {
					t.Fatalf("AbsPath(sub/dir/deep.txt) = %v", err)
				}
				wantSub := baseDir + string(filepath.Separator) + "sub" + string(filepath.Separator) + "dir" + string(filepath.Separator) + "deep.txt"
				if subPath != wantSub {
					t.Errorf("AbsPath(sub/dir/deep.txt) = %q, want %q", subPath, wantSub)
				}
			} else {
				if err == nil {
					t.Fatalf("AbsPath() = %q, want error for unsupported FS", got)
				}
				if !strings.Contains(err.Error(), "not accessible outside") {
					t.Errorf("error = %q, want it to mention 'not accessible outside'", err)
				}
			}
		})
	}
}
