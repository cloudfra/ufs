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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRsync(t *testing.T) {
	srcFS, err := newLocalFS(testLocalFSName)
	if err != nil {
		t.Fatalf("cannot mount localFS(%q), %s", testLocalFSName, err)
	}
	for _, fsysTC := range getAllRegularTestCaseList() {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)
			if err := Rsync(srcFS, fsys, cwdPath); err != nil {
				t.Errorf("rsync failed with error, %s", err)
			}

			err := ForEachFilename(srcFS, cwdPath, true, func(name string) error {
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
	srcFS, err := newLocalFS(testLocalFSName)
	if err != nil {
		t.Fatalf("cannot mount localFS(%q), %s", testLocalFSName, err)
	}

	destFS := makeAngryFS(angryFSPrefix)

	if err := Rsync(srcFS, destFS, cwdPath); err == nil {
		t.Error("rsync expected to fail got nil error")
	}
}

func TestRsyncNull(t *testing.T) {
	srcFS, err := newLocalFS(testLocalFSName)
	if err != nil {
		t.Fatalf("cannot mount localFS(%q), %s", testLocalFSName, err)
	}

	destFS := mustNullFS(t)

	if err := Rsync(srcFS, destFS, cwdPath); err != nil {
		t.Errorf("rsync expected to succeed, failed with error: %s", err)
	}

	entries, err := destFS.ReadDir(cwdPath)
	if err != nil {
		t.Error(err)
	}
	if len(entries) > 0 {
		t.Errorf("nullFS should have 0 entries, got: %v", entries)
	}
}

// setupListFS creates a memFS with: a.txt, dir/b.txt, dir/c.txt.
func setupListFS(t *testing.T) FS {
	t.Helper()
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fsys.Close() })
	if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "dir/b.txt", "dir/c.txt"} {
		f, err := fsys.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	return fsys
}

// --- Copy ---

func TestCopy(t *testing.T) {
	src, _ := newMemFS("memory://src")
	dst, _ := newMemFS("memory://dst")
	defer src.Close()
	defer dst.Close()

	f, _ := src.Create("hello.txt")
	f.WriteString("hello world")
	f.Close()

	if err := Copy(src, "hello.txt", dst, "copy.txt"); err != nil {
		t.Fatalf("Copy() = %v, want nil", err)
	}

	rf, err := dst.Open("copy.txt")
	if err != nil {
		t.Fatalf("Open(copy.txt) = %v, want nil", err)
	}
	defer rf.Close()
	data, err := io.ReadAll(rf)
	if err != nil {
		t.Fatalf("ReadAll() = %v, want nil", err)
	}
	if string(data) != "hello world" {
		t.Errorf("copy content = %q, want %q", data, "hello world")
	}
}

func TestCopyOpenError(t *testing.T) {
	src, _ := newAngryFS("angry://")
	dst, _ := newMemFS("memory://dst")
	defer dst.Close()

	if err := Copy(src, "file.txt", dst, "file.txt"); err == nil {
		t.Error("Copy with angry src succeeded, want error")
	}
}

func TestCopyCreateError(t *testing.T) {
	src, _ := newMemFS("memory://src")
	defer src.Close()
	f, _ := src.Create("file.txt")
	f.WriteString("data")
	f.Close()

	dst, _ := newAngryFS("angry://")
	if err := Copy(src, "file.txt", dst, "file.txt"); err == nil {
		t.Error("Copy with angry dst succeeded, want error")
	}
}

// --- List ---

func TestList(t *testing.T) {
	fsys := setupListFS(t)

	got, err := List(fsys, cwdPath)
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

	got, err := ListFiles(fsys, cwdPath)
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
	FS
	files []string
}

func (lf *listFilenamesFS) ListFilenames(_ string) ([]string, error) {
	return lf.files, nil
}

