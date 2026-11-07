// Copyright 2026 Cloudfra
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
	"io"
	"io/fs"
	"maps"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

// copyFileTree is the content each CopyFile case starts from. It is kept below
// one directory, apart from anything a test backend is seeded with, and every
// directory holds a file, so that it also exists on bucket-backed file systems.
var copyFileTree = map[string]string{
	"c/a.txt":         "a",
	"c/dir/b.txt":     "b",
	"c/dir/sub/d.txt": "d",
	"c/empty.txt":     "",
	"c/other/e.txt":   "e",
}

// copyFileDirs are the directories of copyFileTree, created before its files.
var copyFileDirs = []string{"c/dir/sub", "c/other"}

// copyFileLargeContent is built from this many buffers, each one holding a
// different byte and being larger than the buffer a copy loop is likely to use.
const copyFileLargeBuffers = 8

const copyFileLargeBufferSize = 64 * 1024

// CopyFileFS verifies that a file system's CopyFile copies the content of a
// file and nothing else: a missing source, a taken destination, a directory
// source, the root and an invalid path fail without changing the tree.
//
// A copy stands on its own, so editing the destination afterwards may not
// change the source. The permission bits a copy carries are not checked.
func CopyFileFS(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.CopyFileFS) {
	if createFSFunc == nil {
		t.Fatal("createFSFunc is nil")
	}
	copyFileCopies(t, createFSFunc)
	copyFileRefusals(t, createFSFunc)
	copyFileIndependent(t, createFSFunc)
	copyFileLarge(t, createFSFunc)
	copyFileInvalidPaths(t, createFSFunc)
}

// copyFileCopies checks the copies that must succeed and the tree each leaves.
func copyFileCopies(t *testing.T, createFSFunc func(t *testing.T) types.CopyFileFS) {
	t.Run("CopyFile", func(t *testing.T) {
		t.Parallel()

		copies := []struct {
			name       string
			src, dst   string
			wantExtras map[string]string
		}{
			{
				name:       "File",
				src:        "c/a.txt",
				dst:        "c/f.txt",
				wantExtras: map[string]string{"c/f.txt": "a"},
			},
			{
				name:       "EmptyFile",
				src:        "c/empty.txt",
				dst:        "c/blank.txt",
				wantExtras: map[string]string{"c/blank.txt": ""},
			},
			{
				name:       "IntoExistingDir",
				src:        "c/a.txt",
				dst:        "c/dir/a.txt",
				wantExtras: map[string]string{"c/dir/a.txt": "a"},
			},
			{
				name:       "FromSubDir",
				src:        "c/dir/sub/d.txt",
				dst:        "c/d.txt",
				wantExtras: map[string]string{"c/d.txt": "d"},
			},
		}
		for _, tc := range copies {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys, _ := newCopyFS(t, createFSFunc)
				if err := fsys.CopyFile(tc.src, tc.dst); err != nil {
					t.Fatalf("CopyFile(%q, %q) = %v", tc.src, tc.dst, err)
				}
				assertCopyTree(t, fsys, copyFileWant(tc.wantExtras))
			})
		}
	})
}

