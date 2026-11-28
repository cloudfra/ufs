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
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

// chtimesTree is the content each ChtimesFS case starts from. It is kept below
// one directory, apart from anything a test backend is seeded with, and every
// directory holds a file, so that it also exists on bucket-backed file
// systems.
var chtimesTree = map[string]string{
	"t/dir/file.txt": "inside a directory",
	"t/file.txt":     "at the top",
}

// chtimesDirs are the directories chtimesTree needs that its files do not
// create on their own.
var chtimesDirs = []string{"t/dir"}

// chtimesOld and chtimesNew are the two times the cases ask for. Each is a
// whole second, because [types.ChtimesFS] leaves a backend free to round to the
// second and a whole second is then read back exactly, and each is far in the
// past, because the time a seeded node carries is the time the seeding ran.
var (
	chtimesOld = time.Date(2001, time.February, 3, 4, 5, 6, 0, time.UTC)
	chtimesNew = time.Date(2003, time.April, 5, 6, 7, 8, 0, time.UTC)
)

// chtimesNodeCases are the nodes of chtimesTree a case may be asked to change
// the time of: a file at the top of the tree, a directory, and a file below a
// directory.
var chtimesNodeCases = []struct {
	name string
	path string
}{
	{name: "File", path: "t/file.txt"},
	{name: "Dir", path: "t/dir"},
	{name: "FileInDir", path: "t/dir/file.txt"},
}

// chtimesWriteTestFS is the set of interfaces the cases that change a time
// need: they seed chtimesTree with MkdirAll and Create, change a time through
// Chtimes, and read the node back through Stat, with ReadFile telling whether
// the call left the content of what it touched alone.
type chtimesWriteTestFS interface {
	types.SimpleFS
	types.ChtimesFS
	types.MkdirAllFS
	types.CreateFileFS
	fs.StatFS
	fs.ReadFileFS
}

// chtimesRefusedTestFS is the set of interfaces the refused cases need. It is
// chtimesWriteTestFS without StatFS because a refused call has no result to
// read, but it keeps ReadFile: a refusal is only worth believing once the tree
// it was supposed to leave alone has been read back.
type chtimesRefusedTestFS interface {
	types.SimpleFS
	types.ChtimesFS
	types.MkdirAllFS
	types.CreateFileFS
	fs.ReadFileFS
}

// chtimesClosedTestFS is the set of interfaces the closed case needs: it seeds
// nothing, because a closed file system is asked nothing, and reads nothing
// back, because the error is the whole assertion.
type chtimesClosedTestFS interface {
	types.SimpleFS
	types.ChtimesFS
}

// chtimesInvalidPathsTestFS is the set of interfaces chtimesInvalidPaths
// needs: it only asks Chtimes for names that must be refused, which every file
// system checks before it looks anything up, so nothing has to be seeded or
// read back.
type chtimesInvalidPathsTestFS interface {
	types.SimpleFS
	types.ChtimesFS
}

// chtimesReadOnlyTestFS is the set of interfaces chtimesReadOnly needs: it asks
// a read-only file system to change a time, which needs no read interface
// beyond [fs.FS].
type chtimesReadOnlyTestFS interface {
	types.SimpleFS
	types.ChtimesFS
}

// chtimesSeededFS is the set of interfaces newChtimesFS uses to seed
// chtimesTree.
type chtimesSeededFS interface {
	types.SimpleFS
	types.MkdirAllFS
	types.CreateFileFS
}

// chtimesTreeFS is the set of interfaces assertChtimesTree uses to read a tree
// back: it walks the tree and reads every file it holds.
type chtimesTreeFS interface {
	fs.FS
	fs.ReadFileFS
}

