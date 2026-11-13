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
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

// removeAllRoot is the directory every case of the RemoveAllFS suite works
// under, so that the tree check sees only the content the suite created.
const removeAllRoot = "r"

// removeAllTree is the content each RemoveAllFS case starts from. It is kept
// below one directory, apart from anything a test backend is seeded with, and
// every directory holds a file, so that it also exists on bucket-backed file
// systems. The nesting is three directories deep, so a remove that only goes
// one level down is caught. r/c.txt.bak shares the prefix of the r/c.txt some
// cases remove, and r/ax shares the prefix of the r/a others remove, so an
// implementation that deletes every path beginning with name without a
// trailing separator gets caught.
var removeAllTree = map[string]string{
	"r/a/f.txt":          "f",
	"r/a/sub/g.txt":      "g",
	"r/a/sub/deep/h.txt": "h",
	"r/ax/ax.txt":        "ax",
	"r/b/empty.txt":      "",
	"r/b/h.txt":          "h",
	"r/c.txt":            "c",
	"r/c.txt.bak":        "bak",
}

// removeAllDirs are the directories removeAllTree needs that its files do not
// create on their own.
var removeAllDirs = []string{"r/a", "r/a/sub", "r/a/sub/deep", "r/ax", "r/b"}

// removeAllSiblingName shares the prefix of the name some cases remove, so it
// must survive them all.
const removeAllSiblingName = "r/c.txt.bak"

// removeAllEmptyDir holds no file, so only a file system with an explicit
// directory structure can keep it: it is not seeded for Args.Bucket file
// systems, and the case that removes it is not run for them.
const removeAllEmptyDir = "r/emptydir"

// removeAllInvalidNames extend the shared invalidPaths list with the invalid
// spellings written against the tree removeAllSeeds seeds.
var removeAllInvalidNames = append(invalidPaths, "r/", "r//c.txt", "r/./c.txt", "r/b/../c.txt", "r/c.txt/")

// removeAllMissingNames are the names no file system holds. RemoveAll is a
// no-op that returns nil for each of them: a name below a directory that does
// not exist, and a name below a file that does.
var removeAllMissingNames = []string{
	"r/missing.txt",
	"r/a/sub/missing.txt",
	"r/nope/deeper/name.txt",
}

// removeAllDeletesTestFS is the minimum set of interfaces removeAllDeletes
// needs: it seeds removeAllTree with MkdirAll and Create, removes through
// RemoveAll, and reads the result back through Stat and ReadFile.
type removeAllDeletesTestFS interface {
	types.SimpleFS
	types.RemoveAllFS
	types.MkdirAllFS
	types.CreateFileFS
	fs.ReadFileFS
	fs.StatFS
}

// removeAllNoOpTestFS is the minimum set of interfaces removeAllNoOps needs: it
// seeds removeAllTree, asks RemoveAll for names that are not there, and checks
// that removeAllTree is left as it was.
type removeAllNoOpTestFS interface {
	types.SimpleFS
	types.RemoveAllFS
	types.MkdirAllFS
	types.CreateFileFS
	fs.ReadFileFS
}

// removeAllInvalidNamesTestFS is the minimum set of interfaces
// removeAllInvalidPaths needs: it only asks RemoveAll for names that must be
// refused, which every file system checks before it looks anything up, so
// nothing has to be seeded or read back.
type removeAllInvalidNamesTestFS interface {
	types.SimpleFS
	types.RemoveAllFS
}

// removeAllReadOnlyTestFS is the minimum set of interfaces removeAllReadOnly
// needs: it asks a read-only file system to remove one of its own names and
// checks that it kept it, which needs a directory listing and nothing else.
type removeAllReadOnlyTestFS interface {
	types.SimpleFS
	types.RemoveAllFS
	fs.ReadDirFS
}

// removeAllSeededFS is the set of interfaces newRemoveAllFS uses to seed
// removeAllTree.
type removeAllSeededFS interface {
	types.SimpleFS
	types.MkdirAllFS
	types.CreateFileFS
}

