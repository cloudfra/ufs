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
	"testing"
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
	t.Cleanup(func() { fsys.Close() })
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
	fsys.Close()
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
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll = %v, want nil", err)
	}
	if len(data) == 0 {
		t.Error("Open(\"index.html\") returned empty file, want non-empty")
	}
}

// TestArchiveFSInvalidPaths verifies that every mutating and reading operation
// rejects malformed paths with an error.
func TestArchiveFSInvalidPaths(t *testing.T) {
	fsys := mustArchiveFS(t)
	rfs, ok := fsys.(fs.ReadFileFS)
	if !ok {
		t.Fatal("archiveFS does not implement fs.ReadFileFS")
	}
	rds, ok := fsys.(fs.ReadDirFS)
	if !ok {
		t.Fatal("archiveFS does not implement fs.ReadDirFS")
	}

	invalidPaths := []string{"/absolute/path", "../relative/path", "invalid/../path"}
	ops := []struct {
		name string
		fn   func(string) error
	}{
		{"Open", func(p string) error { _, err := fsys.Open(p); return err }},
		{"Create", func(p string) error { _, err := fsys.Create(p); return err }},
		{"MkdirAll", func(p string) error { return fsys.MkdirAll(p, fs.ModePerm) }},
		{"ReadFile", func(p string) error { _, err := rfs.ReadFile(p); return err }},
		{"ReadDir", func(p string) error { _, err := rds.ReadDir(p); return err }},
		{"ReadLink", func(p string) error { _, err := fsys.ReadLink(p); return err }},
		{"Lstat", func(p string) error { _, err := fsys.Lstat(p); return err }},
	}

	for _, op := range ops {
		for _, path := range invalidPaths {
			t.Run(op.name+"/"+path, func(t *testing.T) {
				if err := op.fn(path); err == nil {
					t.Errorf("%s(%q) succeeded, want error", op.name, path)
				}
			})
		}
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

// TestArchiveFSReadDir verifies that ReadDir returns at least one entry for
// both the root and a named subdirectory.
func TestArchiveFSReadDir(t *testing.T) {
	fsys := mustArchiveFS(t)
	rds, ok := fsys.(fs.ReadDirFS)
	if !ok {
		t.Fatal("archiveFS does not implement fs.ReadDirFS")
	}

	for _, dir := range []string{cwdPath, "assets"} {
		t.Run(dir, func(t *testing.T) {
			entries, err := rds.ReadDir(dir)
			if err != nil {
				t.Fatalf("ReadDir(%q) = %v, want nil", dir, err)
			}
			if len(entries) == 0 {
				t.Errorf("ReadDir(%q) returned no entries, want at least one", dir)
			}
		})
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