// ChtimesFS runs the [types.ChtimesFS] conformance suite, and skips everything
// when the file system does not answer that interface, which [types.WriteFS]
// does not ask for.
//
// Every case calls the method the suite has just asserted rather than going
// through [polyfills.Chtimes]: what is under test is what a file system does
// with the times it is handed, and the polyfill has its own tests for the part
// it decides, which is whether to call the file system at all.
//
// What is out of reach for these fixtures is named here rather than skipped
// quietly:
//
//   - The access time is compared only where Stat hands back the platform's own
//     stat structure, which is the only way to read an access time at all
//     through io/fs. *core.memFS stores no access time, so among the read-write
//     fixtures only a local file system is judged on it.
//   - A file system declared read-only through [Args.ReadOnly] cannot be seeded
//     through itself, so its success path cannot be exercised: the read-only
//     cases judge only that a change is refused. A driver test that wants a
//     changed time read back has to seed the file system underneath, which is
//     what the [types.ReleaseFS] suite does with [SeedReleaseFS].
//   - Whether the times read back keep sub-second precision is not a check,
//     because [types.ChtimesFS] leaves that to the backend. Every case asks for
//     whole seconds so that the cases which do compare a time are not asking a
//     file system to be more precise than it promises.
func chtimesFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("ChtimesFS", func(t *testing.T) {
		requireType[types.ChtimesFS](t, args)
		t.Parallel()
		if args.ReadOnly {
			chtimesReadOnly(t, requireFSTypeFunc[chtimesReadOnlyTestFS](t, createFSFunc))
			chtimesInvalidPaths(t, requireFSTypeFunc[chtimesInvalidPathsTestFS](t, createFSFunc))
			return
		}
		chtimesSetsTime(t, requireFSTypeFunc[chtimesWriteTestFS](t, createFSFunc))
		chtimesZeroLeavesAlone(t, requireFSTypeFunc[chtimesWriteTestFS](t, createFSFunc))
		chtimesAccessTime(t, requireFSTypeFunc[chtimesWriteTestFS](t, createFSFunc))
		chtimesRefused(t, requireFSTypeFunc[chtimesRefusedTestFS](t, createFSFunc))
		chtimesClosed(t, requireFSTypeFunc[chtimesClosedTestFS](t, createFSFunc))
		chtimesInvalidPaths(t, requireFSTypeFunc[chtimesInvalidPathsTestFS](t, createFSFunc))
	})
}

// chtimesSetsTime requires Chtimes to report the modification time it was asked
// for, and to leave the content of the node alone. The access time is handed a
// zero value, so this is also the case that catches a file system that treats a
// time it was not given as one to erase.
func chtimesSetsTime(t *testing.T, createFSFunc func(t *testing.T) chtimesWriteTestFS) {
	t.Run("ChtimesSetsTime", func(t *testing.T) {
		t.Parallel()
		for _, tc := range chtimesNodeCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys := newChtimesFS(t, createFSFunc)
				if err := fsys.Chtimes(tc.path, time.Time{}, chtimesOld); err != nil {
					t.Fatalf("Chtimes(%q, zero, %s) = %v, want nil", tc.path, chtimesOld, err)
				}
				assertChtimesModTime(t, fsys, tc.path, chtimesOld)
				assertChtimesTree(t, fsys, chtimesTree)
			})
		}
	})
}

// chtimesZeroLeavesAlone requires a zero time to leave the time it stands for
// where it was, which is what lets a caller change one of a node's two times:
// the modification time set by the first call has to survive a second call that
// names only an access time. A file system that read the zero as the epoch
// reports nothing but the epoch here.
func chtimesZeroLeavesAlone(t *testing.T, createFSFunc func(t *testing.T) chtimesWriteTestFS) {
	t.Run("ChtimesZeroLeavesAlone", func(t *testing.T) {
		t.Parallel()
		for _, tc := range chtimesNodeCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys := newChtimesFS(t, createFSFunc)
				if err := fsys.Chtimes(tc.path, time.Time{}, chtimesOld); err != nil {
					t.Fatalf("Chtimes(%q, zero, %s) = %v, want nil", tc.path, chtimesOld, err)
				}
				if err := fsys.Chtimes(tc.path, chtimesNew, time.Time{}); err != nil {
					t.Fatalf("Chtimes(%q, %s, zero) = %v, want nil", tc.path, chtimesNew, err)
				}
				assertChtimesModTime(t, fsys, tc.path, chtimesOld)
			})
		}
	})
}

