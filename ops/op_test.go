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

package ops

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/decorators/readonlyfs"
	"github.com/cloudfra/ufs/drivers/wrappers/readwrapfs"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

const testLocalFSName = "../testing/testassets"

// rsyncDestTestCaseList are the writable file systems Rsync is verified
// against. localFS is opened with [ufs.New], so it is always nested.
var rsyncDestTestCaseList = []struct {
	name     string
	createFS func(tb testing.TB) ufs.WriteFS
}{
	{name: "nestFS.localFS", createFS: newTestLocalFS},
	{name: "tempMountFS", createFS: newTestTempMountFS},
	{name: "memFS", createFS: newTestMemFS},
	{name: "nestFS.tempMountFS", createFS: func(tb testing.TB) ufs.WriteFS {
		return ufs.WrapNestFS(tb.Context(), newTestTempMountFS(tb))
	}},
	{name: "nestFS.memFS", createFS: func(tb testing.TB) ufs.WriteFS {
		return ufs.WrapNestFS(tb.Context(), newTestMemFS(tb))
	}},
}

func newTestLocalFS(tb testing.TB) ufs.WriteFS {
	tb.Helper()
	fsys, err := ufs.New(tb.Context(), tb.TempDir())
	if err != nil {
		tb.Fatalf("cannot create localFS file system, %s", err)
	}
	return fsys
}

func newTestTempMountFS(tb testing.TB) ufs.WriteFS {
	tb.Helper()
	fsys, err := ufs.NewTempMountFS(tb.Context(), "test://", func(string) error { return nil })
	if err != nil {
		tb.Fatalf("cannot create tempMountFS file system, %s", err)
	}
	return fsys
}

func newTestMemFS(_ testing.TB) ufs.WriteFS {
	return ufs.MakeMemFS("memory://")
}

func newMemFS(name string) (ufs.WriteFS, error) {
	return ufs.MakeMemFS(name), nil
}

func newAngryFS(name string) (ufs.WriteFS, error) {
	return ufs.New(context.Background(), name)
}

// testAssetsFS returns the test assets as the source of the Rsync tests. It
// fails the test when the directory is missing or empty: os.DirFS does not
// report a missing directory, and Rsync of nothing would pass every check.
func testAssetsFS(t *testing.T) fs.FS {
	t.Helper()
	srcFS := osutil.DirFS(testLocalFSName)
	files, err := ListFiles(srcFS, pathutil.CwdPath)
	if err != nil {
		t.Fatalf("ListFiles(%q) = %v, want nil", testLocalFSName, err)
	}
	if len(files) == 0 {
		t.Fatalf("%q holds no files, want the test assets", testLocalFSName)
	}
	return srcFS
}

func TestRsync(t *testing.T) {
	srcFS := testAssetsFS(t)
	for _, fsysTC := range rsyncDestTestCaseList {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)
			t.Cleanup(ufsTesting.ValidateClose(t, fsys))
			if err := Rsync(srcFS, fsys, pathutil.CwdPath); err != nil {
				t.Errorf("rsync failed with error, %s", err)
			}

			err := ForEachFilename(srcFS, pathutil.CwdPath, func(name string) error {
				srcData, err := fs.ReadFile(srcFS, name)
				if err != nil {
					return fmt.Errorf("cannot read srcFS(%q), %w", name, err)
				}
				gotData, err := fs.ReadFile(fsys, name)
				if err != nil {
					return fmt.Errorf("cannot read destFS(%q), %w", name, err)
				}
				wantString := string(srcData)
				gotString := string(gotData)
				if diff := cmp.Diff(wantString, gotString); diff != "" {
					return fmt.Errorf("%q mismatch got %s, want %s diff(-want,+got):\n %v", name, gotString, wantString, diff)
				}
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		})
	}
}

func TestRsyncAngry(t *testing.T) {
	srcFS := testAssetsFS(t)

	destFS, err := newAngryFS("angry://")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.WantCloseError(t, destFS)()

	if err := Rsync(srcFS, destFS, pathutil.CwdPath); err == nil {
		t.Error("rsync expected to fail got nil error")
	}
}

func TestRsyncNull(t *testing.T) {
	srcFS := testAssetsFS(t)

	destFS, err := ufs.New(t.Context(), "null://")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, destFS)()

	if err := Rsync(srcFS, destFS, pathutil.CwdPath); err != nil {
		t.Errorf("rsync expected to succeed, failed with error: %s", err)
	}

	entries, err := destFS.ReadDir(pathutil.CwdPath)
	if err != nil {
		t.Error(err)
	}
	if len(entries) > 0 {
		t.Errorf("nullFS should have 0 entries, got: %v", entries)
	}
}