// removeAllTreeFS is the set of interfaces assertRemoveAllTree uses to read a
// tree back: it walks the tree and reads every file it holds.
type removeAllTreeFS interface {
	fs.FS
	fs.ReadFileFS
}

// removeAllFS runs the [types.RemoveAllFS] conformance suite.
func removeAllFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("RemoveAllFS", func(t *testing.T) {
		requireType[types.RemoveAllFS](t, args)
		t.Parallel()
		if args.ReadOnly {
			removeAllReadOnly(t, requireFSTypeFunc[removeAllReadOnlyTestFS](t, createFSFunc))
			removeAllInvalidPaths(t, requireFSTypeFunc[removeAllInvalidNamesTestFS](t, createFSFunc))
			return
		}
		removeAllDeletes(t, args, requireFSTypeFunc[removeAllDeletesTestFS](t, createFSFunc))
		removeAllNoOps(t, requireFSTypeFunc[removeAllNoOpTestFS](t, createFSFunc))
		removeAllInvalidPaths(t, requireFSTypeFunc[removeAllInvalidNamesTestFS](t, createFSFunc))
	})
}

// removeAllDeleteCases are the names RemoveAll must delete and everything that
// must still be there afterwards, each from removeAllTree.
var removeAllDeleteCases = []struct {
	name    string
	oldName string
	// want is the tree afterwards.
	want map[string]string
	// gone are names at or below oldName that must stop resolving.
	gone []string
}{
	{
		// The directory holds a file, a subdirectory, and a file two levels
		// down, all of which go. The directory whose name starts with oldName
		// has to survive, so an implementation that deletes every path
		// beginning with oldName fails here.
		name:    "SubTree",
		oldName: "r/a",
		want: map[string]string{
			"r/ax/ax.txt":        "ax",
			"r/b/empty.txt":      "",
			"r/b/h.txt":          "h",
			"r/c.txt":            "c",
			removeAllSiblingName: "bak",
		},
		gone: []string{"r/a", "r/a/f.txt", "r/a/sub", "r/a/sub/g.txt", "r/a/sub/deep", "r/a/sub/deep/h.txt"},
	},
	{
		// Removing a directory one level down leaves the file in its parent
		// and everything else alone.
		name:    "SubDir",
		oldName: "r/a/sub",
		want: map[string]string{
			"r/a/f.txt":          "f",
			"r/ax/ax.txt":        "ax",
			"r/b/empty.txt":      "",
			"r/b/h.txt":          "h",
			"r/c.txt":            "c",
			removeAllSiblingName: "bak",
		},
		gone: []string{"r/a/sub", "r/a/sub/g.txt", "r/a/sub/deep", "r/a/sub/deep/h.txt"},
	},
	{
		name:    "DeepestDir",
		oldName: "r/a/sub/deep",
		want: map[string]string{
			"r/a/f.txt":          "f",
			"r/a/sub/g.txt":      "g",
			"r/ax/ax.txt":        "ax",
			"r/b/empty.txt":      "",
			"r/b/h.txt":          "h",
			"r/c.txt":            "c",
			removeAllSiblingName: "bak",
		},
		gone: []string{"r/a/sub/deep", "r/a/sub/deep/h.txt"},
	},
	{
		name:    "DirOfFiles",
		oldName: "r/b",
		want: map[string]string{
			"r/a/f.txt":          "f",
			"r/a/sub/g.txt":      "g",
			"r/a/sub/deep/h.txt": "h",
			"r/ax/ax.txt":        "ax",
			"r/c.txt":            "c",
			removeAllSiblingName: "bak",
		},
		gone: []string{"r/b", "r/b/empty.txt", "r/b/h.txt"},
	},
	{
		// The sibling whose name starts with oldName has to survive, so an
		// implementation that deletes every path beginning with oldName fails
		// here rather than passing by removing one extra file.
		name:    "File",
		oldName: "r/c.txt",
		want: map[string]string{
			"r/a/f.txt":          "f",
			"r/a/sub/g.txt":      "g",
			"r/a/sub/deep/h.txt": "h",
			"r/ax/ax.txt":        "ax",
			"r/b/empty.txt":      "",
			"r/b/h.txt":          "h",
			removeAllSiblingName: "bak",
		},
		gone: []string{"r/c.txt"},
	},
	{
		name:    "EmptyFile",
		oldName: "r/b/empty.txt",
		want: map[string]string{
			"r/a/f.txt":          "f",
			"r/a/sub/g.txt":      "g",
			"r/a/sub/deep/h.txt": "h",
			"r/ax/ax.txt":        "ax",
			"r/b/h.txt":          "h",
			"r/c.txt":            "c",
			removeAllSiblingName: "bak",
		},
		gone: []string{"r/b/empty.txt"},
	},
	{
		// Everything the suite created sits below removeAllRoot, so this
		// leaves the file system with nothing to walk.
		name:    "TopDir",
		oldName: removeAllRoot,
		want:    map[string]string{},
		gone:    []string{removeAllRoot, "r/a", "r/a/f.txt", "r/ax", "r/c.txt"},
	},
}

