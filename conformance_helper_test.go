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
	"fmt"
	"io"
	"io/fs"
	"path"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
)

// This file holds the hard coded list of file systems that the conformance
// tests in conformance_test.go run against, plus the shared support logic for
// those tests.

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
				dir := mustTemp(tb)
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
			wantString: "file://" + osTempDir(),
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

func testFileSystem(t *testing.T, newFSFunc func(ctx context.Context, name string) (FS, error), name string) {
	t.Helper()
	fsys := mustFS(t, newFSFunc, name)

	wantFiles := []string{"a", "ab/b/c", "ab/d/c", "def", "abc", "abc.txt", "temp/abc.txt"}

	mkdirForTest(t, fsys, "ab/b")
	mkdirForTest(t, fsys, "temp")
	mkdirForTest(t, fsys, "ab/d")

	for _, name := range wantFiles {
		t.Run(fmt.Sprintf("crud_%s", name), func(t *testing.T) {
			wantData := randomString(1000)
			if wf, err := fsys.Create(name); err != nil {
				t.Errorf("cannot create file %q, %s", name, err)
			} else {
				info, err := wf.Stat()
				if err != nil {
					t.Errorf("cannot Stat() %q, %s", name, err)
				}
				if info == nil {
					t.Fatalf("info is nil")
				}
				if info.IsDir() != false {
					t.Errorf("%q is a directory, want file", name)
				}
				if n, err := io.WriteString(wf, wantData); err != nil {
					t.Errorf("cannot write file content to %q, %s", name, err)
				} else if n != len(wantData) {
					t.Errorf("contents written to file does not match the size got %d, want %d", n, len(wantData))
				}
				if err := wf.Close(); err != nil {
					t.Errorf("failed to Close() write file %q, %s", name, err)
				}
			}

			if rf, err := fsys.Open(name); err != nil {
				t.Errorf("cannot open file %q, %s", name, err)
			} else {
				if rf == nil {
					t.Fatal("rf is nil")
				}
				info, err := rf.Stat()
				if err != nil {
					t.Errorf("cannot Stat() %q, %s", name, err)
				}
				if info == nil {
					t.Fatal("info is nil")
				}
				if info.IsDir() != false {
					t.Errorf("%q is a directory, want file", name)
				}
				if got, err := io.ReadAll(rf); err != nil {
					t.Errorf("cannot read file content to %q, %s", name, err)
				} else if diff := cmp.Diff(wantData, string(got)); diff != "" {
					t.Errorf("io.ReadAll(%s) mismatch (-want +got):\n%s\nwant: %q\ngot: %q", name, diff, wantData, string(got))
				}
				if err := rf.Close(); err != nil {
					t.Errorf("failed to Close() read file %q, %s", name, err)
				}
			}
		})
	}

	if err := fstest.TestFS(fsys, wantFiles...); err != nil {
		t.Errorf("fstest.TestFS failed for %q: %v", name, err)
	}

	if err := fsys.Close(); err != nil {
		t.Errorf("error on Close(), %v", err)
	}
}

func mkdirForTest(tb testing.TB, fsys FS, dirs ...string) {
	tb.Helper()
	dir := path.Join(dirs...)
	if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
		tb.Fatalf("cannot create directory %q, %s", dir, err)
	}
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

func assertInvalidPathError(t *testing.T, path string, err error, wantOp string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s(%q) succeeded, want error", wantOp, path)
		return
	}
	if perr, ok := err.(*fs.PathError); ok {
		if wantOp != perr.Op {
			t.Errorf("fs.PathError.Op mismatch, got: %q, want: %q", perr.Op, wantOp)
		}
		if path != perr.Path {
			t.Errorf("fs.PathError.Path mismatch, got: %q, want: %q", perr.Path, path)
		}
	} else {
		t.Errorf("%q is not a *fs.PathError, got: %q", err, reflect.TypeOf(err).Name())
	}
}