// setupListFS creates a memFS with: a.txt, dir/b.txt, dir/c.txt.
func setupListFS(t *testing.T) ufs.WriteFS {
	t.Helper()
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "dir/b.txt", "dir/c.txt"} {
		f, err := fsys.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return fsys
}

// --- Copy ---

func TestCopy(t *testing.T) {
	src, err := newMemFS("memory://src")
	if err != nil {
		t.Fatal(err)
	}
	dst, err := newMemFS("memory://dst")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := src.Close(); err != nil {
			t.Errorf("failed to close src FS: %v", err)
		}
		if err := dst.Close(); err != nil {
			t.Errorf("failed to close dst FS: %v", err)
		}
	}()

	f, err := src.Create("hello.txt")
	if err != nil {
		t.Errorf("failed to create file in srcFS: %v", err)
	}
	if n, err := f.WriteString("hello world"); err != nil {
		t.Fatalf("WriteString() = %v", err)
	} else if n != len("hello world") {
		t.Fatalf("WriteString() = %d, want %d", n, len("hello world"))
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	if err := Copy(src, "hello.txt", dst, "copy.txt"); err != nil {
		t.Fatalf("Copy() = %v, want nil", err)
	}

	rf, err := dst.Open("copy.txt")
	if err != nil {
		t.Fatalf("Open(copy.txt) = %v, want nil", err)
	}
	defer func() {
		if err := rf.Close(); err != nil {
			t.Errorf("failed to close rf: %v", err)
		}
	}()
	data, err := io.ReadAll(rf)
	if err != nil {
		t.Fatalf("ReadAll() = %v, want nil", err)
	}
	if string(data) != "hello world" {
		t.Errorf("copy content = %q, want %q", data, "hello world")
	}
}

func TestCopyOpenError(t *testing.T) {
	src, err := newAngryFS("angry://")
	if err != nil {
		t.Fatal(err)
	}
	dst, err := newMemFS("memory://dst")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dst.Close(); err != nil {
			t.Errorf("failed to close dst FS: %v", err)
		}
	}()

	if err := Copy(src, "file.txt", dst, "file.txt"); err == nil {
		t.Error("Copy with angry src succeeded, want error")
	}
}

// closeErrorFS is a ufs.WriteFS whose files accept every write and then fail to
// Close, like a backend that only persists buffered data on Close.
type closeErrorFS struct {
	ufs.WriteFS
	err error
}

func (fsys *closeErrorFS) Create(name string) (ufs.File, error) {
	f, err := fsys.WriteFS.Create(name)
	if err != nil {
		return nil, err
	}
	return &closeErrorFile{File: f, err: fsys.err}, nil
}

type closeErrorFile struct {
	ufs.File
	err error
}

func (f *closeErrorFile) Close() error {
	return ufserrors.Join(f.File.Close(), f.err)
}

func TestCopyDestinationCloseError(t *testing.T) {
	src, err := newMemFS("memory://src")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, src)()
	f, err := src.Create("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("data"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	mem, err := newMemFS("memory://dst")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, mem)()
	wantErr := errors.New("flush failed")
	dst := &closeErrorFS{WriteFS: mem, err: wantErr}

	if err := Copy(src, "file.txt", dst, "file.txt"); !errors.Is(err, wantErr) {
		t.Errorf("Copy() = %v, want %v", err, wantErr)
	}
}

func TestCopyCreateError(t *testing.T) {
	src, err := newMemFS("memory://src")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := src.Close(); err != nil {
			t.Errorf("failed to close src FS: %v", err)
		}
	}()
	f, err := src.Create("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := f.WriteString("data"); err != nil {
		t.Fatal(err)
	} else if n != len("data") {
		t.Fatalf("WriteString() = %d, want %d", n, len("data"))
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	dst, err := newAngryFS("angry://")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dst.Close(); err == nil {
			t.Error("want angryFS to fail Close(), got nil error")
		}
	}()
	if err := Copy(src, "file.txt", dst, "file.txt"); err == nil {
		t.Error("Copy with angry dst succeeded, want error")
	}
}

// --- List ---

func TestList(t *testing.T) {
	fsys := setupListFS(t)

	got, err := List(fsys, pathutil.CwdPath)
	if err != nil {
		t.Fatalf("List() = %v, want nil", err)
	}
	want := []string{"a.txt", "dir", "dir/b.txt", "dir/c.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("List() mismatch (-want +got):\n%s", diff)
	}
}

