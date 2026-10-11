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
	"os"
	"path/filepath"
	"strings"
	"testing"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

func TestAbsPath(t *testing.T) {
	t.Parallel()

	supportedFS := map[string]bool{
		"localFS":            true,
		"tempMountFS":        true,
		"nestFS.localFS":     true,
		"nestFS.tempMountFS": true,
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

// TestAbsPathRejectsInvalidNames checks that AbsPath does not turn a name that
// is not a valid path, such as one with "..", into a host path outside of the
// file system's root.
func TestAbsPathRejectsInvalidNames(t *testing.T) {
	t.Parallel()

	for _, tc := range getAllExceptAngryTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			defer ufsTesting.ValidateClose(t, fsys)()

			for _, name := range []string{"..", "../outside.txt", "a/../../outside.txt", "/etc/passwd", ""} {
				if got, err := AbsPath(fsys, name); err == nil {
					t.Errorf("AbsPath(%q) = %q, want an error", name, got)
				}
			}
		})
	}
}

func TestAbsPathOSRootRejectsInvalidNames(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, root)()

	if got, err := AbsPath(root, "../outside.txt"); err == nil {
		t.Errorf("AbsPath(../outside.txt) = %q, want an error", got)
	}
	if _, err := AbsPath(root, "inside.txt"); err != nil {
		t.Errorf("AbsPath(inside.txt) = %v, want nil", err)
	}
}
