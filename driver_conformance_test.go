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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
)

// fsTestCase describes one FS instance a conformance test can exercise.
type fsTestCase struct {
	name       string
	createFS   func(tb testing.TB) FS
	wantString string
}

// driverTestCases maps every conformance-eligible driver's registered Name
// to how to build a fresh instance of it for testing. Every driver
// registered with Standard: true must have an entry here —
// TestConformanceDriverCoverage fails loudly if one is missing, so a newly
// added driver can't silently skip conformance coverage.
var driverTestCases = map[string]fsTestCase{
	"local": {
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
	"memory": {
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
	"bolt": boltFSTestCaseList()[0],
	"null": {
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
	"archive": {
		name:       "archiveFS",
		createFS:   mustArchiveFS,
		wantString: "archiveFS(",
	},
	"http-archive": {
		// newTempMountRemoteArchiveFS (the real driver CreateFunc) uses the
		// SSRF-hardened download client, which correctly refuses to fetch
		// from httptest's loopback address. testDownloadAndMount exercises
		// the same download-then-mount behavior via a client that skips
		// pre-flight SSRF validation (as archivefs_test.go's own
		// TestDownloadFileAndMount* tests already do), so this still
		// exercises real archive-over-HTTP behavior even though it doesn't
		// go through the driver's exact tempMountFS-wrapping path.
		name: "httpArchiveFS",
		createFS: func(tb testing.TB) FS {
			ts := testArchiveServer(tb)
			return testDownloadAndMount(tb, ts, "/testassets.zip")
		},
		wantString: "archiveFS(",
	},
	"git": {
		name: "gitFS",
		createFS: func(tb testing.TB) FS {
			srcDir, err := osutil.MkdirTemp("", "conformance*.git")
			if err != nil {
				tb.Fatal(err)
			}
			tb.Cleanup(func() {
				if err := osutil.RemoveAll(srcDir); err != nil {
					tb.Errorf("osutil.RemoveAll(%q) = %v", srcDir, err)
				}
			})
			// An empty repo (no committed files) so the FS starts pristine,
			// matching every other conformance driver's starting state.
			if err := initTestGitRepo(tb, srcDir, map[string]string{}); err != nil {
				tb.Fatalf("initTestGitRepo() = %v, want nil", err)
			}
			fsys, err := newGitFS(tb.Context(), srcDir)
			if err != nil {
				tb.Fatalf("newGitFS(%q) = %v, want nil", srcDir, err)
			}
			tb.Cleanup(func() {
				if err := fsys.Close(); err != nil {
					tb.Errorf("Close() = %v", err)
				}
			})
			return fsys
		},
		wantString: "tempMountFS(",
	},
	"gcs": {
		name: "gcsFS",
		createFS: func(tb testing.TB) FS {
			client := createEmptyStorage(tb)
			fsys, err := makeGCSFSWithClient(tb.Context(), client, "gs://first")
			if err != nil {
				tb.Fatalf("makeGCSFSWithClient() = %v, want nil", err)
			}
			tb.Cleanup(func() {
				if err := fsys.Close(); err != nil {
					tb.Errorf("Close() = %v", err)
				}
			})
			return fsys
		},
		wantString: "gcsFS(",
	},
}

// permDeniedFSTestCaseList holds FSes whose write operations return
// fs.ErrPermission rather than succeeding silently. This isn't derived from
// the driver registry: readOnlyFS is a wrapper applied programmatically
// (via ReadOnly), not a URI-addressable driver.
var permDeniedFSTestCaseList = []fsTestCase{
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

var (
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

// standardDrivers returns every registered driver with Standard set, sorted
// by Name for deterministic test output.
func standardDrivers() []Driver {
	r := getRegistrar()
	r.RLock()
	defer r.RUnlock()
	var drivers []Driver
	for _, d := range r.m {
		if d.Standard {
			drivers = append(drivers, d)
		}
	}
	sort.Slice(drivers, func(i, j int) bool { return drivers[i].Name < drivers[j].Name })
	return drivers
}

// TestConformanceDriverCoverage ensures every Standard driver has a
// construction entry in driverTestCases, so a newly registered conformance
// driver can't silently skip these tests.
func TestConformanceDriverCoverage(t *testing.T) {
	for _, d := range standardDrivers() {
		if _, ok := driverTestCases[d.Name]; !ok {
			t.Errorf("driver %q is registered with Standard: true but has no entry in driverTestCases", d.Name)
		}
	}
}

func readWriteFSTestCaseList() []fsTestCase {
	var cases []fsTestCase
	for _, d := range standardDrivers() {
		if tc, ok := driverTestCases[d.Name]; ok && d.ReadWrite {
			cases = append(cases, tc)
		}
	}
	return cases
}

func readOnlyFSTestCaseList() []fsTestCase {
	var cases []fsTestCase
	for _, d := range standardDrivers() {
		if tc, ok := driverTestCases[d.Name]; ok && !d.ReadWrite {
			cases = append(cases, tc)
		}
	}
	return cases
}

func getReadWriteTestCaseList() []fsTestCase {
	return readWriteFSTestCaseList()
}

func getReadOnlyTestCaseList() []fsTestCase {
	return appendNestFSTestCase(readOnlyFSTestCaseList())
}

// getAllTestCaseList returns every Standard driver's case (read-write and
// read-only), plus their nestFS-wrapped variants.
func getAllTestCaseList() []fsTestCase {
	return appendNestFSTestCase(append(readOnlyFSTestCaseList(), readWriteFSTestCaseList()...))
}

func getAllRegularTestCaseList() []fsTestCase {
	return appendNestFSTestCase(readWriteFSTestCaseList())
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

// testFileSystem runs the standard CRUD + fstest.TestFS battery against the
// FS built by tc.createFS. tc.createFS is responsible for its own cleanup
// (via tb.Cleanup), so this does not close fsys itself.
func testFileSystem(t *testing.T, tc fsTestCase) {
	t.Helper()
	fsys := tc.createFS(t)

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
		t.Errorf("fstest.TestFS failed for %q: %v", tc.name, err)
	}
}

// TestFSConformance runs the CRUD + fstest.TestFS battery against every
// Standard, read-write driver (and its nestFS-wrapped variant). This
// replaces what used to be one hand-written wrapper test per backend
// (TestLocalFS, TestMemFS, TestBoltFS, TestGCSFS, TestNestFS).
func TestFSConformance(t *testing.T) {
	t.Parallel()
	for _, tc := range getAllRegularTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testFileSystem(t, tc)
		})
	}
}

func mkdirForTest(tb testing.TB, fsys FS, dirs ...string) {
	tb.Helper()
	dir := path.Join(dirs...)
	if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
		tb.Fatalf("cannot create directory %q, %s", dir, err)
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

// TestFSMkdirAll verifies MkdirAll across every Standard driver. Read-write
// drivers must always succeed; read-only drivers may either succeed as a
// no-op (e.g. nullFS, which discards writes) or reject with
// fs.ErrPermission (e.g. archiveFS) — both are valid read-only conformance
// behaviors.
func TestFSMkdirAll(t *testing.T) {
	t.Parallel()
	for _, tc := range getAllRegularTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			if err := fsys.MkdirAll("subdir", fs.ModePerm); err != nil {
				t.Errorf("MkdirAll() = %v, want nil", err)
			}
		})
	}
	for _, tc := range getReadOnlyTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			if err := fsys.MkdirAll("subdir", fs.ModePerm); err != nil && !errors.Is(err, fs.ErrPermission) {
				t.Errorf("MkdirAll() = %v, want nil or fs.ErrPermission", err)
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

func TestReadOnlyFS(t *testing.T) {
	t.Parallel()

	for _, tc := range append(append(readWriteFSTestCaseList(), readOnlyFSTestCaseList()...), permDeniedFSTestCaseList...) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			if fsys == nil {
				t.Fatalf("file system is nil")
			}
			verifyReadOnlyFS(t, fsys)
		})
	}
}

func TestFS(t *testing.T) {
	t.Parallel()

	for _, tc := range readWriteFSTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			if fsys == nil {
				t.Fatalf("file system is nil")
			}
			verifyFS(t, fsys)
		})
	}
}