// copyFileRefusals checks the copies that must fail, the error each reports,
// and that the tree is left exactly as it was.
func copyFileRefusals(t *testing.T, createFSFunc func(t *testing.T) types.CopyFileFS) {
	t.Run("CopyFileRefusals", func(t *testing.T) {
		t.Parallel()

		refusals := []struct {
			name string
			src  string
			dst  string
			// wantErr is the sentinel the copy must report, empty when the
			// sentinel for that case is still open in issue #548.
			wantErr error
			// wantDstAbsent asks for a destination that was never there to
			// stay missing, so that a copy that made an empty file or an
			// empty directory is caught even when it reported an error.
			wantDstAbsent bool
		}{
			{name: "MissingSource", src: "c/missing.txt", dst: "c/got.txt", wantErr: fs.ErrNotExist, wantDstAbsent: true},
			{name: "MissingSourceInDir", src: "c/dir/missing.txt", dst: "c/got.txt", wantErr: fs.ErrNotExist, wantDstAbsent: true},
			{name: "MissingSourceInMissingDir", src: "c/nodir/x.txt", dst: "c/got.txt", wantErr: fs.ErrNotExist, wantDstAbsent: true},
			{name: "Self", src: "c/a.txt", dst: "c/a.txt", wantErr: fs.ErrExist},
			{name: "ExistingFile", src: "c/a.txt", dst: "c/other/e.txt", wantErr: fs.ErrExist},
			{name: "ExistingDir", src: "c/a.txt", dst: "c/dir", wantErr: fs.ErrExist},
			{name: "ExistingOtherDir", src: "c/a.txt", dst: "c/other", wantErr: fs.ErrExist},
			{name: "DestUnderFile", src: "c/a.txt", dst: "c/a.txt/x.txt"},
			{name: "DirSource", src: "c/dir", dst: "c/copied", wantDstAbsent: true},
			{name: "DirSourceOverFile", src: "c/dir", dst: "c/other/e.txt"},
			{name: "SubDirSource", src: "c/dir/sub", dst: "c/copied", wantDstAbsent: true},
			{name: "FromRoot", src: ".", dst: "c/copied", wantErr: fs.ErrPermission, wantDstAbsent: true},
			{name: "ToRoot", src: "c/a.txt", dst: ".", wantErr: fs.ErrPermission},
		}
		for _, tc := range refusals {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys, _ := newCopyFS(t, createFSFunc)
				err := fsys.CopyFile(tc.src, tc.dst)
				if err == nil {
					t.Fatalf("CopyFile(%q, %q) = nil, want an error", tc.src, tc.dst)
				}
				if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
					t.Errorf("CopyFile(%q, %q) = %v, want %v", tc.src, tc.dst, err, tc.wantErr)
				}
				assertCopyTree(t, fsys, copyFileTree)
				if tc.wantDstAbsent {
					if _, err := fs.Stat(fsys, tc.dst); !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("Stat(%q) after a failed CopyFile = %v, want %v", tc.dst, err, fs.ErrNotExist)
					}
				}
			})
		}
	})
}

// copyFileIndependent checks that a copy is its own file: editing the
// destination may not change the source, and a copy of a copy carries the edit.
func copyFileIndependent(t *testing.T, createFSFunc func(t *testing.T) types.CopyFileFS) {
	t.Run("CopyFileIndependent", func(t *testing.T) {
		t.Parallel()
		fsys, wFS := newCopyFS(t, createFSFunc)
		if err := fsys.CopyFile("c/a.txt", "c/f.txt"); err != nil {
			t.Fatalf("CopyFile(c/a.txt, c/f.txt) = %v", err)
		}
		WriteFile(t, wFS, "c/f.txt", "edited")
		assertCopyContent(t, fsys, "c/a.txt", "a")
		assertCopyContent(t, fsys, "c/f.txt", "edited")
		if err := fsys.CopyFile("c/f.txt", "c/g.txt"); err != nil {
			t.Fatalf("CopyFile(c/f.txt, c/g.txt) = %v", err)
		}
		assertCopyContent(t, fsys, "c/g.txt", "edited")
	})
}

// copyFileLarge checks that a file larger than a copy buffer arrives whole and
// in order, which a copy that repeats, drops or reorders a buffer does not.
func copyFileLarge(t *testing.T, createFSFunc func(t *testing.T) types.CopyFileFS) {
	t.Run("CopyFileLarge", func(t *testing.T) {
		t.Parallel()
		fsys, wFS := newCopyFS(t, createFSFunc)
		content := copyFileLargeContent()
		WriteFile(t, wFS, "c/large.txt", content)
		if err := fsys.CopyFile("c/large.txt", "c/large-copy.txt"); err != nil {
			t.Fatalf("CopyFile(c/large.txt, c/large-copy.txt) = %v", err)
		}
		assertCopyContent(t, fsys, "c/large-copy.txt", content)
	})
}

