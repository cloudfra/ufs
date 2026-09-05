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
	"io"
	"io/fs"
	"path"
	"testing"
	"testing/fstest"
)

const testArchive = "testing/testassets/archives/testassets.tar.gz"

func TestIsMountableArchivePath(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := isMountableArchivePath(tc.input); got != tc.wantIsMountableArchivePath {
				t.Errorf("isMountableArchivePath(%q) got: %v, want: %v", tc.input, got, tc.wantIsMountableArchivePath)
			}
		})
	}
}

func mustArchiveFS(t *testing.T) FS {
	t.Helper()
	fsys, err := newArchiveFSFromLocalFS(context.Background(), testArchive)
	if err != nil {
		t.Fatalf("newArchiveFSFromLocalFS(%q) = %v, want nil", testArchive, err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("failed to close archive FS: %v", err)
		}
	})
	return fsys
}

func TestNewArchiveFSFromLocalFS(t *testing.T) {
	fsys, err := newArchiveFSFromLocalFS(context.Background(), testArchive)
	if err != nil {
		t.Fatal(err)
	}
	if fsys == nil {
		t.Fatal("fsys is nil")
	}
	if err := fsys.Close(); err != nil {
		t.Errorf("failed to close archive FS: %v", err)
	}
}

func TestNewArchiveFSFromLocalFSInvalid(t *testing.T) {
	_, err := newArchiveFSFromLocalFS(context.Background(), "nonexistent-archive.tar.gz")
	if err == nil {
		t.Fatal("newArchiveFSFromLocalFS(nonexistent) = nil error, want error")
	}
}

func TestArchiveFSClose(t *testing.T) {
	fsys, err := newArchiveFSFromLocalFS(context.Background(), testArchive)
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestArchiveFSOpen(t *testing.T) {
	fsys := mustArchiveFS(t)

	f, err := fsys.Open("index.html")
	if err != nil {
		t.Fatalf("Open(\"index.html\") = %v, want nil", err)
	}
	defer validateClose(t, f)()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll = %v, want nil", err)
	}
	if len(data) == 0 {
		t.Error("Open(\"index.html\") returned empty file, want non-empty")
	}
}

func TestArchiveFSCreate(t *testing.T) {
	fsys := mustArchiveFS(t)

	_, err := fsys.Create("newfile.txt")
	if err == nil {
		t.Fatal("Create() = nil error, want ErrPermission")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Create() error = %v, want to wrap fs.ErrPermission", err)
	}
}

func TestArchiveFSMkdirAll(t *testing.T) {
	fsys := mustArchiveFS(t)

	err := fsys.MkdirAll("newdir", fs.ModePerm)
	if err == nil {
		t.Fatal("MkdirAll() = nil error, want ErrPermission")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("MkdirAll() error = %v, want to wrap fs.ErrPermission", err)
	}
}

func TestArchiveFSReadFile(t *testing.T) {
	fsys := mustArchiveFS(t)

	rfs, ok := fsys.(fs.ReadFileFS)
	if !ok {
		t.Fatal("archiveFS does not implement fs.ReadFileFS")
	}

	data, err := rfs.ReadFile("index.html")
	if err != nil {
		t.Fatalf("ReadFile(\"index.html\") = %v, want nil", err)
	}
	if len(data) == 0 {
		t.Error("ReadFile(\"index.html\") returned empty data, want non-empty")
	}
}

func TestArchiveFSReadDir(t *testing.T) {
	fsys := mustArchiveFS(t)

	rfs, ok := fsys.(fs.ReadDirFS)
	if !ok {
		t.Fatal("archiveFS does not implement fs.ReadDirFS")
	}

	entries, err := rfs.ReadDir(cwdPath)
	if err != nil {
		t.Fatalf("ReadDir(\".\") = %v, want nil", err)
	}
	if len(entries) == 0 {
		t.Error("ReadDir(\".\") returned no entries, want at least one")
	}
}

func TestArchiveFSReadDirSubdir(t *testing.T) {
	fsys := mustArchiveFS(t)

	rfs, ok := fsys.(fs.ReadDirFS)
	if !ok {
		t.Fatal("archiveFS does not implement fs.ReadDirFS")
	}

	entries, err := rfs.ReadDir("assets")
	if err != nil {
		t.Fatalf("ReadDir(\"assets\") = %v, want nil", err)
	}
	if len(entries) == 0 {
		t.Error("ReadDir(\"assets\") returned no entries, want at least one")
	}
}

