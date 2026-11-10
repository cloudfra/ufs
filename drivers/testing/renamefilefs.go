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
	"io/fs"
	"os"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

// renameTree is the content each RenameFileFS case starts from. It is kept below
// one directory, apart from anything a test backend is seeded with, and every
// directory holds a file, so that it also exists on bucket-backed file
// systems. r/ax shares a name prefix with the r/a the PrefixName case moves, so
// an implementation that treats a directory and everything whose path starts
// with it as one subtree gets caught.
var renameTree = map[string]string{
	"r/a/f.txt":     "f",
	"r/a/sub/g.txt": "g",
	"r/ax/ax.txt":   "ax",
	"r/b/empty.txt": "",
	"r/b/h.txt":     "h",
	"r/c.txt":       "c",
}

// renameDirs are the directories renameTree needs that its files do not create
// on their own.
var renameDirs = []string{"r/a/sub", "r/ax", "r/b"}

// renameEmptyDir holds no file, so only a file system with an explicit
// directory structure can keep it: it is not seeded for Args.Bucket file
// systems, and the cases that move onto it are not run for them.
const renameEmptyDir = "r/emptydir"

// renameRefusedPaths extend the shared invalidPaths list with the names that
// only rename has to refuse: the empty name, an absolute path, a trailing
// separator, an empty element, and paths holding a dot element.
var renameRefusedPaths = append(invalidPaths, "", "/", "r/", "r//c.txt", "r/./a2", "r/b/../a2", "r/c.txt/")

// renameMovesTestFS is the minimum set of interfaces renameMoves needs: it
// seeds renameTree with MkdirAll and Create, renames through Rename, and reads
// the result back through Stat and ReadFile.
type renameMovesTestFS interface {
	types.SimpleFS
	types.RenameFileFS
	types.MkdirAllFS
	types.CreateFileFS
	fs.ReadFileFS
	fs.StatFS
}

// renameRefusedTestFS is the minimum set of interfaces renameRefused needs: it
// seeds renameTree, asks Rename for a move that must be refused, and checks
// that renameTree is left as it was.
type renameRefusedTestFS interface {
	types.SimpleFS
	types.RenameFileFS
	types.MkdirAllFS
	types.CreateFileFS
	fs.ReadFileFS
}

// renameInvalidPathsTestFS is the minimum set of interfaces
// renameInvalidPaths needs: it only asks Rename for names that must be
// refused, which every file system checks before it looks anything up, so
// nothing has to be seeded or read back.
type renameInvalidPathsTestFS interface {
	types.SimpleFS
	types.RenameFileFS
}

// renameReadOnlyTestFS is the minimum set of interfaces renameReadOnly needs:
// it asks a read-only file system for a move that must be refused, which needs
// no read interface beyond fs.FS.
type renameReadOnlyTestFS interface {
	types.SimpleFS
	types.RenameFileFS
}

// renameSeededFS is the set of interfaces newRenameFS uses to seed renameTree.
type renameSeededFS interface {
	types.SimpleFS
	types.MkdirAllFS
	types.CreateFileFS
}

// renameTreeFS is the set of interfaces assertRenameTree uses to read a tree
// back: it walks the tree and reads every file it holds.
type renameTreeFS interface {
	fs.FS
	fs.ReadFileFS
}

// renameFileFS runs the types.RenameFileFS conformance suite.
func renameFileFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("RenameFileFS", func(t *testing.T) {
		requireType[types.RenameFileFS](t, args)
		t.Parallel()
		if args.ReadOnly {
			renameReadOnly(t, requireFSTypeFunc[renameReadOnlyTestFS](t, createFSFunc))
			renameInvalidPaths(t, requireFSTypeFunc[renameInvalidPathsTestFS](t, createFSFunc))
			return
		}
		renameMoves(t, requireFSTypeFunc[renameMovesTestFS](t, createFSFunc))
		renameRefused(t, args, requireFSTypeFunc[renameRefusedTestFS](t, createFSFunc))
		renameInvalidPaths(t, requireFSTypeFunc[renameInvalidPathsTestFS](t, createFSFunc))
	})
}