// chtimesAccessTime requires the access time to be the one that was asked for
// where the file system reports an access time at all. [fileAtime] is what says
// whether this fixture can read one back, and [types.ChtimesFS] leaves a
// backend that tracks no access time free to leave it untouched, so a fixture
// that cannot see one reports that rather than passing silently.
func chtimesAccessTime(t *testing.T, createFSFunc func(t *testing.T) chtimesWriteTestFS) {
	t.Run("ChtimesAccessTime", func(t *testing.T) {
		t.Parallel()
		for _, tc := range chtimesNodeCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys := newChtimesFS(t, createFSFunc)
				if err := fsys.Chtimes(tc.path, chtimesNew, chtimesOld); err != nil {
					t.Fatalf("Chtimes(%q, %s, %s) = %v, want nil", tc.path, chtimesNew, chtimesOld, err)
				}
				info, err := fsys.Stat(tc.path)
				if err != nil {
					t.Fatalf("Stat(%q) = %v, want nil", tc.path, err)
				}
				atime, ok := fileAtime(info)
				if !ok {
					t.Skipf("Stat(%q) reports no platform stat structure, so this fixture cannot read an access time back", tc.path)
				}
				if !atime.Equal(chtimesNew) {
					t.Errorf("Stat(%q) access time = %s, want %s", tc.path, atime.Format(time.RFC3339Nano), chtimesNew.Format(time.RFC3339Nano))
				}
			})
		}
	})
}

// chtimesMissingCases are names that the seeded tree does not hold. Each is
// refused with an error wrapping [fs.ErrNotExist], which is what
// [types.ChtimesFS] says a name that does not exist returns, whatever the
// backend calls the lookup that failed.
var chtimesMissingCases = []string{
	"t/missing.txt",
	"t/missing",
	"t/missing/file.txt",
	"t/dir/missing.txt",
}

// chtimesConflictCases are names that run into a regular file on the way down,
// where the lookup needed a directory. Which error that is stays open here for
// the same reason dirFileConflictCases leaves it open: a store that keeps its
// nodes by full path, as *core.memFS does, cannot tell a name below a regular
// file from a name that is nowhere at all, while a kernel-backed file system
// reports the OS's ENOTDIR. What is settled is that the call fails, reports an
// *fs.PathError, and changes nothing.
var chtimesConflictCases = []string{
	"t/file.txt/below",
	"t/file.txt/below/deeper",
}

// chtimesRefused checks that Chtimes refuses a name it cannot change and stops
// there, leaving chtimesTree and its times as they were.
func chtimesRefused(t *testing.T, createFSFunc func(t *testing.T) chtimesRefusedTestFS) {
	t.Run("ChtimesRefused", func(t *testing.T) {
		t.Parallel()
		for _, name := range chtimesMissingCases {
			t.Run("missing_"+pathLabel(name), func(t *testing.T) {
				t.Parallel()
				fsys := newChtimesFS(t, createFSFunc)
				err := fsys.Chtimes(name, time.Time{}, chtimesOld)
				if !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("Chtimes(%q, zero, %s) = %v, want an error wrapping %v", name, chtimesOld, err, fs.ErrNotExist)
				}
				assertChtimesPathError(t, name, err)
				assertChtimesTree(t, fsys, chtimesTree)
			})
		}
		for _, name := range chtimesConflictCases {
			t.Run("conflict_"+pathLabel(name), func(t *testing.T) {
				t.Parallel()
				fsys := newChtimesFS(t, createFSFunc)
				err := fsys.Chtimes(name, time.Time{}, chtimesOld)
				if err == nil {
					t.Errorf("Chtimes(%q, zero, %s) = nil, want an error: name is below a regular file", name, chtimesOld)
				}
				assertChtimesPathError(t, name, err)
				assertChtimesTree(t, fsys, chtimesTree)
			})
		}
	})
}