func TestArchiveFSReadLink(t *testing.T) {
	fsys := mustArchiveFS(t)

	_, err := fsys.ReadLink("index.html")
	if err == nil {
		t.Fatal("ReadLink() = nil error, want error (archives have no symlinks)")
	}
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("ReadLink() error = %v, want to wrap fs.ErrInvalid", err)
	}
}

func TestArchiveFSLstat(t *testing.T) {
	fsys := mustArchiveFS(t)

	info, err := fsys.Lstat("index.html")
	if err != nil {
		t.Fatalf("Lstat(%q) = %v, want nil", "index.html", err)
	}
	if info == nil {
		t.Fatal("Lstat() returned nil info")
	}
	if info.Name() != "index.html" {
		t.Errorf("Lstat().Name() = %q, want %q", info.Name(), "index.html")
	}
}

func TestArchiveFSStatNonExistent(t *testing.T) {
	fsys := mustArchiveFS(t)

	_, err := fsys.Stat("nonexistent-file-that-does-not-exist.txt")
	if err == nil {
		t.Fatal("Stat() = nil error, want error for nonexistent file")
	}
}

func TestArchiveFSRemove(t *testing.T) {
	fsys := mustArchiveFS(t)

	err := fsys.Remove("index.html")
	if err == nil {
		t.Fatal("Remove() = nil error, want ErrPermission")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Remove() error = %v, want to wrap fs.ErrPermission", err)
	}
}

func TestArchiveFSRemoveAll(t *testing.T) {
	fsys := mustArchiveFS(t)

	err := fsys.RemoveAll("assets")
	if err == nil {
		t.Fatal("RemoveAll() = nil error, want ErrPermission")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("RemoveAll() error = %v, want to wrap fs.ErrPermission", err)
	}
}

const testNoDirDeepArchive = "testing/testassets/archives/nodir-deep-testassets.zip"

// noDirDeepFiles lists every file entry in testNoDirDeepArchive. The zip
// contains only these file entries — no explicit directory entries — so
// every directory in the tree (deep, deep/x, deep/x/y, deep/z, onetwothree)
// is implicit. See Makefile_testassets.mk for how the archive is built.
var noDirDeepFiles = []string{
	"deep/5.txt",
	"deep/x/3.txt",
	"deep/x/y/1.txt",
	"deep/x/y/2.txt",
	"deep/z/4.txt",
	"onetwothree/1.txt",
	"onetwothree/2.txt",
}

var noDirDeepDirs = map[string][]string{
	".":           {"deep", "onetwothree"},
	"deep":        {"5.txt", "x", "z"},
	"deep/x":      {"3.txt", "y"},
	"deep/x/y":    {"1.txt", "2.txt"},
	"deep/z":      {"4.txt"},
	"onetwothree": {"1.txt", "2.txt"},
}

func mustNoDirDeepArchiveFS(t *testing.T) FS {
	t.Helper()
	fsys, err := newArchiveFSFromLocalFS(context.Background(), testNoDirDeepArchive)
	if err != nil {
		t.Fatalf("newArchiveFSFromLocalFS(%q) = %v, want nil", testNoDirDeepArchive, err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("failed to close archive FS: %v", err)
		}
	})
	return fsys
}

func TestArchiveFSImplicitDirStatName(t *testing.T) {
	t.Parallel()
	fsys := mustNoDirDeepArchiveFS(t)

	for dir := range noDirDeepDirs {
		if dir == "." {
			continue
		}
		t.Run(dir, func(t *testing.T) {
			info, err := fsys.Stat(dir)
			if err != nil {
				t.Fatalf("Stat(%q) = %v", dir, err)
			}
			if !info.IsDir() {
				t.Errorf("Stat(%q).IsDir() = false, want true", dir)
			}
			if got := info.Name(); got != path.Base(dir) {
				t.Errorf("Stat(%q).Name() = %q, want %q", dir, got, path.Base(dir))
			}
		})
	}
}

func TestArchiveFSImplicitDirOpenStatName(t *testing.T) {
	t.Parallel()
	fsys := mustNoDirDeepArchiveFS(t)

	for dir := range noDirDeepDirs {
		if dir == "." {
			continue
		}
		t.Run(dir, func(t *testing.T) {
			f, err := fsys.Open(dir)
			if err != nil {
				t.Fatalf("Open(%q) = %v", dir, err)
			}
			defer validateClose(t, f)()

			info, err := f.Stat()
			if err != nil {
				t.Fatalf("Open(%q).Stat() = %v", dir, err)
			}
			if !info.IsDir() {
				t.Errorf("Open(%q).Stat().IsDir() = false, want true", dir)
			}
			if got := info.Name(); got != path.Base(dir) {
				t.Errorf("Open(%q).Stat().Name() = %q, want %q", dir, got, path.Base(dir))
			}
		})
	}
}

