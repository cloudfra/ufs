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
