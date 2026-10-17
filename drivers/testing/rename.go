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

package testing

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/ops"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

// renameTree is the content each Rename case starts from. It is kept below
// one directory, apart from anything a test backend is seeded with, and every
// directory holds a file, so that it also exists on bucket-backed file
// systems.
var renameTree = map[string]string{
	"r/a/f.txt":     "f",
	"r/a/sub/g.txt": "g",
	"r/b/empty.txt": "",
	"r/b/h.txt":     "h",
	"r/c.txt":       "c",
}

// Rename verifies [ops.Rename], which uses the file system's own Rename when it
// has one: files and directory trees move with their content, and a missing
// source, an existing destination, the root, a move into itself and invalid
// paths fail with the documented errors and change nothing.
func Rename(t *testing.T, createFS func(*testing.T) types.WriteFS) {
	t.Run("Rename", func(t *testing.T) {
		t.Parallel()

		moves := []struct {
			name             string
			oldPath, newPath string
			want             map[string]string
		}{
			{
				name:    "File",
				oldPath: "r/c.txt",
				newPath: "r/d.txt",
				want:    map[string]string{"r/a/f.txt": "f", "r/a/sub/g.txt": "g", "r/b/empty.txt": "", "r/b/h.txt": "h", "r/d.txt": "c"},
			},
			{
				name:    "FileToOtherDir",
				oldPath: "r/c.txt",
				newPath: "r/b/c.txt",
				want:    map[string]string{"r/a/f.txt": "f", "r/a/sub/g.txt": "g", "r/b/empty.txt": "", "r/b/h.txt": "h", "r/b/c.txt": "c"},
			},
			{
				name:    "FileFromSubDir",
				oldPath: "r/a/sub/g.txt",
				newPath: "r/g.txt",
				want:    map[string]string{"r/a/f.txt": "f", "r/b/empty.txt": "", "r/b/h.txt": "h", "r/c.txt": "c", "r/g.txt": "g"},
			},
			{
				name:    "EmptyFile",
				oldPath: "r/b/empty.txt",
				newPath: "r/empty.txt",
				want:    map[string]string{"r/a/f.txt": "f", "r/a/sub/g.txt": "g", "r/b/h.txt": "h", "r/c.txt": "c", "r/empty.txt": ""},
			},
			{
				name:    "Dir",
				oldPath: "r/a",
				newPath: "r/b/a2",
				want:    map[string]string{"r/b/a2/f.txt": "f", "r/b/a2/sub/g.txt": "g", "r/b/empty.txt": "", "r/b/h.txt": "h", "r/c.txt": "c"},
			},
		}
		for _, tc := range moves {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys := newRenameFS(t, createFS)
				if err := ops.Rename(fsys, tc.oldPath, tc.newPath); err != nil {
					t.Fatalf("Rename(%q, %q) = %v", tc.oldPath, tc.newPath, err)
				}
				assertRenameTree(t, fsys, tc.want)
				if _, err := fsys.Stat(tc.oldPath); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("Stat(%q) after Rename = %v, want %v", tc.oldPath, err, fs.ErrNotExist)
				}
			})
		}

		failures := []struct {
			name             string
			oldPath, newPath string
			wantErr          error
			wantPathError    bool
		}{
			{name: "MissingSource", oldPath: "r/x.txt", newPath: "r/y.txt", wantErr: fs.ErrNotExist},
			{name: "ExistingFile", oldPath: "r/c.txt", newPath: "r/a/f.txt", wantErr: fs.ErrExist},
			{name: "ExistingDir", oldPath: "r/a", newPath: "r/b", wantErr: fs.ErrExist},
			{name: "FileOverDir", oldPath: "r/c.txt", newPath: "r/b", wantErr: fs.ErrExist},
			{name: "Self", oldPath: "r/c.txt", newPath: "r/c.txt", wantErr: fs.ErrExist},
			{name: "IntoItself", oldPath: "r/a", newPath: "r/a/sub/a", wantErr: fs.ErrInvalid},
			{name: "FromRoot", oldPath: ".", newPath: "r/x", wantErr: fs.ErrPermission},
			{name: "ToRoot", oldPath: "r/c.txt", newPath: ".", wantErr: fs.ErrPermission},
			{name: "InvalidOldPath", oldPath: "../c.txt", newPath: "r/d.txt", wantErr: fs.ErrInvalid, wantPathError: true},
			{name: "InvalidNewPath", oldPath: "r/c.txt", newPath: "/d.txt", wantErr: fs.ErrInvalid, wantPathError: true},
		}
		for _, tc := range failures {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys := newRenameFS(t, createFS)
				err := ops.Rename(fsys, tc.oldPath, tc.newPath)
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Rename(%q, %q) = %v, want %v", tc.oldPath, tc.newPath, err, tc.wantErr)
				}
				var pathErr *fs.PathError
				var linkErr *os.LinkError
				if tc.wantPathError && !errors.As(err, &pathErr) {
					t.Errorf("Rename(%q, %q) = %T, want *fs.PathError", tc.oldPath, tc.newPath, err)
				} else if !tc.wantPathError && !errors.As(err, &linkErr) {
					t.Errorf("Rename(%q, %q) = %T, want *os.LinkError", tc.oldPath, tc.newPath, err)
				}
				assertRenameTree(t, fsys, renameTree)
			})
		}
	})
}

// newRenameFS returns a file system from createFS holding renameTree.
func newRenameFS(t *testing.T, createFS func(*testing.T) types.WriteFS) types.WriteFS {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	for _, dir := range []string{"r/a/sub", "r/b"} {
		mkdirForTest(t, fsys, dir)
	}
	for name, content := range renameTree {
		WriteFile(t, fsys, name, content)
	}
	return fsys
}

// assertRenameTree checks that the files of fsys and their content are want.
func assertRenameTree(t *testing.T, fsys types.WriteFS, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	err := fs.WalkDir(fsys, "r", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fsys.ReadFile(name)
		if err != nil {
			return err
		}
		got[name] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir() = %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("files diff(-want,+got):\n%s", diff)
	}
}
