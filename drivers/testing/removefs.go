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

// removeTree is the content each RemoveFS case starts from. It is kept below
// one directory, apart from anything a test backend is seeded with, and every
// directory holds a file, so that it also exists on bucket-backed file
// systems. r/c.txt.bak shares the prefix of the r/c.txt every case removes
// first, and r/ax shares the prefix of the r/a it refuses to remove, so an
// implementation that deletes everything whose path starts with name gets
// caught.
var removeTree = map[string]string{
	"r/a/f.txt":     "f",
	"r/a/sub/g.txt": "g",
	"r/ax/ax.txt":   "ax",
	"r/b/empty.txt": "",
	"r/b/h.txt":     "h",
	"r/c.txt":       "c",
	"r/c.txt.bak":   "bak",
}

// removeDirs are the directories removeTree needs that its files do not create
// on their own.
var removeDirs = []string{"r/a/sub", "r/ax", "r/b"}

// removeSiblingName shares the prefix of the name every case removes, so it
// must survive them all.
const removeSiblingName = "r/c.txt.bak"

// removeEmptyDir holds no file, so only a file system with an explicit
// directory structure can keep it: it is not seeded for Args.Bucket file
// systems, and the cases that remove it are not run for them.
const removeEmptyDir = "r/emptydir"

// removeInvalidNames extend the shared invalidPaths list with the names only
// Remove has to refuse: the empty name, an absolute path, a trailing
// separator, an empty element, and paths holding a dot element.
var removeInvalidNames = append(invalidPaths, "", "/", "r/", "r//c.txt", "r/./c.txt", "r/b/../c.txt", "r/c.txt/")

// removeDeletesTestFS is the minimum set of interfaces removeDeletes needs: it
// seeds removeTree with MkdirAll and Create, removes through Remove, and reads
// the result back through Stat and ReadFile.
type removeDeletesTestFS interface {
	types.SimpleFS
	types.RemoveFS
	types.MkdirAllFS
	types.CreateFileFS
	fs.ReadFileFS
	fs.StatFS
}

// removeRefusedTestFS is the minimum set of interfaces removeRefused needs: it
// seeds removeTree, asks Remove for a name that must be refused, and checks
// that removeTree is left as it was.
type removeRefusedTestFS interface {
	types.SimpleFS
	types.RemoveFS
	types.MkdirAllFS
	types.CreateFileFS
	fs.ReadFileFS
}

// removeInvalidNamesTestFS is the minimum set of interfaces
// removeInvalidNames needs: it only asks Remove for names that must be
// refused, which every file system checks before it looks anything up, so
// nothing has to be seeded or read back.
type removeInvalidNamesTestFS interface {
	types.SimpleFS
	types.RemoveFS
}

// removeReadOnlyTestFS is the minimum set of interfaces removeReadOnly needs:
// it asks a read-only file system to remove one of its own names and checks
// that it kept it, which needs a directory listing and nothing else.
type removeReadOnlyTestFS interface {
	types.SimpleFS
	types.RemoveFS
	fs.ReadDirFS
}

// removeSeededFS is the set of interfaces newRemoveFS uses to seed removeTree.
type removeSeededFS interface {
	types.SimpleFS
	types.MkdirAllFS
	types.CreateFileFS
}

// removeTreeFS is the set of interfaces assertRemoveTree uses to read a tree
// back: it walks the tree and reads every file it holds.
type removeTreeFS interface {
	fs.FS
	fs.ReadFileFS
}

// removeFS runs the [types.RemoveFS] conformance suite.
func removeFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("RemoveFS", func(t *testing.T) {
		requireType[types.RemoveFS](t, args)
		t.Parallel()
		if args.ReadOnly {
			removeReadOnly(t, requireFSTypeFunc[removeReadOnlyTestFS](t, createFSFunc))
			removeInvalidPaths(t, requireFSTypeFunc[removeInvalidNamesTestFS](t, createFSFunc))
			return
		}
		removeDeletes(t, args, requireFSTypeFunc[removeDeletesTestFS](t, createFSFunc))
		removeRefused(t, requireFSTypeFunc[removeRefusedTestFS](t, createFSFunc))
		removeInvalidPaths(t, requireFSTypeFunc[removeInvalidNamesTestFS](t, createFSFunc))
	})
}