// removeAllDeletes checks that RemoveAll deletes a name and everything below
// it, that the names stop resolving afterwards, and that nothing else went
// with them.
//
// Removing the root is not run: [types.RemoveAllFS] does not say what happens
// to it, and the file systems differ. *core.memFS and *boltfs.boltFS empty it
// and report success, *core.localFS reports an operating system error that
// matches no sentinel, and *core.nullFS reports success without doing
// anything.
func removeAllDeletes(t *testing.T, args Args, createFSFunc func(t *testing.T) removeAllDeletesTestFS) {
	t.Run("RemoveAllDeletes", func(t *testing.T) {
		t.Parallel()
		for _, tc := range removeAllDeleteCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys := newRemoveAllFS(t, createFSFunc)
				if err := fsys.RemoveAll(tc.oldName); err != nil {
					t.Fatalf("RemoveAll(%q) = %v, want nil", tc.oldName, err)
				}
				assertRemoveAllTree(t, fsys, tc.want)

				for _, name := range tc.gone {
					if _, err := fsys.Stat(name); !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("Stat(%q) after RemoveAll(%q) = %v, want %v: the remove left %q behind", name, tc.oldName, err, fs.ErrNotExist, name)
					}
				}
			})
		}

		// r/emptydir holds no file, so a bucket file system, which has no
		// explicit directory structure, cannot keep it.
		if !args.Bucket {
			t.Run("EmptyDir", func(t *testing.T) {
				t.Parallel()
				fsys := newRemoveAllFS(t, createFSFunc, removeAllEmptyDir)
				if err := fsys.RemoveAll(removeAllEmptyDir); err != nil {
					t.Fatalf("RemoveAll(%q) = %v, want nil", removeAllEmptyDir, err)
				}
				assertRemoveAllTree(t, fsys, removeAllTree)
				if info, err := fsys.Stat(removeAllEmptyDir); err == nil {
					t.Errorf("Stat(%q) after RemoveAll = %q, want an error: the directory is still there", removeAllEmptyDir, info.Name())
				}
			})
		}

		t.Run("ParentDirsSurvive", func(t *testing.T) {
			t.Parallel()
			// RemoveAll takes the named directory with it, not the directories
			// above it.
			fsys := newRemoveAllFS(t, createFSFunc)
			if err := fsys.RemoveAll("r/a/sub"); err != nil {
				t.Fatalf("RemoveAll(%q) = %v, want nil", "r/a/sub", err)
			}
			for _, dir := range []string{removeAllRoot, "r/a"} {
				info, err := fsys.Stat(dir)
				if err != nil {
					t.Errorf("Stat(%q) after RemoveAll = %v, want nil: the remove took the parent with it", dir, err)
					continue
				}
				if !info.IsDir() {
					t.Errorf("Stat(%q) after RemoveAll is not a directory", dir)
				}
			}
		})

		t.Run("ThenCreate", func(t *testing.T) {
			t.Parallel()
			// A remove that leaves a directory entry or a stale node behind
			// shows up when the same names are created again and read back.
			fsys := newRemoveAllFS(t, createFSFunc)
			if err := fsys.RemoveAll("r/a"); err != nil {
				t.Fatalf("RemoveAll(%q) = %v, want nil", "r/a", err)
			}
			mkdirForTest(t, fsys, "r/a/sub")
			WriteFile(t, fsys, "r/a/sub/new.txt", "again")
			got, err := fsys.ReadFile("r/a/sub/new.txt")
			if err != nil {
				t.Fatalf("ReadFile(%q) after RemoveAll and Create = %v, want nil", "r/a/sub/new.txt", err)
			}
			if diff := cmp.Diff("again", string(got)); diff != "" {
				t.Errorf("ReadFile(%q) after RemoveAll and Create mismatch (-want +got):\n%s", "r/a/sub/new.txt", diff)
			}
			assertRemoveAllTree(t, fsys, map[string]string{
				"r/a/sub/new.txt":    "again",
				"r/ax/ax.txt":        "ax",
				"r/b/empty.txt":      "",
				"r/b/h.txt":          "h",
				"r/c.txt":            "c",
				removeAllSiblingName: "bak",
			})
		})
	})
}

