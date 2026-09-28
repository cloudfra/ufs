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
	"io"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/cloudfra/ufs/internal/pathutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

func TestFSConventions(t *testing.T) {
	srcFS, err := newLocalFS(t.Context(), testLocalFSName)
	if err != nil {
		t.Fatalf("cannot mount localFS(%q), %s", testLocalFSName, err)
	}
	t.Cleanup(ufsTesting.ValidateClose(t, srcFS))
	for _, fsysTC := range getReadWriteTestCaseList() {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)
			if err := Rsync(srcFS, fsys, pathutil.CwdPath); err != nil {
				t.Errorf("rsync failed with error, %s", err)
			}

			allFilenames, err := List(srcFS, pathutil.CwdPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(allFilenames) == 0 {
				t.Fatal("expected at least 1 file name")
			}
			if err := fstest.TestFS(fsys, allFilenames...); err != nil {
				t.Error(err)
			}
		})
	}
}

type fsTestCase struct {
	name       string
	createFS   func(tb testing.TB) FS
	wantString string
}

var (
	readWriteFSTestCaseList = []fsTestCase{
		{
			name: "localFS",
			createFS: func(tb testing.TB) FS {
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
			createFS: func(tb testing.TB) FS {
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
			createFS: func(tb testing.TB) FS {
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
			createFS: func(tb testing.TB) FS {
				fsys := makeNullFS(nullFSPrefix)
				tb.Cleanup(func() {
					if err := fsys.Close(); err != nil {
						tb.Errorf("Close() = %v", err)
					}
				})
				return fsys
			},
			wantString: nullFSPrefix,
		},
	}

	// permDeniedFSTestCaseList holds FSes whose write operations return
	// fs.ErrPermission rather than succeeding silently.
	permDeniedFSTestCaseList = []fsTestCase{
		{
			name: "readOnlyFS",
			createFS: func(tb testing.TB) FS {
				inner := makeNullFS(nullFSPrefix)
				tb.Cleanup(func() {
					if err := inner.Close(); err != nil {
						tb.Fatalf("failed to close inner FS: %v", err)
					}
				})
				return ReadOnly(inner)
			},
			wantString: nullFSPrefix,
		},
	}

	testassetFilenameList = []string{
		pathutil.CwdPath,
		"files/index.html",
		"archives/nested-testassets.zip",
	}

	testassetDirList = map[string][]string{
		pathutil.CwdPath: {},
		"files":          {},
		"archives":       {},
	}

	testassetCreateFileList = []string{"a.txt", "b.txt", "a/b.txt"}
)

func getReadWriteTestCaseList() []fsTestCase {
	return readWriteFSTestCaseList
}

func getAllRegularTestCaseList() []fsTestCase {
	return appendNestFSTestCase(readWriteFSTestCaseList)
}

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
			createFS: func(tb testing.TB) FS {
				return makeNestFS(ctx, tc.createFS(tb))
			},
		}
	}
	return result
}

func mustFS(tb testing.TB, newFSFunc func(context.Context, string) (FS, error), name string) FS {
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

func TestFSMkdirAll(t *testing.T) {
	t.Parallel()
	for _, tc := range getAllExceptAngryTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			defer ufsTesting.ValidateClose(t, fsys)()
			if err := fsys.MkdirAll("subdir", fs.ModePerm); err != nil {
				t.Errorf("MkdirAll() = %v, want nil", err)
			}
		})
	}
}