// removeDeleteCases are the names Remove must delete, each from removeTree,
// with the files that must still be there afterwards.
var removeDeleteCases = []struct {
	name    string
	oldName string
	// want is the tree afterwards.
	want map[string]string
}{
	{
		// The sibling whose name starts with oldName has to survive, so an
		// implementation that deletes every path beginning with oldName fails
		// here rather than passing by removing one extra file.
		name:    "File",
		oldName: "r/c.txt",
		want: map[string]string{
			"r/a/f.txt":       "f",
			"r/a/sub/g.txt":   "g",
			"r/ax/ax.txt":     "ax",
			"r/b/empty.txt":   "",
			"r/b/h.txt":       "h",
			removeSiblingName: "bak",
		},
	},
	{
		// A file removing out of a subdirectory leaves the subdirectory and
		// everything else below its parent alone.
		name:    "FileFromSubDir",
		oldName: "r/a/sub/g.txt",
		want: map[string]string{
			"r/a/f.txt":       "f",
			"r/ax/ax.txt":     "ax",
			"r/b/empty.txt":   "",
			"r/b/h.txt":       "h",
			"r/c.txt":         "c",
			removeSiblingName: "bak",
		},
	},
	{
		name:    "EmptyFile",
		oldName: "r/b/empty.txt",
		want: map[string]string{
			"r/a/f.txt":       "f",
			"r/a/sub/g.txt":   "g",
			"r/ax/ax.txt":     "ax",
			"r/b/h.txt":       "h",
			"r/c.txt":         "c",
			removeSiblingName: "bak",
		},
	},
}

// removeDeletes checks that Remove deletes a file, that the name stops
// resolving afterwards, and that nothing else went with it.
func removeDeletes(t *testing.T, args Args, createFSFunc func(t *testing.T) removeDeletesTestFS) {
	t.Run("RemoveDeletes", func(t *testing.T) {
		t.Parallel()
		for _, tc := range removeDeleteCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys := newRemoveFS(t, createFSFunc)
				if err := fsys.Remove(tc.oldName); err != nil {
					t.Fatalf("Remove(%q) = %v, want nil", tc.oldName, err)
				}
				assertRemoveTree(t, fsys, tc.want)

				if _, err := fsys.Stat(tc.oldName); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("Stat(%q) after Remove = %v, want %v: the remove left %q behind", tc.oldName, err, fs.ErrNotExist, tc.oldName)
				}
				if data, err := fsys.ReadFile(tc.oldName); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("ReadFile(%q) after Remove = %q, %v, want %v: the name still reads", tc.oldName, data, err, fs.ErrNotExist)
				}
			})
		}

		// r/emptydir holds no file, and emptying r/a takes its directories with
		// it, so neither case can run on a bucket file system, which has no
		// explicit directories to delete.
		if !args.Bucket {
			t.Run("EmptyDir", func(t *testing.T) {
				t.Parallel()
				fsys := newRemoveFS(t, createFSFunc, removeEmptyDir)
				if err := fsys.Remove(removeEmptyDir); err != nil {
					t.Fatalf("Remove(%q) = %v, want nil", removeEmptyDir, err)
				}
				assertRemoveTree(t, fsys, removeTree)
				if info, err := fsys.Stat(removeEmptyDir); err == nil {
					t.Errorf("Stat(%q) after Remove = %q, want an error: the directory is still there", removeEmptyDir, info.Name())
				}
			})

			t.Run("DirAfterChildren", func(t *testing.T) {
				t.Parallel()
				// An emptied directory is an empty directory, which Remove has
				// to delete.
				fsys := newRemoveFS(t, createFSFunc)
				for _, name := range []string{"r/a/f.txt", "r/a/sub/g.txt"} {
					if err := fsys.Remove(name); err != nil {
						t.Fatalf("Remove(%q) = %v, want nil", name, err)
					}
				}
				for _, dir := range []string{"r/a/sub", "r/a"} {
					if err := fsys.Remove(dir); err != nil {
						t.Fatalf("Remove(%q) = %v, want nil: a directory emptied by Remove is not removable", dir, err)
					}
				}
				assertRemoveTree(t, fsys, map[string]string{
					"r/ax/ax.txt":     "ax",
					"r/b/empty.txt":   "",
					"r/b/h.txt":       "h",
					"r/c.txt":         "c",
					removeSiblingName: "bak",
				})
			})

			t.Run("ParentDirsSurvive", func(t *testing.T) {
				t.Parallel()
				// Removing the only file under a directory leaves that directory
				// and its parent in place.
				fsys := newRemoveFS(t, createFSFunc)
				if err := fsys.Remove("r/a/sub/g.txt"); err != nil {
					t.Fatalf("Remove(%q) = %v, want nil", "r/a/sub/g.txt", err)
				}
				for _, dir := range []string{"r/a/sub", "r/a"} {
					info, err := fsys.Stat(dir)
					if err != nil {
						t.Errorf("Stat(%q) after Remove = %v, want nil: the remove took the directory with it", dir, err)
						continue
					}
					if !info.IsDir() {
						t.Errorf("Stat(%q) after Remove is not a directory", dir)
					}
				}
			})
		}

		t.Run("Twice", func(t *testing.T) {
			t.Parallel()
			fsys := newRemoveFS(t, createFSFunc)
			if err := fsys.Remove("r/c.txt"); err != nil {
				t.Fatalf("Remove(%q) = %v, want nil", "r/c.txt", err)
			}
			if err := fsys.Remove("r/c.txt"); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Remove(%q) the second time = %v, want an error wrapping %v", "r/c.txt", err, fs.ErrNotExist)
			}
			assertRemoveTree(t, fsys, map[string]string{
				"r/a/f.txt":       "f",
				"r/a/sub/g.txt":   "g",
				"r/ax/ax.txt":     "ax",
				"r/b/empty.txt":   "",
				"r/b/h.txt":       "h",
				removeSiblingName: "bak",
			})
		})

		t.Run("ThenCreate", func(t *testing.T) {
			t.Parallel()
			// A remove that leaves a directory entry or a stale node behind
			// shows up when the same name is created again and read back.
			fsys := newRemoveFS(t, createFSFunc)
			if err := fsys.Remove("r/c.txt"); err != nil {
				t.Fatalf("Remove(%q) = %v, want nil", "r/c.txt", err)
			}
			WriteFile(t, fsys, "r/c.txt", "again")
			got, err := fsys.ReadFile("r/c.txt")
			if err != nil {
				t.Fatalf("ReadFile(%q) after Remove and Create = %v, want nil", "r/c.txt", err)
			}
			if diff := cmp.Diff("again", string(got)); diff != "" {
				t.Errorf("ReadFile(%q) after Remove and Create mismatch (-want +got):\n%s", "r/c.txt", diff)
			}
			assertRemoveTree(t, fsys, map[string]string{
				"r/a/f.txt":       "f",
				"r/a/sub/g.txt":   "g",
				"r/ax/ax.txt":     "ax",
				"r/b/empty.txt":   "",
				"r/b/h.txt":       "h",
				"r/c.txt":         "again",
				removeSiblingName: "bak",
			})
		})
	})
}

