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

package conformance

import (
	"context"
	"io"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

type fsTestCase struct {
	name       string
	createFS   func(tb testing.TB) ufs.WriteFS
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
			wantData := randomString(100)
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