func TestArchiveFSImplicitDirReadDir(t *testing.T) {
	t.Parallel()
	fsys := mustNoDirDeepArchiveFS(t)

	for dir, wantNames := range noDirDeepDirs {
		t.Run(dir, func(t *testing.T) {
			assertDir(t, fsys, dir, wantNames)
		})
	}
}

func TestArchiveFSImplicitDirWalkDir(t *testing.T) {
	t.Parallel()
	fsys := mustNoDirDeepArchiveFS(t)

	visited := map[string]bool{}
	err := fs.WalkDir(fsys, ".", func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		visited[p] = true
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir = %v", err)
	}

	for dir := range noDirDeepDirs {
		if !visited[dir] {
			t.Errorf("WalkDir did not visit directory %q", dir)
		}
	}
	for _, filePath := range noDirDeepFiles {
		if !visited[filePath] {
			t.Errorf("WalkDir did not visit file %q", filePath)
		}
	}
}

func TestArchiveFSImplicitDirReadFile(t *testing.T) {
	t.Parallel()
	fsys := mustNoDirDeepArchiveFS(t)

	for _, filePath := range noDirDeepFiles {
		t.Run(filePath, func(t *testing.T) {
			got, err := fs.ReadFile(fsys, filePath)
			if err != nil {
				t.Fatalf("ReadFile(%q) = %v", filePath, err)
			}
			if len(got) == 0 {
				t.Errorf("ReadFile(%q) returned empty content", filePath)
			}
		})
	}
}

func TestArchiveFSImplicitDirLstatName(t *testing.T) {
	t.Parallel()
	fsys := mustNoDirDeepArchiveFS(t)

	for dir := range noDirDeepDirs {
		if dir == "." {
			continue
		}
		t.Run(dir, func(t *testing.T) {
			info, err := fsys.Lstat(dir)
			if err != nil {
				t.Fatalf("Lstat(%q) = %v", dir, err)
			}
			if got := info.Name(); got != path.Base(dir) {
				t.Errorf("Lstat(%q).Name() = %q, want %q", dir, got, path.Base(dir))
			}
		})
	}
}

func TestArchiveFSImplicitDirFSTestConformance(t *testing.T) {
	t.Parallel()
	fsys := mustNoDirDeepArchiveFS(t)

	if err := fstest.TestFS(fsys, noDirDeepFiles...); err != nil {
		t.Errorf("fstest.TestFS = %v", err)
	}
}

// TestArchiveFSInvalidPaths verifies that every FS operation on archiveFS
// rejects paths that fail fs.ValidPath.
func TestArchiveFSInvalidPaths(t *testing.T) {
	invalidPaths := []string{
		"/absolute/path",
		"../relative/path",
		"invalid/../path",
	}

	tests := []struct {
		name string
		op   func(fsys FS, path string) error
	}{
		{"Open", func(fsys FS, path string) error {
			_, err := fsys.Open(path)
			return err
		}},
		{"Create", func(fsys FS, path string) error {
			_, err := fsys.Create(path)
			return err
		}},
		{"MkdirAll", func(fsys FS, path string) error {
			return fsys.MkdirAll(path, fs.ModePerm)
		}},
		{"Remove", func(fsys FS, path string) error {
			return fsys.Remove(path)
		}},
		{"RemoveAll", func(fsys FS, path string) error {
			return fsys.RemoveAll(path)
		}},
		{"ReadFile", func(fsys FS, path string) error {
			_, err := fsys.(fs.ReadFileFS).ReadFile(path)
			return err
		}},
		{"ReadDir", func(fsys FS, path string) error {
			_, err := fsys.(fs.ReadDirFS).ReadDir(path)
			return err
		}},
		{"ReadLink", func(fsys FS, path string) error {
			_, err := fsys.ReadLink(path)
			return err
		}},
		{"Lstat", func(fsys FS, path string) error {
			_, err := fsys.Lstat(path)
			return err
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fsys := mustArchiveFS(t)
			for _, path := range invalidPaths {
				t.Run(path, func(t *testing.T) {
					if err := tc.op(fsys, path); err == nil {
						t.Errorf("%s(%q) succeeded, want error", tc.name, path)
					}
				})
			}
		})
	}
}
