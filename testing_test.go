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
	"os"
	"path"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/xyproto/randomstring"
)

type fsTestCase struct {
	name       string
	createFS   func(tb testing.TB) FS
	wantString string
}

var (
	angryFSTestCase = fsTestCase{
		name: "angryFS",
		createFS: func(tb testing.TB) FS {
			return mustAngryFS(tb)
		},
		wantString: angryFSPrefix,
	}

	readWriteFSTestCaseList = []fsTestCase{
		{
			name: "localFS",
			createFS: func(tb testing.TB) FS {
				dir := mustTemp(tb)
				fsys, err := newLocalFS(dir)
				if err != nil {
					tb.Fatalf("cannot create localFS file system, %s", err)
				}
				tb.Cleanup(func() {
					fsys.Close()
				})
				return fsys
			},
			wantString: "file://" + osTempDir(),
		},
		{
			name: "tempMountFS",
			createFS: func(tb testing.TB) FS {
				fsys, err := newTempMountFS("test://", func(string) error { return nil })
				if err != nil {
					tb.Fatalf("cannot create tempMountFS file system, %s", err)
				}
				tb.Cleanup(func() {
					fsys.Close()
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
					fsys.Close()
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
					fsys.Close()
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
		cwdPath,
		"files/index.html",
		"archives/nested-testassets.zip",
	}

	testassetDirList = map[string][]string{
		cwdPath:    {},
		"files":    {},
		"archives": {},
	}

	testassetCreateFileList = []string{"a.txt", "b.txt", "a/b.txt"}
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
	result := []fsTestCase{}
	for _, tc := range tcl {
		result = append(result, tc, fsTestCase{
			name: "nestFS." + tc.name,
			createFS: func(tb testing.TB) FS {
				return makeNestFS(ctx, tc.createFS(tb))
			},
		})
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

func newFSFuncWithoutContext(fn func(name string) (FS, error)) func(context.Context, string) (FS, error) {
	return func(_ context.Context, name string) (FS, error) {
		return fn(name)
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

func randomString(size int) string {
	return randomstring.HumanFriendlyString(size)
}

func osTempDir() string {
	return coerceUnix(os.TempDir())
}

func mustTemp(tb testing.TB) string {
	tempDir, err := os.MkdirTemp("", "")
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() {
		if err := os.RemoveAll(tempDir); err != nil {
			tb.Error(err)
		}
	})
	return tempDir
}

func mustTime(s string) time.Time {
	val, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return val
}

func TestFSMkdirAll(t *testing.T) {
	t.Parallel()
	for _, tc := range getAllExceptAngryTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			defer fsys.Close()
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
			defer fsys.Close()
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
			if fsys == nil {
				t.Fatalf("file system is nil")
			}
			defer func() {
				if err := fsys.Close(); err != nil {
					t.Errorf("second file system close() failed, %s", err)
				}
			}()
			verifyReadOnlyFS(t, fsys)
			if err := fsys.Close(); err != nil {
				t.Errorf("file system failed to close without errors, %s", err)
			}
		})
	}
}

func TestFS(t *testing.T) {
	t.Parallel()

	for _, tc := range readWriteFSTestCaseList {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			if fsys == nil {
				t.Fatalf("file system is nil")
			}
			defer func() {
				if err := fsys.Close(); err != nil {
					t.Errorf("second file system close() failed, %s", err)
				}
			}()
			verifyFS(t, fsys)
			if err := fsys.Close(); err != nil {
				t.Errorf("file system failed to close without errors, %s", err)
			}
		})
	}
}

func must(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Error(err)
	}
}

func toMapKeys[T any](m map[string]T) []string {
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func dirEntryListToNames(entries []fs.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

func assertContains(t *testing.T, fsys FS, name string, substr string) {
	t.Helper()
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		t.Error(err)
	}
	if !strings.Contains(string(data), substr) {
		t.Errorf("%q does not contain %q, (len: %d) %q", name, substr, len(data), string(data))
	}
}

func assertDir(t *testing.T, fsys FS, name string, want []string) {
	t.Helper()

	if gotEntries, err := fsys.ReadDir(name); err != nil {
		t.Errorf("cannot ReadDir(%q), %s", name, err)
	} else {
		gotEntryNames := dirEntryListToNames(gotEntries)
		if d := cmp.Diff(want, gotEntryNames); d != "" {
			t.Errorf("fs.ReadDir(%q) mismatch, got %s, want %s diff(-want,+got):\n %v", name, gotEntryNames, want, d)
		}
	}

	if f, err := fsys.Open(name); err != nil {
		t.Errorf("cannot open %q, %s", name, err)
	} else {
		rdf, ok := f.(fs.ReadDirFile)
		if ok {
			if gotEntries, err := rdf.ReadDir(-1); err != nil {
				t.Errorf("cannot ReadDir(%q), %s", name, err)
			} else {
				gotEntryNames := dirEntryListToNames(gotEntries)
				if d := cmp.Diff(want, gotEntryNames); d != "" {
					t.Errorf("ReadDir(-1) mismatch, got %s, want %s diff(-want,+got):\n %v", gotEntryNames, want, d)
				}
			}
		} else {
			t.Errorf("%q does not open a ReadDirFile, %s", name, reflect.TypeOf(f).Name())
		}
	}
}

func skipTestOnWindows(tb testing.TB) {
	if runtime.GOOS == "windows" {
		tb.Skip("test is not compatible with windows, skipping")
	}
}
