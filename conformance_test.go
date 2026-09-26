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

// Conformance tests run every built-in backend through the shared checks in
// drivers/testing plus the ufs-specific ReadFS/FS contract (String, URI,
// interface assertions). Backend-specific tests live in each backend's own
// _test.go file.

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	driverTesting "github.com/cloudfra/ufs/drivers/testing"

	"github.com/cloudfra/ufs/internal/pathutil"
	internalTesting "github.com/cloudfra/ufs/internal/testing"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

type fsTestCase struct {
	name       string
	createFS   func(tb testing.TB) FS
	wantString string
}

var (
	angryFSTestCase = fsTestCase{
		name:       "angryFS",
		createFS:   mustAngryFS,
		wantString: angryFSPrefix,
	}

	readWriteFSTestCaseList = []fsTestCase{
		{
			name: "localFS",
			createFS: func(tb testing.TB) FS {
				dir := internalTesting.MkdirTemp(tb)
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
			wantString: "file://" + internalTesting.TempDir(),
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
)

func getReadWriteTestCaseList() []fsTestCase {
	return readWriteFSTestCaseList
}

func getAllTestCaseList() []fsTestCase {
	return appendNestFSTestCase(append(append(readOnlyFSTestCaseList, readWriteFSTestCaseList...), angryFSTestCase))
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

// fsFactory adapts a driver constructor to a driverTesting.Factory that
// creates the file system named name.
func fsFactory(newFSFunc func(ctx context.Context, name string) (FS, error), name string) driverTesting.Factory {
	return func(tb testing.TB) fs.FS {
		return mustFS(tb, newFSFunc, name)
	}
}

// factory adapts tc.createFS to a driverTesting.Factory.
func (tc fsTestCase) factory() driverTesting.Factory {
	return func(tb testing.TB) fs.FS { return tc.createFS(tb) }
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
			verifyReadOnlyFS(t, fsys)
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

func TestFSString(t *testing.T) {
	for _, tc := range getAllTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			if got := fsys.String(); !strings.Contains(got, tc.wantString) {
				t.Errorf("%s.String() should contain %q: got: %q", fsys, tc.wantString, got)
			}
		})
	}
}

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

func TestInvalidPath(t *testing.T) {
	for _, tc := range getAllTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			driverTesting.InvalidPaths[File](t, tc.factory())
		})
	}
}

func TestFSClose(t *testing.T) {
	for _, tc := range getAllRegularTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			driverTesting.Close[File](t, tc.factory())
		})
	}
}

func TestFSMkdirAll(t *testing.T) {
	t.Parallel()
	for _, tc := range getAllExceptAngryTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			driverTesting.MkdirAll(t, tc.factory())
		})
	}
}

func TestFSReadDir(t *testing.T) {
	for _, tc := range getAllRegularTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			driverTesting.ReadDir(t, tc.factory())
		})
	}
}

func TestFSCreate(t *testing.T) {
	for _, tc := range getAllRegularTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			driverTesting.CreateAndRead[File](t, tc.factory())
		})
	}
}

func TestFSReadFile(t *testing.T) {
	t.Parallel()
	for _, tc := range getReadWriteTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			driverTesting.ReadFile[File](t, tc.factory())
		})
	}
}

// TestFSDirFileConflicts verifies that every WriteFS rejects Create and
// MkdirAll calls that would replace a directory with a file or place anything
// under a regular file, and that a rejected call leaves the tree unchanged.
func TestFSDirFileConflicts(t *testing.T) {
	t.Parallel()
	for _, tc := range getAllRegularTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			driverTesting.DirFileConflicts[File](t, tc.factory())
		})
	}
}