// removeSentinelCases are the names Remove must refuse with a documented
// sentinel, each from removeTree, with the error it must wrap.
var removeSentinelCases = []struct {
	name    string
	oldName string
	wantErr error
}{
	{name: "Missing", oldName: "r/missing.txt", wantErr: fs.ErrNotExist},
	{name: "MissingInSubDir", oldName: "r/a/sub/missing.txt", wantErr: fs.ErrNotExist},
}

// removeErrorCases are the names Remove must refuse without a sentinel that
// every file system reports the same way, each from removeTree.
var removeErrorCases = []struct {
	name    string
	oldName string
}{
	{name: "NonEmptyDir", oldName: "r/a"},
	{name: "NonEmptySubDir", oldName: "r/a/sub"},
	{name: "Root", oldName: "."},
	{name: "BelowFile", oldName: "r/c.txt/below"},
}

// removeRefused checks that a refused Remove leaves removeTree untouched.
//
// The sentinel for a non-empty directory is not checked: *core.memFS,
// *boltfs.boltFS and *gcsfs.gcsFS report ufserrors.ErrDirNotEmpty while
// *core.localFS reports the operating system's, which satisfies
// [fs.ErrExist] and nothing else, so the contract is still open.
//
// Removing the root is not checked for [fs.ErrPermission] either, as
// [types.RemoveFS] documents: *core.memFS and *boltfs.boltFS report it, while
// *core.localFS reports an operating system error that matches no sentinel.
// What is checked is that the root is not reported as missing, since a file
// system that gets that wrong is deleting from the wrong place.
func removeRefused(t *testing.T, createFSFunc func(t *testing.T) removeRefusedTestFS) {
	t.Run("RemoveRefused", func(t *testing.T) {
		t.Parallel()
		for _, tc := range removeSentinelCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys := newRemoveFS(t, createFSFunc)
				if err := fsys.Remove(tc.oldName); !errors.Is(err, tc.wantErr) {
					t.Errorf("Remove(%q) = %v, want an error wrapping %v", tc.oldName, err, tc.wantErr)
				}
				assertRemoveTree(t, fsys, removeTree)
			})
		}

		for _, tc := range removeErrorCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys := newRemoveFS(t, createFSFunc)
				err := fsys.Remove(tc.oldName)
				if err == nil {
					t.Fatalf("Remove(%q) = nil, want an error", tc.oldName)
				}
				if tc.name == "Root" && errors.Is(err, fs.ErrNotExist) {
					t.Errorf("Remove(%q) = %v, want an error other than %v: the root is never missing", tc.oldName, err, fs.ErrNotExist)
				}
				assertRemoveTree(t, fsys, removeTree)
			})
		}
	})
}