// renameMoveCases are the moves Rename must perform, each from renameTree,
// with the files and content expected afterwards.
var renameMoveCases = []struct {
	name             string
	oldPath, newPath string
	want             map[string]string
}{
	{
		name:    "File",
		oldPath: "r/c.txt",
		newPath: "r/d.txt",
		want:    map[string]string{"r/a/f.txt": "f", "r/a/sub/g.txt": "g", "r/ax/ax.txt": "ax", "r/b/empty.txt": "", "r/b/h.txt": "h", "r/d.txt": "c"},
	},
	{
		name:    "FileToOtherDir",
		oldPath: "r/c.txt",
		newPath: "r/b/c.txt",
		want:    map[string]string{"r/a/f.txt": "f", "r/a/sub/g.txt": "g", "r/ax/ax.txt": "ax", "r/b/empty.txt": "", "r/b/h.txt": "h", "r/b/c.txt": "c"},
	},
	{
		name:    "FileFromSubDir",
		oldPath: "r/a/sub/g.txt",
		newPath: "r/g.txt",
		want:    map[string]string{"r/a/f.txt": "f", "r/ax/ax.txt": "ax", "r/b/empty.txt": "", "r/b/h.txt": "h", "r/c.txt": "c", "r/g.txt": "g"},
	},
	{
		name:    "EmptyFile",
		oldPath: "r/b/empty.txt",
		newPath: "r/empty.txt",
		want:    map[string]string{"r/a/f.txt": "f", "r/a/sub/g.txt": "g", "r/ax/ax.txt": "ax", "r/b/h.txt": "h", "r/c.txt": "c", "r/empty.txt": ""},
	},
	{
		name:    "Dir",
		oldPath: "r/a",
		newPath: "r/b/a2",
		want:    map[string]string{"r/b/a2/f.txt": "f", "r/b/a2/sub/g.txt": "g", "r/ax/ax.txt": "ax", "r/b/empty.txt": "", "r/b/h.txt": "h", "r/c.txt": "c"},
	},
	{
		// A directory moving up out of its parent leaves the parent, and
		// anything else below it, where they are.
		name:    "DirUp",
		oldPath: "r/a/sub",
		newPath: "r/sub",
		want:    map[string]string{"r/a/f.txt": "f", "r/ax/ax.txt": "ax", "r/b/empty.txt": "", "r/b/h.txt": "h", "r/c.txt": "c", "r/sub/g.txt": "g"},
	},
	{
		// newPath starts with oldPath without being below it, and r/ax starts
		// with oldPath without being below it either, so an implementation that
		// moves everything whose path starts with oldPath gets the wrong name
		// for anything below r/a, and moves r/ax with it.
		name:    "PrefixName",
		oldPath: "r/a",
		newPath: "r/ab",
		want:    map[string]string{"r/ab/f.txt": "f", "r/ab/sub/g.txt": "g", "r/ax/ax.txt": "ax", "r/b/empty.txt": "", "r/b/h.txt": "h", "r/c.txt": "c"},
	},
}

// renameMoves checks that a file or a whole directory tree moves with its
// content, and that oldPath no longer resolves afterwards.
//
// Missing parent directories of newPath are not checked: *core.memFS and
// *boltfs.boltFS create them as [types.RenameFileFS] documents, while
// *core.localFS refuses the move with an error wrapping [fs.ErrNotExist], so
// the contract is still open.
func renameMoves(t *testing.T, createFSFunc func(t *testing.T) renameMovesTestFS) {
	t.Run("RenameMoves", func(t *testing.T) {
		t.Parallel()
		for _, tc := range renameMoveCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys := newRenameFS(t, createFSFunc)
				if err := fsys.Rename(tc.oldPath, tc.newPath); err != nil {
					t.Fatalf("Rename(%q, %q) = %v, want nil", tc.oldPath, tc.newPath, err)
				}
				assertRenameTree(t, fsys, tc.want)
				if _, err := fsys.Stat(tc.oldPath); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("Stat(%q) after Rename = %v, want %v: the move left oldPath behind", tc.oldPath, err, fs.ErrNotExist)
				}
			})
		}
	})
}

// renameRefusalCases are the moves Rename must refuse from renameTree, with
// the error each must wrap.
var renameRefusalCases = []struct {
	name             string
	oldPath, newPath string
	wantErr          error
}{
	{name: "MissingSource", oldPath: "r/x.txt", newPath: "r/y.txt", wantErr: fs.ErrNotExist},
	{name: "ExistingFile", oldPath: "r/c.txt", newPath: "r/a/f.txt", wantErr: fs.ErrExist},
	{name: "ExistingDir", oldPath: "r/a", newPath: "r/b", wantErr: fs.ErrExist},
	{name: "FileOverDir", oldPath: "r/c.txt", newPath: "r/b", wantErr: fs.ErrExist},
	{name: "Self", oldPath: "r/c.txt", newPath: "r/c.txt", wantErr: fs.ErrExist},
	{name: "IntoItself", oldPath: "r/a", newPath: "r/a/sub/a", wantErr: fs.ErrInvalid},
	{name: "UnderFile", oldPath: "r/c.txt", newPath: "r/c.txt/x", wantErr: fs.ErrInvalid},
	{name: "FromRoot", oldPath: ".", newPath: "r/x", wantErr: fs.ErrPermission},
	{name: "ToRoot", oldPath: "r/c.txt", newPath: ".", wantErr: fs.ErrPermission},
}

// renameEmptyDirCases are the moves onto a directory that holds no file, which
// only a file system with an explicit directory structure can be seeded with.
var renameEmptyDirCases = []struct {
	name             string
	oldPath, newPath string
	wantErr          error
}{
	{name: "OntoEmptyDir", oldPath: "r/c.txt", newPath: renameEmptyDir, wantErr: fs.ErrExist},
	{name: "DirOntoEmptyDir", oldPath: "r/a", newPath: renameEmptyDir, wantErr: fs.ErrExist},
}