func TestListSubdir(t *testing.T) {
	fsys := setupListFS(t)

	got, err := List(fsys, "dir")
	if err != nil {
		t.Fatalf("List(dir) = %v, want nil", err)
	}
	want := []string{"dir", "dir/b.txt", "dir/c.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("List(dir) mismatch (-want +got):\n%s", diff)
	}
}

// --- ListFiles ---

func TestListFiles(t *testing.T) {
	fsys := setupListFS(t)

	got, err := ListFiles(fsys, pathutil.CwdPath)
	if err != nil {
		t.Fatalf("ListFiles() = %v, want nil", err)
	}
	want := []string{"a.txt", "dir/b.txt", "dir/c.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ListFiles() mismatch (-want +got):\n%s", diff)
	}
}

// listFilenamesFS implements the optional ListFilenames interface.
type listFilenamesFS struct {
	ufs.WriteFS
	files []string
}

func (lf *listFilenamesFS) ListFilenames(_ string) ([]string, error) {
	return lf.files, nil
}

func TestListFilesInterface(t *testing.T) {
	inner, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := inner.Close(); err != nil {
			t.Errorf("failed to close inner FS: %v", err)
		}
	}()

	want := []string{"fast.txt", "path.txt"}
	fsys := &listFilenamesFS{WriteFS: inner, files: want}

	got, err := ListFiles(fsys, pathutil.CwdPath)
	if err != nil {
		t.Fatalf("ListFiles() via interface = %v, want nil", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ListFiles() via interface mismatch (-want +got):\n%s", diff)
	}
}

// --- ForEachFilename ---

func TestForEachFilename(t *testing.T) {
	fsys := setupListFS(t)

	var got []string
	err := ForEachFilename(fsys, pathutil.CwdPath, func(name string) error {
		got = append(got, name)
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachFilename() = %v, want nil", err)
	}
	want := []string{"a.txt", "dir/b.txt", "dir/c.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ForEachFilename() mismatch (-want +got):\n%s", diff)
	}
}

// forEachFilenameFS implements the optional ForEachFilenameIter interface.
type forEachFilenameFS struct {
	ufs.WriteFS
	files []string
}

func (f *forEachFilenameFS) ForEachFilename(_ string, fn func(string) error) error {
	for _, file := range f.files {
		if err := fn(file); err != nil {
			return err
		}
	}
	return nil
}