// removeAllNoOps checks the no-op half of the [types.RemoveAllFS] contract: a
// name that does not exist is reported as success, and a remove that cannot
// find anything takes nothing with it.
//
// The name below a file is not checked for an error: *core.memFS,
// *boltfs.boltFS and *gcsfs.gcsFS treat it as another name that does not
// exist, while *core.localFS reports an operating system error that matches no
// sentinel. What is checked is that nothing was removed.
func removeAllNoOps(t *testing.T, createFSFunc func(t *testing.T) removeAllNoOpTestFS) {
	t.Run("RemoveAllNoOps", func(t *testing.T) {
		t.Parallel()
		for _, name := range removeAllMissingNames {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				fsys := newRemoveAllFS(t, createFSFunc)
				if err := fsys.RemoveAll(name); err != nil {
					t.Errorf("RemoveAll(%q) = %v, want nil: a name that does not exist is a no-op", name, err)
				}
				assertRemoveAllTree(t, fsys, removeAllTree)
			})
		}

		t.Run("BelowFile", func(t *testing.T) {
			t.Parallel()
			fsys := newRemoveAllFS(t, createFSFunc)
			if err := fsys.RemoveAll("r/c.txt/below"); err != nil {
				t.Logf("RemoveAll(%q) = %v", "r/c.txt/below", err)
			}
			assertRemoveAllTree(t, fsys, removeAllTree)
		})

		t.Run("Twice", func(t *testing.T) {
			t.Parallel()
			fsys := newRemoveAllFS(t, createFSFunc)
			if err := fsys.RemoveAll("r/a"); err != nil {
				t.Fatalf("RemoveAll(%q) = %v, want nil", "r/a", err)
			}
			if err := fsys.RemoveAll("r/a"); err != nil {
				t.Errorf("RemoveAll(%q) the second time = %v, want nil: the second remove is a no-op", "r/a", err)
			}
			assertRemoveAllTree(t, fsys, map[string]string{
				"r/ax/ax.txt":        "ax",
				"r/b/empty.txt":      "",
				"r/b/h.txt":          "h",
				"r/c.txt":            "c",
				removeAllSiblingName: "bak",
			})
		})
	})
}