// removeInvalidPaths checks that Remove refuses every invalid name with an
// *fs.PathError for remove, which is what [pathutil.Validate] returns before a
// file system looks anything up.
func removeInvalidPaths(t *testing.T, createFSFunc func(t *testing.T) removeInvalidNamesTestFS) {
	t.Run("RemoveInvalidPaths", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		for _, name := range removeInvalidNames {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				err := fsys.Remove(name)
				ufsTesting.AssertInvalidPathError(t, name, err, "remove")
				// AssertInvalidPathError checks the shape of the error only, so
				// the sentinel is checked here too: a file system that looks an
				// invalid name up before validating it reports fs.ErrNotExist in
				// an *fs.PathError with the same Op and Path.
				if !errors.Is(err, fs.ErrInvalid) {
					t.Errorf("Remove(%q) = %v, want an error wrapping %v", name, err, fs.ErrInvalid)
				}
			})
		}
	})
}

// removeReadOnlyNames are the names a read-only file system is asked to
// remove, all of which exist in the content its own test creates it with.
var removeReadOnlyNames = []string{"r/c.txt", "r/a", "."}

// removeReadOnly checks Remove on a file system declared read-only through
// Args.ReadOnly: it refuses with an *fs.PathError for remove wrapping
// [fs.ErrPermission], or reports success without losing anything, as
// *core.nullFS does for every write.
func removeReadOnly(t *testing.T, createFSFunc func(t *testing.T) removeReadOnlyTestFS) {
	t.Run("RemoveReadOnly", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		before, err := fs.ReadDir(fsys, ".")
		if err != nil {
			t.Fatalf("ReadDir('.') = %v, want nil", err)
		}

		for _, name := range removeReadOnlyNames {
			err := fsys.Remove(name)
			if err == nil {
				continue
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("Remove(%q) = %T %v, want an *fs.PathError", name, err, err)
			} else if pathErr.Op != "remove" {
				t.Errorf("Remove(%q) Op = %q, want remove", name, pathErr.Op)
			}
			if !errors.Is(err, fs.ErrPermission) {
				t.Errorf("Remove(%q) = %v, want an error wrapping %v", name, err, fs.ErrPermission)
			}
		}

		after, err := fs.ReadDir(fsys, ".")
		if err != nil {
			t.Fatalf("ReadDir('.') after Remove = %v, want nil", err)
		}
		if diff := cmp.Diff(dirNames(before), dirNames(after)); diff != "" {
			t.Errorf("ReadDir('.') changed after Remove on a read-only file system (-want +got):\n%s", diff)
		}
	})
}

// dirNames returns the names in entries, for comparing one directory listing
// against another.
func dirNames(entries []fs.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

// newRemoveFS returns a file system from createFS holding removeTree, the
// directories removeDirs, and any extraDirs left empty.
func newRemoveFS[T removeSeededFS](t *testing.T, createFS func(*testing.T) T, extraDirs ...string) T {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))

	dirs := make([]string, 0, len(removeDirs)+len(extraDirs))
	dirs = append(dirs, removeDirs...)
	dirs = append(dirs, extraDirs...)
	for _, dir := range dirs {
		mkdirForTest(t, fsys, dir)
	}
	for name, content := range removeTree {
		WriteFile(t, fsys, name, content)
	}
	return fsys
}

// assertRemoveTree checks that the files of fsys and their content are want.
func assertRemoveTree(t *testing.T, fsys removeTreeFS, want map[string]string) {
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