// copyFileInvalidPaths checks that CopyFile refuses a path that is not valid
// for the file system, both as the source and as the destination.
func copyFileInvalidPaths(t *testing.T, createFSFunc func(t *testing.T) types.CopyFileFS) {
	t.Run("CopyFileInvalidPaths", func(t *testing.T) {
		t.Parallel()

		for _, name := range append(invalidPaths, "", "c/", "c//a.txt", "./c/a.txt") {
			label := strings.ReplaceAll(name, "/", "_")
			if label == "" {
				label = "empty"
			}
			for _, tc := range []struct {
				name     string
				src, dst string
			}{
				{name: "AsSource", src: name, dst: "c/copied.txt"},
				{name: "AsDestination", src: "c/a.txt", dst: name},
			} {
				t.Run(tc.name+"_"+label, func(t *testing.T) {
					t.Parallel()
					fsys, _ := newCopyFS(t, createFSFunc)
					err := fsys.CopyFile(tc.src, tc.dst)
					if !errors.Is(err, fs.ErrInvalid) {
						t.Errorf("CopyFile(%q, %q) = %v, want %v", tc.src, tc.dst, err, fs.ErrInvalid)
					}
					var pathErr *fs.PathError
					if !errors.As(err, &pathErr) {
						t.Errorf("CopyFile(%q, %q) = %T, want an *fs.PathError", tc.src, tc.dst, err)
					}
					assertCopyTree(t, fsys, copyFileTree)
				})
			}
		}
	})
}

// newCopyFS returns a file system from createFSFunc seeded with copyFileTree,
// which needs a file system that can write.
func newCopyFS(t *testing.T, createFSFunc func(t *testing.T) types.CopyFileFS) (types.CopyFileFS, types.WriteFS) {
	t.Helper()
	fsys := createFSFunc(t)
	if closer, ok := fsys.(io.Closer); ok {
		t.Cleanup(ufsTesting.ValidateClose(t, closer))
	}
	wFS, ok := fsys.(types.WriteFS)
	if !ok {
		t.Fatalf("%T does not implement types.WriteFS, which this suite needs to seed the tree", fsys)
	}
	for _, dir := range copyFileDirs {
		mkdirForTest(t, wFS, dir)
	}
	for name, content := range copyFileTree {
		WriteFile(t, wFS, name, content)
	}
	return fsys, wFS
}

// copyFileWant returns copyFileTree plus the entries a successful copy added.
func copyFileWant(extras map[string]string) map[string]string {
	want := maps.Clone(copyFileTree)
	for name, content := range extras {
		want[name] = content
	}
	return want
}

// assertCopyTree checks that the files under "c" and their content are want.
func assertCopyTree(t *testing.T, fsys fs.FS, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	err := fs.WalkDir(fsys, "c", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		got[name] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(c) = %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("files diff(-want,+got):\n%s", diff)
	}
}

// assertCopyContent checks the content of name, reporting where it differs
// from want rather than the whole file.
func assertCopyContent(tb testing.TB, fsys fs.FS, name, want string) {
	tb.Helper()
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		tb.Fatalf("ReadFile(%q) = %v", name, err)
	}
	if string(data) == want {
		return
	}
	if len(data) != len(want) {
		tb.Errorf("ReadFile(%q) = %d bytes, want %d bytes", name, len(data), len(want))
		return
	}
	for i := range len(want) {
		if data[i] != want[i] {
			tb.Errorf("ReadFile(%q) differs at offset %d: got %q, want %q", name, i, data[i], want[i])
			return
		}
	}
}

// copyFileLargeContent returns content whose buffers differ from each other,
// so that a copy which repeats, drops or reorders one is caught by its offset.
func copyFileLargeContent() string {
	var sb strings.Builder
	for i := range copyFileLargeBuffers {
		sb.WriteString(strings.Repeat(string(rune('a'+i)), copyFileLargeBufferSize))
	}
	return sb.String()
}