// renameRefused checks that a refused move reports the documented error as an
// *os.LinkError and leaves renameTree untouched.
func renameRefused(t *testing.T, args Args, createFSFunc func(t *testing.T) renameRefusedTestFS) {
	t.Run("RenameRefused", func(t *testing.T) {
		t.Parallel()
		for _, tc := range renameRefusalCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				assertRenameRefused(t, newRenameFS(t, createFSFunc), tc.oldPath, tc.newPath, tc.wantErr)
			})
		}

		// r/emptydir holds no file, so it cannot exist on a bucket file
		// system, which has no explicit directories to move onto.
		if !args.Bucket {
			for _, tc := range renameEmptyDirCases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					assertRenameRefused(t, newRenameFS(t, createFSFunc, renameEmptyDir), tc.oldPath, tc.newPath, tc.wantErr)
				})
			}
		}
	})
}

// assertRenameRefused renames oldPath to newPath on fsys, expecting an
// *os.LinkError wrapping wantErr, and checks that renameTree is left as it was.
func assertRenameRefused(t *testing.T, fsys renameRefusedTestFS, oldPath, newPath string, wantErr error) {
	t.Helper()
	err := fsys.Rename(oldPath, newPath)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Rename(%q, %q) = %v, want an error wrapping %v", oldPath, newPath, err, wantErr)
	}
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) {
		t.Errorf("Rename(%q, %q) = %T, want an *os.LinkError", oldPath, newPath, err)
	}
	assertRenameTree(t, fsys, renameTree)
}

// renameInvalidPaths checks that Rename refuses every invalid name on either
// side with an *fs.PathError for rename, which is what
// [pathutil.ValidateRename] returns before a file system looks anything up.
func renameInvalidPaths(t *testing.T, createFSFunc func(t *testing.T) renameInvalidPathsTestFS) {
	t.Run("RenameInvalidPaths", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		for _, name := range renameRefusedPaths {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ufsTesting.AssertInvalidPathError(t, name, fsys.Rename(name, "r/d.txt"), "rename")
				ufsTesting.AssertInvalidPathError(t, name, fsys.Rename("r/c.txt", name), "rename")
			})
		}
	})
}

// renameReadOnlyCases are the moves a read-only file system must refuse, with
// the error each must wrap. Every one is caught by the path checks every file
// system runs first, so they hold without seeding anything.
var renameReadOnlyCases = []struct {
	name             string
	oldPath, newPath string
	wantErr          error
}{
	{name: "FromRoot", oldPath: ".", newPath: "r/x", wantErr: fs.ErrPermission},
	{name: "ToRoot", oldPath: "r/c.txt", newPath: ".", wantErr: fs.ErrPermission},
	{name: "IntoItself", oldPath: "r/a", newPath: "r/a/sub/a", wantErr: fs.ErrInvalid},
}

// renameReadOnly checks Rename on a file system declared read-only through
// Args.ReadOnly: it refuses a move with the documented error, or discards it.
func renameReadOnly(t *testing.T, createFSFunc func(t *testing.T) renameReadOnlyTestFS) {
	t.Run("RenameReadOnly", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		for _, tc := range renameReadOnlyCases {
			t.Run(tc.name, func(t *testing.T) {
				err := fsys.Rename(tc.oldPath, tc.newPath)
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("Rename(%q, %q) = %v, want an error wrapping %v", tc.oldPath, tc.newPath, err, tc.wantErr)
				}
				var linkErr *os.LinkError
				if !errors.As(err, &linkErr) {
					t.Errorf("Rename(%q, %q) = %T, want an *os.LinkError", tc.oldPath, tc.newPath, err)
				}
			})
		}

		// A read-only file system either refuses the move or accepts it only
		// to discard it, as *core.nullFS does, so a move between two ordinary
		// names has to fail with fs.ErrPermission or report success without
		// storing anything.
		if err := fsys.Rename("r/c.txt", "r/d.txt"); err != nil && !errors.Is(err, fs.ErrPermission) {
			t.Errorf("Rename(%q, %q) = %v, want an error wrapping %v or nil", "r/c.txt", "r/d.txt", err, fs.ErrPermission)
		}
	})
}

// newRenameFS returns a file system from createFS holding renameTree, the
// directories renameDirs, and any extraDirs left empty.
func newRenameFS[T renameSeededFS](t *testing.T, createFS func(*testing.T) T, extraDirs ...string) T {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	for _, dir := range append(renameDirs, extraDirs...) {
		mkdirForTest(t, fsys, dir)
	}
	for name, content := range renameTree {
		WriteFile(t, fsys, name, content)
	}
	return fsys
}

// assertRenameTree checks that the files of fsys and their content are want.
func assertRenameTree(t *testing.T, fsys renameTreeFS, want map[string]string) {
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
