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

package conformance

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfra/ufs"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

const (
	testLocalFSName = "testing/testassets"
)

func TestRsync(t *testing.T) {
	srcFS, err := ufsTesting.NewLocalFS(t.Context(), testLocalFSName)
	if err != nil {
		t.Fatalf("cannot mount localFS(%q), %s", testLocalFSName, err)
	}
	t.Cleanup(ufsTesting.ValidateClose(t, srcFS))
	for _, fsysTC := range getAllRegularTestCaseList() {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)
			if err := ufs.Rsync(srcFS, fsys, ufsTesting.CwdPath); err != nil {
				t.Errorf("rsync failed with error, %s", err)
			}

			err := ufs.ForEachFilename(srcFS, ufsTesting.CwdPath, func(name string) error {
				srcData, err := fs.ReadFile(srcFS, name)
				if err != nil {
					return fmt.Errorf("cannot read srcFS(%q), %w", name, err)
				}
				gotData, err := fs.ReadFile(fsys, name)
				if err != nil {
					return fmt.Errorf("cannot read destFS(%q), %w", name, err)
				}
				wantString := string(srcData)
				gotString := string(gotData)
				if diff := cmp.Diff(wantString, gotString); diff != "" {
					return fmt.Errorf("%q mismatch got %s, want %s diff(-want,+got):\n %v", name, gotString, wantString, diff)
				}
				return nil
			})
			if err != nil {
				t.Error(err)
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

			got, err := ufs.AbsPath(fsys, "file.txt")
			if supportedFS[tc.name] {
				if err != nil {
					t.Fatalf("AbsPath() returned unexpected error: %v", err)
				}
				if !filepath.IsAbs(got) {
					t.Errorf("AbsPath() = %q, want absolute path", got)
				}

				baseDir, err := ufs.AbsPath(fsys, ".")
				if err != nil {
					t.Fatalf("AbsPath(., ) = %v", err)
				}
				want := baseDir + string(filepath.Separator) + "file.txt"
				if got != want {
					t.Errorf("AbsPath() = %q, want %q", got, want)
				}

				subPath, err := ufs.AbsPath(fsys, "sub/dir/deep.txt")
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
