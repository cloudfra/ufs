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

func TestArchiveFSOpenInvalid(t *testing.T) {
	fsys := mustArchiveFS(t)

	invalidPaths := []string{
		"/absolute/path",
		"../relative/path",
		"invalid/../path",
	}
	for _, path := range invalidPaths {
		if _, err := fsys.Open(path); err == nil {
			t.Errorf("Open(%q) succeeded, want error", path)
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

func TestArchiveFSCreateInvalid(t *testing.T) {
	fsys := mustArchiveFS(t)

	invalidPaths := []string{
		"/absolute/path",
		"../relative/path",
		"invalid/../path",
	}
	for _, path := range invalidPaths {
		if _, err := fsys.Create(path); err == nil {
			t.Errorf("Create(%q) succeeded, want error", path)
		}
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

func TestArchiveFSMkdirAllInvalid(t *testing.T) {
	fsys := mustArchiveFS(t)

	invalidPaths := []string{
		"/absolute/path",
		"../relative/path",
		"invalid/../path",
	}
	for _, path := range invalidPaths {
		if err := fsys.MkdirAll(path, fs.ModePerm); err == nil {
			t.Errorf("MkdirAll(%q) succeeded, want error", path)
		}
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

func TestArchiveFSReadFileInvalid(t *testing.T) {
	fsys := mustArchiveFS(t)

	rfs, ok := fsys.(fs.ReadFileFS)
	if !ok {
		t.Fatal("archiveFS does not implement fs.ReadFileFS")
	}

	invalidPaths := []string{
		"/absolute/path",
		"../relative/path",
		"invalid/../path",
	}
	for _, path := range invalidPaths {
		if _, err := rfs.ReadFile(path); err == nil {
			t.Errorf("ReadFile(%q) succeeded, want error", path)
		}
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

func TestArchiveFSReadLinkInvalid(t *testing.T) {
	fsys := mustArchiveFS(t)

	for _, p := range []string{"/absolute/path", "../relative/path", "invalid/../path"} {
		if _, err := fsys.ReadLink(p); err == nil {
			t.Errorf("ReadLink(%q) succeeded, want error", p)
		}
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

func TestArchiveFSLstatInvalid(t *testing.T) {
	fsys := mustArchiveFS(t)

	for _, p := range []string{"/absolute/path", "../relative/path", "invalid/../path"} {
		if _, err := fsys.Lstat(p); err == nil {
			t.Errorf("Lstat(%q) succeeded, want error", p)
		}
	}
}

func TestArchiveFSStatNonExistent(t *testing.T) {
	fsys := mustArchiveFS(t)

	_, err := fsys.Stat("nonexistent-file-that-does-not-exist.txt")
	if err == nil {
		t.Fatal("Stat() = nil error, want error for nonexistent file")
	}
}

func TestArchiveFSReadDirInvalid(t *testing.T) {
	fsys := mustArchiveFS(t)

	rfs, ok := fsys.(fs.ReadDirFS)
	if !ok {
		t.Fatal("archiveFS does not implement fs.ReadDirFS")
	}

	invalidPaths := []string{
		"/absolute/path",
		"../relative/path",
		"invalid/../path",
	}
	for _, path := range invalidPaths {
		if _, err := rfs.ReadDir(path); err == nil {
			t.Errorf("ReadDir(%q) succeeded, want error", path)
		}
	}
}

const testSevenZipArchive = "testing/testassets/archives/testassets.7z"

func TestNewArchiveFSFrom7z(t *testing.T) {
	fsys, err := newArchiveFSFromLocalFS(context.Background(), testSevenZipArchive)
	if err != nil {
		t.Fatalf("newArchiveFSFromLocalFS(.7z) = %v, want nil", err)
	}
	defer fsys.Close()

	data, err := fsys.ReadFile("index.html")
	if err != nil {
		t.Fatalf("ReadFile(index.html) from .7z = %v, want nil", err)
	}
	if len(data) == 0 {
		t.Error("ReadFile returned empty, want non-empty")
	}
}

func TestNestFS7zFileIsSeekable(t *testing.T) {
	fsys, err := newNestFS(cwdPath)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()
	nfs := fsys.(*nestFS)

	f, err := nfs.Open("testing/testassets/archives/testassets.7z.d/index.html")
	if err != nil {
		t.Fatalf("Open(.7z.d/index.html) = %v", err)
	}
	defer f.Close()

	if _, ok := f.(io.ReadSeeker); !ok {
		t.Fatal("file from .7z does not implement io.ReadSeeker")
	}
}
