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
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/internal/pathutil"
)

// pathTestCases feeds both the pathutil-level checks that historically lived
// alongside it (now in internal/pathutil/pathutil_test.go) and
// TestIsMountableArchivePath (archivefs_test.go), which is why it still
// lives here rather than moving with the rest of path_test.go's contents.
var pathTestCases = []struct {
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
		input:                      pathutil.CwdPath,
		wantTrimSlash:              pathutil.CwdPath,
		wantSplitPath:              []string{pathutil.CwdPath},
		wantIsCwd:                  true,
		wantIsDirName:              true,
		wantCoerceUnix:             pathutil.CwdPath,
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

func TestAbsPath(t *testing.T) {
	t.Parallel()

	supportedFS := map[string]bool{
		"localFS":        true,
		"tempMountFS":    true,
		"nestFS.localFS": true,
		// gitFS wraps a tempMountFS over a real local clone, so it
		// genuinely supports AbsPath. nestFS.gitFS does not: nestFS's own
		// getAbsPath only recognizes a direct *localFS inner, not a
		// tempMountFS-backed one.
		"gitFS": true,
	}

	for _, tc := range getAllTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			defer validateClose(t, fsys)()

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