func TestForEachFilenameInterface(t *testing.T) {
	inner, err := newMemFS("memory://test")
	if err != nil {
		t.Errorf("newMemFS returned an error, %s", err)
	}
	defer func() {
		if err := inner.Close(); err != nil {
			t.Errorf("failed to close inner FS: %v", err)
		}
	}()

	want := []string{"fast.txt", "path.txt"}
	fsys := &forEachFilenameFS{WriteFS: inner, files: want}

	var got []string

	if err := ForEachFilename(fsys, pathutil.CwdPath, func(name string) error {
		got = append(got, name)
		return nil
	}); err != nil {
		t.Fatalf("ForEachFilename() via interface = %v, want nil", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ForEachFilename() via interface mismatch (-want +got):\n%s", diff)
	}
}

func TestForEachFilenameCallbackError(t *testing.T) {
	fsys := setupListFS(t)
	sentinel := errors.New("stop")

	count := 0
	err := ForEachFilename(fsys, pathutil.CwdPath, func(_ string) error {
		count++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("ForEachFilename() = %v, want sentinel error", err)
	}
	if count != 1 {
		t.Errorf("callback called %d times, want 1", count)
	}
}

// --- ForEachFileInfo ---

func TestForEachFileInfo(t *testing.T) {
	fsys := setupListFS(t)

	var gotNames []string
	err := ForEachFileInfo(fsys, pathutil.CwdPath, func(info fs.FileInfo) error {
		gotNames = append(gotNames, info.Name())
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachFileInfo() = %v, want nil", err)
	}
	// fs.Stat returns the basename; sorted file paths are a.txt, dir/b.txt, dir/c.txt
	want := []string{"a.txt", "b.txt", "c.txt"}
	if diff := cmp.Diff(want, gotNames); diff != "" {
		t.Errorf("ForEachFileInfo() mismatch (-want +got):\n%s", diff)
	}
}

// forEachFileInfoFS implements the optional ForEachFileInfoIter interface.
type forEachFileInfoFS struct {
	ufs.WriteFS
	infos []fs.FileInfo
}

func (f *forEachFileInfoFS) ForEachFileInfo(_ string, fn func(fs.FileInfo) error) error {
	for _, info := range f.infos {
		if err := fn(info); err != nil {
			return err
		}
	}
	return nil
}

func TestForEachFileInfoInterface(t *testing.T) {
	inner, err := newMemFS("memory://test")
	if err != nil {
		t.Errorf("newMemFS returned an error, %s", err)
	}
	defer func() {
		if err := inner.Close(); err != nil {
			t.Errorf("failed to close inner FS: %v", err)
		}
	}()

	wantInfos := []fs.FileInfo{
		ufs.NewFileInfo("fast.txt", 10, fs.ModePerm, time.Time{}),
		ufs.NewFileInfo("path.txt", 20, fs.ModePerm, time.Time{}),
	}
	fsys := &forEachFileInfoFS{WriteFS: inner, infos: wantInfos}

	var gotNames []string
	if err := ForEachFileInfo(fsys, pathutil.CwdPath, func(info fs.FileInfo) error {
		gotNames = append(gotNames, info.Name())
		return nil
	}); err != nil {
		t.Fatalf("ForEachFileInfo() via interface = %v, want nil", err)
	}
	want := []string{"fast.txt", "path.txt"}
	if diff := cmp.Diff(want, gotNames); diff != "" {
		t.Errorf("ForEachFileInfo() via interface mismatch (-want +got):\n%s", diff)
	}
}

func TestForEachFileInfoCallbackError(t *testing.T) {
	fsys := setupListFS(t)
	sentinel := errors.New("stop")

	count := 0
	err := ForEachFileInfo(fsys, pathutil.CwdPath, func(_ fs.FileInfo) error {
		count++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("ForEachFileInfo() = %v, want sentinel error", err)
	}
	if count != 1 {
		t.Errorf("callback called %d times, want 1", count)
	}
}

// setupNestFSWithArchive creates a temp directory containing a regular file
// and a zip archive with one entry, then wraps it as a nestFS for Scan tests.
func setupNestFSWithArchive(t *testing.T) ufs.WriteFS {
	t.Helper()
	dir := t.TempDir()

	if err := osutil.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello")); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(dir, "data.zip")
	zf, err := osutil.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("inside.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "content"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}

	nfs, err := ufs.New(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := nfs.Close(); err != nil {
			t.Errorf("failed to close nest FS: %v", err)
		}
	})
	return nfs
}

// --- Scan ---

func TestWalk(t *testing.T) {
	fsys := setupListFS(t)

	var got []string
	err := Walk(fsys, pathutil.CwdPath, WalkArgs{}, func(name string) error {
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
	err := Walk(fsys, pathutil.CwdPath, WalkArgs{ExcludeDirectory: nil}, func(name string) error {
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
	err := Walk(fsys, pathutil.CwdPath, WalkArgs{ExcludeDirectory: []string{"dir"}}, func(name string) error {
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
	err := Walk(fsys, pathutil.CwdPath, WalkArgs{ExcludeDirectory: []string{"d*"}}, func(name string) error {
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
	err := Walk(nfs, pathutil.CwdPath, WalkArgs{}, func(name string) error {
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
		wrap func(t *testing.T, fsys ufs.WriteFS) fs.FS
	}{
		{name: "readOnly", wrap: func(_ *testing.T, fsys ufs.WriteFS) fs.FS { return readonlyfs.New(fsys) }},
		{name: "readWrap", wrap: func(_ *testing.T, fsys ufs.WriteFS) fs.FS { return readwrapfs.FromFS(fsys) }},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := tc.wrap(t, setupNestFSWithArchive(t))

			var got []string
			err := Walk(fsys, pathutil.CwdPath, WalkArgs{}, func(name string) error {
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
	err := Walk(nfs, pathutil.CwdPath, WalkArgs{IncludeMountedArchive: true}, func(name string) error {
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
	err := Walk(fsys, pathutil.CwdPath, WalkArgs{}, func(_ string) error {
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
	dir := t.TempDir()

	if err := osutil.MkdirAll(filepath.Join(dir, "subdir")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.WriteFile(filepath.Join(dir, "subdir", "nested.txt"), []byte("nested")); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(dir, "data.zip")
	zf, err := osutil.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("inside.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "content"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}

	nfs, err := ufs.New(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := nfs.Close(); err != nil {
			t.Errorf("nfs.Close() = %v", err)
		}
	})

	var got []string
	err = Walk(nfs, pathutil.CwdPath, WalkArgs{}, func(name string) error {
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

// --- Remove ---

func setupRemoveFS(t *testing.T) ufs.WriteFS {
	t.Helper()
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "dir/b.txt"} {
		f, err := fsys.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return fsys
}

func TestRemove(t *testing.T) {
	fsys := setupRemoveFS(t)

	if err := Remove(fsys, "a.txt"); err != nil {
		t.Fatalf("Remove('a.txt') = %v, want nil", err)
	}
	if _, err := fsys.Stat("a.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("after Remove, Stat('a.txt') = %v, want ErrNotExist", err)
	}
}

func TestRemoveNotExist(t *testing.T) {
	fsys := setupRemoveFS(t)

	err := Remove(fsys, "ghost.txt")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Remove(nonexistent) = %v, want ErrNotExist", err)
	}
}

func TestRemoveNonEmptyDir(t *testing.T) {
	fsys := setupRemoveFS(t)

	err := Remove(fsys, "dir")
	if err == nil {
		t.Error("Remove(non-empty dir) succeeded, want error")
	}
}

// noRemoverFS wraps an fs.FS without exposing the Remover interface, allowing
// tests to exercise the ErrPermission fallback path in Remove/RemoveAll.
type noRemoverFS struct{ fs.FS }

func TestRemoveFallback(t *testing.T) {
	inner, err := newMemFS("memory://test")
	if err != nil {
		t.Errorf("newMemFS returned an error, %s", err)
	}

	defer func() {
		if err := inner.Close(); err != nil {
			t.Errorf("failed to close inner FS: %v", err)
		}
	}()

	if err := Remove(&noRemoverFS{inner}, "any.txt"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Remove on non-Remover FS = %v, want ErrPermission", err)
	}
}

func TestRemoveAngry(t *testing.T) {
	fsys, err := newAngryFS("angry://")
	if err != nil {
		t.Fatal(err)
	}
	if err := Remove(fsys, "file.txt"); err == nil {
		t.Error("Remove on angry FS succeeded, want error")
	}
}

func TestRemoveNull(t *testing.T) {
	fsys, err := ufs.New(t.Context(), "null://")
	if err != nil {
		t.Fatal(err)
	}
	if err := Remove(fsys, "file.txt"); err != nil {
		t.Errorf("Remove on nullFS = %v, want nil", err)
	}
}

// --- RemoveAll ---

func TestRemoveAll(t *testing.T) {
	fsys := setupRemoveFS(t)

	if err := RemoveAll(fsys, "dir"); err != nil {
		t.Fatalf("RemoveAll('dir') = %v, want nil", err)
	}
	files, err := ListFiles(fsys, pathutil.CwdPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.txt"}
	if diff := cmp.Diff(want, files); diff != "" {
		t.Errorf("after RemoveAll('dir') files mismatch (-want +got):\n%s", diff)
	}
}

func TestRemoveAllNotExist(t *testing.T) {
	fsys := setupRemoveFS(t)

	// RemoveAll on a non-existent path must succeed (no-op).
	if err := RemoveAll(fsys, "ghost"); err != nil {
		t.Errorf("RemoveAll(nonexistent) = %v, want nil", err)
	}
}

func TestRemoveAllRoot(t *testing.T) {
	fsys := setupRemoveFS(t)

	if err := RemoveAll(fsys, pathutil.CwdPath); err != nil {
		t.Fatalf("RemoveAll('.') = %v, want nil", err)
	}
	files, err := ListFiles(fsys, pathutil.CwdPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("after RemoveAll('.'), expected empty FS, got: %v", files)
	}
}

func TestRemoveAllFallback(t *testing.T) {
	inner, err := newMemFS("memory://test")
	if err != nil {
		t.Errorf("newMemFS returned an error, %s", err)
	}
	defer func() {
		if err := inner.Close(); err != nil {
			t.Errorf("failed to close inner FS: %v", err)
		}
	}()

	if err := RemoveAll(&noRemoverFS{inner}, "dir"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("RemoveAll on non-Remover FS = %v, want ErrPermission", err)
	}
}

func TestRemoveAllAngry(t *testing.T) {
	fsys, err := newAngryFS("angry://")
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveAll(fsys, "dir"); err == nil {
		t.Error("RemoveAll on angry FS succeeded, want error")
	}
}

func TestRemoveAllNull(t *testing.T) {
	fsys, err := ufs.New(t.Context(), "null://")
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveAll(fsys, "dir"); err != nil {
		t.Errorf("RemoveAll on nullFS = %v, want nil", err)
	}
}