// removeAllInvalidPaths checks that RemoveAll refuses every invalid name with
// an *fs.PathError for removeall, which is what [pathutil.Validate] returns
// before a file system looks anything up.
func removeAllInvalidPaths(t *testing.T, createFSFunc func(t *testing.T) removeAllInvalidNamesTestFS) {
	t.Run("RemoveAllInvalidPaths", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		for _, name := range removeAllInvalidNames {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				err := fsys.RemoveAll(name)
				ufsTesting.AssertInvalidPathError(t, name, err, "removeall")
				// AssertInvalidPathError checks the shape of the error only, so
				// the sentinel is checked here too: a file system that walks an
				// invalid name before validating it reports the same Op and Path
				// for a name that is simply not there.
				if !errors.Is(err, fs.ErrInvalid) {
					t.Errorf("RemoveAll(%q) = %v, want an error wrapping %v", name, err, fs.ErrInvalid)
				}
			})
		}
	})
}

// removeAllReadOnlyNames are the names a read-only file system is asked to
// remove, all of which exist in the content its own test creates it with.
var removeAllReadOnlyNames = []string{"r/c.txt", "r/a", "."}

// removeAllReadOnly checks RemoveAll on a file system declared read-only
// through Args.ReadOnly: it refuses with an *fs.PathError for removeall
// wrapping [fs.ErrPermission], or reports success without losing anything, as
// *core.nullFS does for every write.
func removeAllReadOnly(t *testing.T, createFSFunc func(t *testing.T) removeAllReadOnlyTestFS) {
	t.Run("RemoveAllReadOnly", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		listing := func() ([]string, error) {
			entries, err := fs.ReadDir(fsys, ".")
			if err != nil {
				return nil, err
			}
			names := make([]string, len(entries))
			for i, entry := range entries {
				names[i] = entry.Name()
			}
			return names, nil
		}

		before, err := listing()
		if err != nil {
			t.Fatalf("ReadDir('.') = %v, want nil", err)
		}

		for _, name := range removeAllReadOnlyNames {
			err := fsys.RemoveAll(name)
			if err == nil {
				continue
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("RemoveAll(%q) = %T %v, want an *fs.PathError", name, err, err)
			} else if pathErr.Op != "removeall" {
				t.Errorf("RemoveAll(%q) Op = %q, want removeall", name, pathErr.Op)
			}
			if !errors.Is(err, fs.ErrPermission) {
				t.Errorf("RemoveAll(%q) = %v, want an error wrapping %v", name, err, fs.ErrPermission)
			}
		}

		after, err := listing()
		if err != nil {
			t.Fatalf("ReadDir('.') after RemoveAll = %v, want nil", err)
		}
		if diff := cmp.Diff(before, after); diff != "" {
			t.Errorf("ReadDir('.') changed after RemoveAll on a read-only file system (-want +got):\n%s", diff)
		}
	})
}

// newRemoveAllFS returns a file system from createFS holding removeAllTree, the
// directories removeAllDirs, and any extraDirs left empty.
func newRemoveAllFS[T removeAllSeededFS](t *testing.T, createFS func(*testing.T) T, extraDirs ...string) T {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))

	dirs := make([]string, 0, len(removeAllDirs)+len(extraDirs))
	dirs = append(dirs, removeAllDirs...)
	dirs = append(dirs, extraDirs...)
	for _, dir := range dirs {
		mkdirForTest(t, fsys, dir)
	}
	for name, content := range removeAllTree {
		WriteFile(t, fsys, name, content)
	}
	return fsys
}

// assertRemoveAllTree checks that the files of fsys below removeAllRoot and
// their content are want.
func assertRemoveAllTree(t *testing.T, fsys removeAllTreeFS, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	err := fs.WalkDir(fsys, removeAllRoot, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			// A nil DirEntry comes only from the root itself, which is gone
			// entirely in the case that removes removeAllRoot.
			if d == nil && name == removeAllRoot && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fsys.ReadFile(name)
		if err != nil {
			return err
		}
		got[name] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%q) = %v", removeAllRoot, err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("files diff(-want,+got):\n%s", diff)
	}
}