func TestListFilesInterface(t *testing.T) {
	inner, _ := newMemFS("memory://test")
	defer inner.Close()
	want := []string{"fast.txt", "path.txt"}
	fsys := &listFilenamesFS{FS: inner, files: want}

	got, err := ListFiles(fsys, cwdPath)
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
	err := ForEachFilename(fsys, cwdPath, true, func(name string) error {
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
	FS
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
	inner, _ := newMemFS("memory://test")
	defer inner.Close()
	want := []string{"fast.txt", "path.txt"}
	fsys := &forEachFilenameFS{FS: inner, files: want}

	var got []string
	err := ForEachFilename(fsys, cwdPath, true, func(name string) error {
		got = append(got, name)
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachFilename() via interface = %v, want nil", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ForEachFilename() via interface mismatch (-want +got):\n%s", diff)
	}
}

func TestForEachFilenameCallbackError(t *testing.T) {
	for _, traverseArchives := range []bool{true, false} {
		t.Run(fmt.Sprintf("traverseArchives=%v", traverseArchives), func(t *testing.T) {
			fsys := setupListFS(t)
			sentinel := errors.New("stop")
			count := 0
			err := ForEachFilename(fsys, cwdPath, traverseArchives, func(_ string) error {
				count++
				return sentinel
			})
			if !errors.Is(err, sentinel) {
				t.Errorf("ForEachFilename(traverseArchives=%v) = %v, want sentinel error", traverseArchives, err)
			}
			if count != 1 {
				t.Errorf("callback called %d times, want 1", count)
			}
		})
	}
}

func TestForEachFilenameFalseSkipsVirtualMounts(t *testing.T) {
	// nestFS over cwd exposes virtual *.d directories for archives.
	// traverseArchives=false should skip them; traverseArchives=true should traverse into them.
	fsys, err := newNestFS(t.Context(), cwdPath)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	archivesDir := "testing/testassets/archives"

	var withBook, noBook []string
	if err := ForEachFilename(fsys, archivesDir, true, func(name string) error {
		withBook = append(withBook, name)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ForEachFilename(fsys, archivesDir, false, func(name string) error {
		noBook = append(noBook, name)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if len(withBook) <= len(noBook) {
		t.Errorf("traverseArchives=true returned %d files, traverseArchives=false returned %d; expected more with traverseArchives=true", len(withBook), len(noBook))
	}
	for _, name := range noBook {
		if strings.Contains(name, archiveDirExt+"/") {
			t.Errorf("traverseArchives=false traversed inside a virtual archive mount: %s", name)
		}
	}
}

func TestForEachFilenameFalsePreservesRealDotDDirs(t *testing.T) {
	// A .d directory with no adjacent archive is a real config directory and must
	// not be skipped.
	fsys, err := newMemFS("memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	if err := fsys.MkdirAll("apt.sources.d", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("apt.sources.d/custom.conf")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	var got []string
	if err := ForEachFilename(fsys, cwdPath, false, func(name string) error {
		got = append(got, name)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !containsString(got, "apt.sources.d/custom.conf") {
		t.Errorf("ForEachFilename(traverseArchives=false) skipped real .d directory, got: %v", got)
	}
}

func TestForEachFilenameFalseNoAdjacentArchive(t *testing.T) {
	// foo.zip.d exists but foo.zip does not — must not be skipped.
	fsys, err := newMemFS("memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	if err := fsys.MkdirAll("foo.zip.d", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("foo.zip.d/readme.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	var got []string
	if err := ForEachFilename(fsys, cwdPath, false, func(name string) error {
		got = append(got, name)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !containsString(got, "foo.zip.d/readme.txt") {
		t.Errorf("ForEachFilename(traverseArchives=false) incorrectly skipped .d directory with no adjacent archive, got: %v", got)
	}
}

// --- ForEachFileInfo ---

func TestForEachFileInfo(t *testing.T) {
	fsys := setupListFS(t)

	var gotNames []string
	err := ForEachFileInfo(fsys, cwdPath, true, func(info fs.FileInfo) error {
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
	FS
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
	inner, _ := newMemFS("memory://test")
	defer inner.Close()
	wantInfos := []fs.FileInfo{
		&fsInfo{name: "fast.txt", size: 10, mode: fs.ModePerm},
		&fsInfo{name: "path.txt", size: 20, mode: fs.ModePerm},
	}
	fsys := &forEachFileInfoFS{FS: inner, infos: wantInfos}

	var gotNames []string
	err := ForEachFileInfo(fsys, cwdPath, true, func(info fs.FileInfo) error {
		gotNames = append(gotNames, info.Name())
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachFileInfo() via interface = %v, want nil", err)
	}
	want := []string{"fast.txt", "path.txt"}
	if diff := cmp.Diff(want, gotNames); diff != "" {
		t.Errorf("ForEachFileInfo() via interface mismatch (-want +got):\n%s", diff)
	}
}

func TestForEachFileInfoCallbackError(t *testing.T) {
	for _, traverseArchives := range []bool{true, false} {
		t.Run(fmt.Sprintf("traverseArchives=%v", traverseArchives), func(t *testing.T) {
			fsys := setupListFS(t)
			sentinel := errors.New("stop")
			count := 0
			err := ForEachFileInfo(fsys, cwdPath, traverseArchives, func(_ fs.FileInfo) error {
				count++
				return sentinel
			})
			if !errors.Is(err, sentinel) {
				t.Errorf("ForEachFileInfo(traverseArchives=%v) = %v, want sentinel error", traverseArchives, err)
			}
			if count != 1 {
				t.Errorf("callback called %d times, want 1", count)
			}
		})
	}
}

func TestForEachFileInfoFalseSkipsVirtualMounts(t *testing.T) {
	fsys, err := newNestFS(t.Context(), cwdPath)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	archivesDir := "testing/testassets/archives"

	var withBook, noBook []fs.FileInfo
	if err := ForEachFileInfo(fsys, archivesDir, true, func(info fs.FileInfo) error {
		withBook = append(withBook, info)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ForEachFileInfo(fsys, archivesDir, false, func(info fs.FileInfo) error {
		noBook = append(noBook, info)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if len(withBook) <= len(noBook) {
		t.Errorf("traverseArchives=true returned %d files, traverseArchives=false returned %d; expected more with traverseArchives=true", len(withBook), len(noBook))
	}
}

func TestForEachFileInfoFalsePreservesRealDotDDirs(t *testing.T) {
	fsys, err := newMemFS("memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	if err := fsys.MkdirAll("apt.sources.d", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("apt.sources.d/custom.conf")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	var gotNames []string
	if err := ForEachFileInfo(fsys, cwdPath, false, func(info fs.FileInfo) error {
		gotNames = append(gotNames, info.Name())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !containsString(gotNames, "custom.conf") {
		t.Errorf("ForEachFileInfo(traverseArchives=false) skipped real .d directory, got: %v", gotNames)
	}
}

func containsString(slice []string, s string) bool {
	return slices.Contains(slice, s)
}
