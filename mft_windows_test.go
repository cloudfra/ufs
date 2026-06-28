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

//go:build windows

package ufs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// createTestTree creates a standard directory tree for integration tests.
func createTestTree(t *testing.T, root string) {
	t.Helper()
	dirs := []string{"a", "a/b", "a/b/c", "d"}
	files := []string{"top.txt", "a/one.txt", "a/b/two.txt", "a/b/c/three.txt", "d/four.txt"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(root, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// walkDirFiles returns all file paths under fsys via fs.WalkDir, sorted.
func walkDirFiles(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	var files []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == "." || d.IsDir() {
			return err
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

// ---- newMFTScanner ---------------------------------------------------------

func TestNewMFTScannerEmptyPath(t *testing.T) {
	t.Parallel()
	_, err := newMFTScanner("")
	if err == nil {
		t.Fatal("newMFTScanner(\"\") = nil, want error")
	}
	if !errors.Is(err, errMFTUnavailable) {
		t.Errorf("error = %v, want errMFTUnavailable", err)
	}
}

// ---- localFS iterator interface conformance --------------------------------

func TestLocalFSIteratorInterfaceConformance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	var _ ForEachFilenameIter = fsys
	var _ ForEachFileInfoIter = fsys
	var _ ListFilenames = fsys
}

// ---- WalkDir fallback tests (no admin needed) ------------------------------

func TestLocalFSForEachFilenameWalkDirFallback(t *testing.T) {
	tmpDir := t.TempDir()
	absPath, err := filepath.Abs(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	createTestTree(t, absPath)

	lfs, err := makeLocalFS("file://" + filepath.ToSlash(absPath))
	if err != nil {
		t.Fatal(err)
	}
	defer lfs.Close()

	var got []string
	if err := lfs.forEachFilenameWalkDir(".", func(name string) error {
		got = append(got, name)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := walkDirFiles(t, lfs)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("WalkDir fallback mismatch (-want +got):\n%s", diff)
	}
}

func TestLocalFSForEachFileInfoWalkDirFallback(t *testing.T) {
	tmpDir := t.TempDir()
	absPath, err := filepath.Abs(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	createTestTree(t, absPath)

	lfs, err := makeLocalFS("file://" + filepath.ToSlash(absPath))
	if err != nil {
		t.Fatal(err)
	}
	defer lfs.Close()

	var names []string
	if err := lfs.forEachFileInfoWalkDir(".", func(info fs.FileInfo) error {
		names = append(names, info.Name())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	want := []string{"four.txt", "one.txt", "three.txt", "top.txt", "two.txt"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("forEachFileInfoWalkDir() name mismatch (-want +got):\n%s", diff)
	}
}

func TestLocalFSListFilenamesWalkDirFallback(t *testing.T) {
	tmpDir := t.TempDir()
	absPath, err := filepath.Abs(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	createTestTree(t, absPath)

	lfs, err := makeLocalFS("file://" + filepath.ToSlash(absPath))
	if err != nil {
		t.Fatal(err)
	}
	defer lfs.Close()

	got, err := lfs.listFilenamesWalkDir(".")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := walkDirFiles(t, lfs)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("listFilenamesWalkDir() mismatch (-want +got):\n%s", diff)
	}
}

// ---- callback error propagation --------------------------------------------

func TestLocalFSForEachFilenameCallbackError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	sentinel := errors.New("stop")
	err = fsys.ForEachFilename(cwdPath, func(string) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("ForEachFilename() = %v, want sentinel error", err)
	}
}

func TestLocalFSForEachFileInfoCallbackError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	sentinel := errors.New("stop")
	err = fsys.ForEachFileInfo(cwdPath, func(fs.FileInfo) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("ForEachFileInfo() = %v, want sentinel error", err)
	}
}

// ---- ListFilenames edge cases ----------------------------------------------

func TestLocalFSListFilenamesEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	got, err := fsys.ListFilenames(cwdPath)
	if err != nil {
		t.Fatalf("ListFilenames() = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListFilenames(empty dir) = %v, want empty", got)
	}
}

func TestLocalFSListFilenamesSorted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"z.txt", "a.txt", "m.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	got, err := fsys.ListFilenames(cwdPath)
	if err != nil {
		t.Fatalf("ListFilenames() = %v", err)
	}
	want := []string{"a.txt", "m.txt", "z.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ListFilenames() not sorted (-want +got):\n%s", diff)
	}
}