// chtimesClosed requires Chtimes on a closed file system to report an error
// wrapping [fs.ErrClosed], which is what every other write operation in these
// suites is required to do: a closed file system has not lost the times of its
// nodes, and reporting success would be reporting a change to nothing.
func chtimesClosed(t *testing.T, createFSFunc func(t *testing.T) chtimesClosedTestFS) {
	t.Run("ChtimesClosed", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		if err := fsys.Close(); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		err := fsys.Chtimes("t/file.txt", time.Time{}, chtimesOld)
		if !errors.Is(err, fs.ErrClosed) {
			t.Errorf("Chtimes() on a closed file system = %v, want an error wrapping %v", err, fs.ErrClosed)
		}
		assertChtimesPathError(t, "t/file.txt", err)
	})
}

// chtimesInvalidPaths checks that Chtimes refuses every invalid name with an
// *fs.PathError for chtimes, which is what [pathutil.Validate] returns before a
// file system looks anything up.
func chtimesInvalidPaths(t *testing.T, createFSFunc func(t *testing.T) chtimesInvalidPathsTestFS) {
	t.Run("ChtimesInvalidPaths", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		for _, name := range invalidPaths {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ufsTesting.AssertInvalidPathError(t, name, fsys.Chtimes(name, time.Time{}, chtimesOld), "chtimes")
			})
		}
	})
}

// chtimesReadOnly requires a file system declared read-only through
// [Args.ReadOnly] to refuse the change rather than report one it did not make.
//
// Which error it refuses with is not pinned the way renameReadOnly pins
// [fs.ErrPermission]. Every read-only file system these suites run against is a
// decorator, so the refusal comes from the file system underneath, which answers
// [fs.ErrPermission] once it has a Chtimes of its own to be pointed at and an
// unimplemented error while it does not. Success is the one answer no file
// system is offered.
func chtimesReadOnly(t *testing.T, createFSFunc func(t *testing.T) chtimesReadOnlyTestFS) {
	t.Run("ChtimesReadOnly", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		for _, tc := range chtimesNodeCases {
			t.Run(tc.name, func(t *testing.T) {
				if err := fsys.Chtimes(tc.path, time.Time{}, chtimesOld); err == nil {
					t.Errorf("Chtimes(%q, zero, %s) = nil on a read-only file system, want an error", tc.path, chtimesOld)
				}
			})
		}
	})
}

// newChtimesFS returns a file system from createFS holding chtimesTree and the
// directories chtimesDirs.
func newChtimesFS[T chtimesSeededFS](t *testing.T, createFS func(*testing.T) T) T {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	mkdirForTest(t, fsys, chtimesDirs...)
	for name, content := range chtimesTree {
		WriteFile(t, fsys, name, content)
	}
	return fsys
}

// assertChtimesModTime reads name back from fsys and requires the modification
// time it reports to be want. The time it reports is printed to nanosecond
// precision so that a backend rounding to the second is distinguishable from one
// storing a different time.
func assertChtimesModTime(t *testing.T, fsys fs.StatFS, name string, want time.Time) {
	t.Helper()
	info, err := fsys.Stat(name)
	if err != nil {
		t.Fatalf("Stat(%q) = %v, want nil", name, err)
	}
	if got := info.ModTime(); !got.Equal(want) {
		t.Errorf("Stat(%q).ModTime() = %s after Chtimes, want %s", name,
			got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

// assertChtimesPathError requires err to carry an *fs.PathError. The Op it
// names is not checked, because the failed lookup belongs to the file system:
// *core.memFS names the ufs verb while a local file system names the syscall
// its platform ran, "chtimesat" on Linux. The op is pinned only for the
// invalid names, which every file system refuses with its own check first.
func assertChtimesPathError(t *testing.T, name string, err error) {
	t.Helper()
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Errorf("Chtimes(%q) = %T, want an *fs.PathError", name, err)
	}
}

// assertChtimesTree checks that the files of fsys and their content are want.
func assertChtimesTree(t *testing.T, fsys chtimesTreeFS, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	err := fs.WalkDir(fsys, "t", func(name string, d fs.DirEntry, err error) error {
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
