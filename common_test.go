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
	"context"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs/internal/pathutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

type fsTestCase struct {
	name       string
	createFS   func(tb testing.TB) WriteFS
	wantString string
}

var (
	readWriteFSTestCaseList = []fsTestCase{
		{
			name: "localFS",
			createFS: func(tb testing.TB) WriteFS {
				dir := tb.TempDir()
				fsys, err := newLocalFS(tb.Context(), dir)
				if err != nil {
					tb.Fatalf("cannot create localFS file system, %s", err)
				}
				tb.Cleanup(func() {
					if err := fsys.Close(); err != nil {
						tb.Errorf("Close() = %v", err)
					}
				})
				return fsys
			},
			wantString: "file://" + pathutil.TempDir(),
		},
		{
			name: "tempMountFS",
			createFS: func(tb testing.TB) WriteFS {
				fsys, err := newTempMountFS(tb.Context(), "test://", func(string) error { return nil })
				if err != nil {
					tb.Fatalf("cannot create tempMountFS file system, %s", err)
				}
				tb.Cleanup(func() {
					if err := fsys.Close(); err != nil {
						tb.Errorf("Close() = %v", err)
					}
				})
				return fsys
			},
			wantString: "test:",
		},
		{
			name: "memFS",
			createFS: func(tb testing.TB) WriteFS {
				fsys := makeMemFS(memFSPrefix)
				tb.Cleanup(func() {
					if err := fsys.Close(); err != nil {
						tb.Errorf("Close() = %v", err)
					}
				})
				return fsys
			},
			wantString: memFSPrefix,
		},
	}

	readOnlyFSTestCaseList = []fsTestCase{
		{
			name: "nullFS",
			createFS: func(tb testing.TB) WriteFS {
				fsys := mustBaseFS(tb, "null:")
				tb.Cleanup(func() {
					if err := fsys.Close(); err != nil {
						tb.Errorf("Close() = %v", err)
					}
				})
				return fsys
			},
			wantString: "null:",
		},
	}
)

func getAllExceptAngryTestCaseList() []fsTestCase {
	return appendNestFSTestCase(append(readOnlyFSTestCaseList, readWriteFSTestCaseList...))
}

func appendNestFSTestCase(tcl []fsTestCase) []fsTestCase {
	ctx := context.Background()
	result := make([]fsTestCase, len(tcl)*2)
	for idx, tc := range tcl {
		result[idx*2] = tc
		result[idx*2+1] = fsTestCase{
			name: "nestFS." + tc.name,
			createFS: func(tb testing.TB) WriteFS {
				return makeNestFS(ctx, tc.createFS(tb))
			},
		}
	}
	return result
}

// mustBaseFS opens name as a base file system: the file system of the
// registered driver that matches name, as is. The WriteFS it returns is not
// wrapped in a nestFS, so it has no mounts and no archive directories; use
// mustNestFS for the file system that New returns.
func mustBaseFS(tb testing.TB, name string) WriteFS {
	tb.Helper()
	return mustFS(tb, newBaseFS, name)
}

// mustNestFS opens name as New does: the base file system for name wrapped
// in a nestFS. It returns an FS, which only a nestFS implements, where
// mustBaseFS returns a WriteFS.
func mustNestFS(tb testing.TB, name string) FS {
	tb.Helper()
	fsys, err := newNestFS(tb.Context(), name)
	if err != nil {
		tb.Fatalf("FileSystem %q has an error, %s", name, err)
	}
	return fsys
}

// mustNullFile returns a file of the null file system. It implements File
// itself, so the file wrappers pass it through as is.
func mustNullFile(tb testing.TB) File {
	tb.Helper()
	f, err := mustBaseFS(tb, "null:").Create("test.txt")
	if err != nil {
		tb.Fatalf("Create() on the null file system has an error, %s", err)
	}
	return f
}

func mustFS(tb testing.TB, newFSFunc func(context.Context, string) (WriteFS, error), name string) WriteFS {
	tb.Helper()

	fsys, err := newFSFunc(tb.Context(), name)
	if err != nil {
		tb.Fatalf("FileSystem %q has an error, %s", name, err)
	}
	if fsys == nil {
		tb.Fatalf("FileSystem %q is nil", name)
	}

	return fsys
}

// dirFileConflictCases are Create and MkdirAll calls that conflict with the
// tree built by newDirFileConflictFS: each replaces a directory with a file
// or treats a regular file as a directory. wantErr and wantOp are the values
// memFS returns; other backends may report the conflict differently (for
// example localFS surfaces the OS's ENOTDIR/EISDIR).
var dirFileConflictCases = []struct {
	name    string
	op      func(fsys WriteFS) error
	wantErr error
	wantOp  string
}{
	{name: "create_on_dir", op: func(fsys WriteFS) error { _, err := fsys.Create("dir/sub"); return err }, wantErr: fs.ErrInvalid, wantOp: "create"},
	{name: "create_on_root", op: func(fsys WriteFS) error { _, err := fsys.Create(pathutil.CwdPath); return err }, wantErr: fs.ErrInvalid, wantOp: "create"},
	{name: "create_under_file", op: func(fsys WriteFS) error { _, err := fsys.Create("dir/file/x"); return err }, wantErr: fs.ErrExist, wantOp: "create"},
	{name: "create_deep_under_file", op: func(fsys WriteFS) error { _, err := fsys.Create("dir/file/x/y"); return err }, wantErr: fs.ErrExist, wantOp: "create"},
	{name: "mkdirall_on_file", op: func(fsys WriteFS) error { return fsys.MkdirAll("dir/file", fs.ModePerm) }, wantErr: fs.ErrExist, wantOp: "mkdir"},
	{name: "mkdirall_under_file", op: func(fsys WriteFS) error { return fsys.MkdirAll("dir/file/x/y", fs.ModePerm) }, wantErr: fs.ErrExist, wantOp: "mkdir"},
}

// newDirFileConflictFS returns a file system from createFS containing the
// directories dir and dir/sub and the regular file dir/file.
func newDirFileConflictFS(t *testing.T, createFS func(testing.TB) WriteFS) WriteFS {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	if err := fsys.MkdirAll("dir/sub", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("dir/file")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("content"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return fsys
}
