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
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var (
	pathTestCases = []struct {
		input                      string
		wantTrimSlash              string
		wantSplitPath              []string
		wantIsCwd                  bool
		wantIsDirName              bool
		wantCoerceUnix             string
		wantIsMountableArchivePath bool
	}{
		{
			input:                      "",
			wantTrimSlash:              "",
			wantSplitPath:              []string{""},
			wantIsCwd:                  true,
			wantIsDirName:              true,
			wantCoerceUnix:             "",
			wantIsMountableArchivePath: false,
		},
		{
			input:                      cwdPath,
			wantTrimSlash:              cwdPath,
			wantSplitPath:              []string{cwdPath},
			wantIsCwd:                  true,
			wantIsDirName:              true,
			wantCoerceUnix:             cwdPath,
			wantIsMountableArchivePath: false,
		},
		{
			input:                      "/",
			wantTrimSlash:              "",
			wantSplitPath:              []string{""},
			wantIsCwd:                  false,
			wantIsDirName:              true,
			wantCoerceUnix:             "/",
			wantIsMountableArchivePath: false,
		},
		{
			input:                      "abc",
			wantTrimSlash:              "abc",
			wantSplitPath:              []string{"abc"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "abc",
			wantIsMountableArchivePath: false,
		},
		{
			input:                      "/abc/d/",
			wantTrimSlash:              "abc/d",
			wantSplitPath:              []string{"abc", "d"},
			wantIsCwd:                  false,
			wantIsDirName:              true,
			wantCoerceUnix:             "/abc/d/",
			wantIsMountableArchivePath: false,
		},
		{
			input:                      "abc/d/",
			wantTrimSlash:              "abc/d",
			wantSplitPath:              []string{"abc", "d"},
			wantIsCwd:                  false,
			wantIsDirName:              true,
			wantCoerceUnix:             "abc/d/",
			wantIsMountableArchivePath: false,
		},
		{
			input:                      "/abc/d",
			wantTrimSlash:              "abc/d",
			wantSplitPath:              []string{"abc", "d"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "/abc/d",
			wantIsMountableArchivePath: false,
		},
		{
			input:                      "abc\\d",
			wantTrimSlash:              "abc\\d",
			wantSplitPath:              []string{"abc\\d"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "abc/d",
			wantIsMountableArchivePath: false,
		},
		{
			input:                      "\\abc\\",
			wantTrimSlash:              "abc",
			wantSplitPath:              []string{"abc"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "/abc/",
			wantIsMountableArchivePath: false,
		},
		{
			input:                      "ok.tar",
			wantTrimSlash:              "ok.tar",
			wantSplitPath:              []string{"ok.tar"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "ok.tar",
			wantIsMountableArchivePath: true,
		},
		{
			input:                      "ok.tar.gz",
			wantTrimSlash:              "ok.tar.gz",
			wantSplitPath:              []string{"ok.tar.gz"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "ok.tar.gz",
			wantIsMountableArchivePath: true,
		},
		{
			input:                      "ok.tar.bz2",
			wantTrimSlash:              "ok.tar.bz2",
			wantSplitPath:              []string{"ok.tar.bz2"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "ok.tar.bz2",
			wantIsMountableArchivePath: true,
		},
		{
			input:                      "ok.tar.xz",
			wantTrimSlash:              "ok.tar.xz",
			wantSplitPath:              []string{"ok.tar.xz"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "ok.tar.xz",
			wantIsMountableArchivePath: true,
		},
		{
			input:                      "ok.tar.lz4",
			wantTrimSlash:              "ok.tar.lz4",
			wantSplitPath:              []string{"ok.tar.lz4"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "ok.tar.lz4",
			wantIsMountableArchivePath: true,
		},
		{
			input:                      "ok.tar.br",
			wantTrimSlash:              "ok.tar.br",
			wantSplitPath:              []string{"ok.tar.br"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "ok.tar.br",
			wantIsMountableArchivePath: true,
		},
		{
			input:                      "ok.tar.zst",
			wantTrimSlash:              "ok.tar.zst",
			wantSplitPath:              []string{"ok.tar.zst"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "ok.tar.zst",
			wantIsMountableArchivePath: true,
		},
		{
			input:                      "ok.zip",
			wantTrimSlash:              "ok.zip",
			wantSplitPath:              []string{"ok.zip"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "ok.zip",
			wantIsMountableArchivePath: true,
		},
		{
			input:                      "ok.tar.lzma",
			wantTrimSlash:              "ok.tar.lzma",
			wantSplitPath:              []string{"ok.tar.lzma"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "ok.tar.lzma",
			wantIsMountableArchivePath: false,
		},
		{
			input:                      "ok.7z",
			wantTrimSlash:              "ok.7z",
			wantSplitPath:              []string{"ok.7z"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "ok.7z",
			wantIsMountableArchivePath: true,
		},
		{
			input:                      "ok.7Z",
			wantTrimSlash:              "ok.7Z",
			wantSplitPath:              []string{"ok.7Z"},
			wantIsCwd:                  false,
			wantIsDirName:              false,
			wantCoerceUnix:             "ok.7Z",
			wantIsMountableArchivePath: true,
		},
	}
)

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
			want:       cwdPath,
			wantOk:     true,
		},
		{
			path:       cwdPath,
			removePath: "",
			want:       cwdPath,
			wantOk:     true,
		},
		{
			path:       "",
			removePath: cwdPath,
			want:       cwdPath,
			wantOk:     true,
		},
		{
			path:       cwdPath,
			removePath: "abc/def",
			want:       cwdPath,
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
			want:       cwdPath,
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

// TestPathFunctions checks trimSlash, splitPath, isCwd, isDirName, and
// coerceUnix for every entry in pathTestCases in a single pass.
func TestPathFunctions(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(fmt.Sprintf("%q", tc.input), func(t *testing.T) {
			t.Parallel()
			if got := trimSlash(tc.input); got != tc.wantTrimSlash {
				t.Errorf("trimSlash(%q) = %q, want %q", tc.input, got, tc.wantTrimSlash)
			}
			if diff := cmp.Diff(splitPath(tc.input), tc.wantSplitPath); diff != "" {
				t.Errorf("splitPath(%q) mismatch (-want +got):\n%s", tc.input, diff)
			}
			if got := isCwd(tc.input); got != tc.wantIsCwd {
				t.Errorf("isCwd(%q) = %v, want %v", tc.input, got, tc.wantIsCwd)
			}
			if got := isDirName(tc.input); got != tc.wantIsDirName {
				t.Errorf("isDirName(%q) = %v, want %v", tc.input, got, tc.wantIsDirName)
			}
			if got := coerceUnix(tc.input); got != tc.wantCoerceUnix {
				t.Errorf("coerceUnix(%q) = %q, want %q", tc.input, got, tc.wantCoerceUnix)
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
		{input: cwdPath, wantErr: false},
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

func TestJoinErrors(t *testing.T) {
	t.Parallel()

	errA := errors.New("error A")
	errB := errors.New("error B")

	testCases := []struct {
		name       string
		errs       []error
		wantNil    bool
		wantSameAs error // non-nil: result must be this exact value (no wrapper)
		wantIsA    bool
		wantIsB    bool
	}{
		{name: "no args", errs: nil, wantNil: true},
		{name: "single nil", errs: []error{nil}, wantNil: true},
		{name: "multiple nils", errs: []error{nil, nil, nil}, wantNil: true},
		{name: "single error", errs: []error{errA}, wantSameAs: errA, wantIsA: true},
		{name: "nil then error", errs: []error{nil, errA}, wantSameAs: errA, wantIsA: true},
		{name: "error then nil", errs: []error{errA, nil}, wantSameAs: errA, wantIsA: true},
		{name: "two errors", errs: []error{errA, errB}, wantIsA: true, wantIsB: true},
		{name: "nil two errors nil", errs: []error{nil, errA, errB, nil}, wantIsA: true, wantIsB: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := joinErrors(tc.errs...)
			if tc.wantNil {
				if got != nil {
					t.Errorf("joinErrors() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("joinErrors() = nil, want non-nil")
			}
			if tc.wantSameAs != nil && got != tc.wantSameAs {
				t.Errorf("joinErrors() returned a wrapped error; want the identical error value, got %v", got)
			}
			if tc.wantIsA && !errors.Is(got, errA) {
				t.Errorf("joinErrors(): errors.Is(result, errA) = false, want true")
			}
			if tc.wantIsB && !errors.Is(got, errB) {
				t.Errorf("joinErrors(): errors.Is(result, errB) = false, want true")
			}
		})
	}
}