func TestFSReadFile(t *testing.T) {
	t.Parallel()
	for _, tc := range getReadWriteTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			wantData := ufsTesting.RandomString(100)
			t.Parallel()
			fsys := tc.createFS(t)
			defer ufsTesting.ValidateClose(t, fsys)()
			f, err := fsys.Create("readfile_test.txt")
			if err != nil {
				t.Fatalf("Create failed: %v", err)
			}
			if _, err := io.WriteString(f, wantData); err != nil {
				t.Fatalf("WriteString failed: %v", err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("Close failed: %v", err)
			}

			rfs, ok := fsys.(fs.ReadFileFS)
			if !ok {
				t.Skip("does not implement fs.ReadFileFS")
			}
			got, err := rfs.ReadFile("readfile_test.txt")
			if err != nil {
				t.Fatalf("ReadFile failed: %v", err)
			}
			if diff := cmp.Diff(wantData, string(got)); diff != "" {
				t.Errorf("ReadFile mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func verifyFS(t *testing.T, fsys FS) {
	verifyReadOnlyFS(t, fsys)
}

func verifyReadOnlyFS(t *testing.T, fsys fs.FS) {
	t.Helper()
	if fsys == nil {
		t.Fatal("file system is nil")
	}
	if _, ok := fsys.(ReadFS); !ok {
		t.Errorf("file system does not implement ReadFS")
	}
}

func TestReadOnlyFS(t *testing.T) {
	t.Parallel()

	for _, tc := range append(append(readWriteFSTestCaseList, readOnlyFSTestCaseList...), permDeniedFSTestCaseList...) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			defer ufsTesting.ValidateClose(t, fsys)()
			if fsys == nil {
				t.Fatalf("file system is nil")
			}
			verifyReadOnlyFS(t, fsys)
			ufsTesting.ValidateClose(t, fsys)()
		})
	}
}

func TestFS(t *testing.T) {
	t.Parallel()

	for _, tc := range readWriteFSTestCaseList {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			defer ufsTesting.ValidateClose(t, fsys)()
			if fsys == nil {
				t.Fatalf("file system is nil")
			}
			verifyFS(t, fsys)
			ufsTesting.ValidateClose(t, fsys)()
		})
	}
}

func TestReadOnlyFSURIIncludesROTag(t *testing.T) {
	t.Parallel()

	for _, tc := range append(readOnlyFSTestCaseList, permDeniedFSTestCaseList...) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			defer ufsTesting.ValidateClose(t, fsys)()

			u, err := fsys.URI()
			if err != nil {
				t.Fatalf("URI() returned error: %v", err)
			}
			if u == nil {
				t.Fatal("URI() = nil, want a URL")
			}
			if got := u.Query().Get("ro"); got != "true" {
				t.Errorf("URI().Query().Get(\"ro\") = %q, want %q", got, "true")
			}
		})
	}
}

// dirFileConflictCases are Create and MkdirAll calls that conflict with the
// tree built by newDirFileConflictFS: each replaces a directory with a file
// or treats a regular file as a directory. wantErr and wantOp are the values
// memFS returns; other backends may report the conflict differently (for
// example localFS surfaces the OS's ENOTDIR/EISDIR).
var dirFileConflictCases = []struct {
	name    string
	op      func(fsys FS) error
	wantErr error
	wantOp  string
}{
	{name: "create_on_dir", op: func(fsys FS) error { _, err := fsys.Create("dir/sub"); return err }, wantErr: fs.ErrInvalid, wantOp: "create"},
	{name: "create_on_root", op: func(fsys FS) error { _, err := fsys.Create(pathutil.CwdPath); return err }, wantErr: fs.ErrInvalid, wantOp: "create"},
	{name: "create_under_file", op: func(fsys FS) error { _, err := fsys.Create("dir/file/x"); return err }, wantErr: fs.ErrExist, wantOp: "create"},
	{name: "create_deep_under_file", op: func(fsys FS) error { _, err := fsys.Create("dir/file/x/y"); return err }, wantErr: fs.ErrExist, wantOp: "create"},
	{name: "mkdirall_on_file", op: func(fsys FS) error { return fsys.MkdirAll("dir/file", fs.ModePerm) }, wantErr: fs.ErrExist, wantOp: "mkdir"},
	{name: "mkdirall_under_file", op: func(fsys FS) error { return fsys.MkdirAll("dir/file/x/y", fs.ModePerm) }, wantErr: fs.ErrExist, wantOp: "mkdir"},
}

// newDirFileConflictFS returns a file system from createFS containing the
// directories dir and dir/sub and the regular file dir/file.
func newDirFileConflictFS(t *testing.T, createFS func(testing.TB) FS) FS {
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
