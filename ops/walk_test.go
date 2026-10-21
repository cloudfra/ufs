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

package ops_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs/drivers/decorators/readonlyfs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/drivers/wrappers/readwrapfs"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/testing/archivetest"
	"github.com/cloudfra/ufs/ops"
	"github.com/google/go-cmp/cmp"
)

func TestWalk(t *testing.T) {
	fsys := setupListFS(t)

	var got []string
	err := ops.Walk(fsys, pathutil.CwdPath, ops.WalkArgs{}, func(name string) error {
		got = append(got, name)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() = %v, want nil", err)
	}
	want := []string{"a.txt", "dir/b.txt", "dir/c.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Walk() mismatch (-want +got):\n%s", diff)
	}
}

func TestWalkExcludeDirectoryNil(t *testing.T) {
	fsys := setupListFS(t)

	var got []string
	err := ops.Walk(fsys, pathutil.CwdPath, ops.WalkArgs{ExcludeDirectory: nil}, func(name string) error {
		got = append(got, name)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() = %v, want nil", err)
	}
	want := []string{"a.txt", "dir/b.txt", "dir/c.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Walk() nil ExcludeDirectory mismatch (-want +got):\n%s", diff)
	}
}

func TestWalkExcludeDirectoryExact(t *testing.T) {
	fsys := setupListFS(t)

	var got []string
	err := ops.Walk(fsys, pathutil.CwdPath, ops.WalkArgs{ExcludeDirectory: []string{"dir"}}, func(name string) error {
		got = append(got, name)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() = %v, want nil", err)
	}
	want := []string{"a.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Walk() ExcludeDirectory exact mismatch (-want +got):\n%s", diff)
	}
}

func TestWalkExcludeDirectoryGlob(t *testing.T) {
	fsys := setupListFS(t)

	var got []string
	err := ops.Walk(fsys, pathutil.CwdPath, ops.WalkArgs{ExcludeDirectory: []string{"d*"}}, func(name string) error {
		got = append(got, name)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() = %v, want nil", err)
	}
	want := []string{"a.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Walk() ExcludeDirectory glob mismatch (-want +got):\n%s", diff)
	}
}

func TestWalkIncludeMountedArchiveDefault(t *testing.T) {
	nfs := setupNestFSWithArchive(t)

	var got []string
	err := ops.Walk(nfs, pathutil.CwdPath, ops.WalkArgs{}, func(name string) error {
		got = append(got, name)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() = %v, want nil", err)
	}
	// data.zip.d is a virtual archive-mount dir and must be skipped by default.
	want := []string{"data.zip", "readme.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Walk() default (no archives) mismatch (-want +got):\n%s", diff)
	}
}

// TestWalkSkipsMountedArchiveThroughWrapper verifies that a file system
// wrapped around the nested file system still lets Walk skip archive
// directories.
func TestWalkSkipsMountedArchiveThroughWrapper(t *testing.T) {
	testCases := []struct {
		name string
		wrap func(t *testing.T, fsys types.WriteFS) fs.FS
	}{
		{name: "readOnly", wrap: func(_ *testing.T, fsys types.WriteFS) fs.FS { return readonlyfs.New(fsys) }},
		{name: "readWrap", wrap: func(_ *testing.T, fsys types.WriteFS) fs.FS { return readwrapfs.FromFS(fsys) }},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := tc.wrap(t, setupNestFSWithArchive(t))

			var got []string
			err := ops.Walk(fsys, pathutil.CwdPath, ops.WalkArgs{}, func(name string) error {
				got = append(got, name)
				return nil
			})
			if err != nil {
				t.Fatalf("Walk() = %v, want nil", err)
			}
			want := []string{"data.zip", "readme.txt"}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Walk() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWalkIncludeMountedArchive(t *testing.T) {
	nfs := setupNestFSWithArchive(t)

	var got []string
	err := ops.Walk(nfs, pathutil.CwdPath, ops.WalkArgs{IncludeMountedArchive: true}, func(name string) error {
		got = append(got, name)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() = %v, want nil", err)
	}
	// With IncludeMountedArchive the walk descends into data.zip.d.
	want := []string{"data.zip", "data.zip.d/inside.txt", "readme.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Walk() IncludeMountedArchive mismatch (-want +got):\n%s", diff)
	}
}

func TestWalkCallbackError(t *testing.T) {
	fsys := setupListFS(t)
	sentinel := errors.New("stop")

	count := 0
	err := ops.Walk(fsys, pathutil.CwdPath, ops.WalkArgs{}, func(_ string) error {
		count++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("Walk() = %v, want sentinel error", err)
	}
	if count != 1 {
		t.Errorf("callback called %d times, want 1", count)
	}
}

// TestWalkNestFSRegularSubdirNotSkipped verifies that a real subdirectory inside
// a nestFS is descended into even when IncludeMountedArchive is false.
func TestWalkNestFSRegularSubdirNotSkipped(t *testing.T) {
	nfs := newMemNestFS(t)
	if err := nfs.MkdirAll("subdir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, nfs, "subdir/nested.txt", []byte("nested"))
	writeTestFile(t, nfs, "data.zip", archivetest.Zip(t, archivetest.Entry{Name: "inside.txt", Data: []byte("content")}))

	var got []string
	err := ops.Walk(nfs, pathutil.CwdPath, ops.WalkArgs{}, func(name string) error {
		got = append(got, name)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() = %v, want nil", err)
	}
	// Real subdir must be descended; archive-mount dir must be skipped.
	want := []string{"data.zip", "subdir/nested.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Walk() regular subdir mismatch (-want +got):\n%s", diff)
	}
}
