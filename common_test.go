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
	"testing"

	"github.com/cloudfra/ufs/internal/pathutil"
)

// tempMountTestURI opens a tempMountFS. It is served by a driver that only
// these tests register, see common_driver_test.go: a tempMountFS has no URI of
// its own.
const tempMountTestURI = "test-tempmount:"

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
				fsys := mustBaseFS(tb, tb.TempDir())
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
				fsys := mustBaseFS(tb, tempMountTestURI)
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
				fsys := mustBaseFS(tb, "memory:")
				tb.Cleanup(func() {
					if err := fsys.Close(); err != nil {
						tb.Errorf("Close() = %v", err)
					}
				})
				return fsys
			},
			wantString: "memory:",
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
