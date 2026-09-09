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
	"archive/zip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
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

//nolint:unused // only referenced by disabled reproduction tests below
const testNoDirArchive = "testing/testassets/archives/nodir-testassets.zip"

//nolint:unused // only referenced by disabled reproduction tests below
func mustNoDirArchiveFS(t *testing.T) FS {
	t.Helper()
	fsys, err := newArchiveFSFromLocalFS(context.Background(), testNoDirArchive)
	if err != nil {
		t.Fatalf("newArchiveFSFromLocalFS(%q) = %v, want nil", testNoDirArchive, err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("failed to close archive FS: %v", err)
		}
	})
	return fsys
}

// xTestArchiveFSOpenImplicitDir reproduces https://github.com/cloudfra/ufs/pull/250:
// Open() on an implicit directory (no explicit zip entry) fails to report
// IsDir() and does not return a fs.ReadDirFile before the archive has been
// indexed by a ReadDir call. Disabled (x-prefixed) until a fix lands.
//
//nolint:unused // disabled reproduction test, see comment above
func xTestArchiveFSOpenImplicitDir(t *testing.T) {
	fsys := mustNoDirArchiveFS(t)

	f, err := fsys.Open("onetwothree")
	if err != nil {
		t.Fatalf("Open(\"onetwothree\") = %v, want nil", err)
	}
	defer validateClose(t, f)()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}
	if !info.IsDir() {
		t.Error("Open(\"onetwothree\") should be a directory but IsDir() = false")
	}

	rdf, ok := f.(fs.ReadDirFile)
	if !ok {
		t.Fatal("Open(\"onetwothree\") did not return a ReadDirFile")
	}
	entries, err := rdf.ReadDir(-1)
	if err != nil {
		t.Fatalf("ReadDir(-1) = %v, want nil", err)
	}
	wantNames := []string{"1.txt", "2.txt", "3.txt"}
	gotNames := make([]string, 0, len(entries))
	for _, e := range entries {
		gotNames = append(gotNames, e.Name())
	}
	if len(gotNames) != len(wantNames) {
		t.Errorf("ReadDir got %v, want %v", gotNames, wantNames)
	}
	for i, want := range wantNames {
		if i < len(gotNames) && gotNames[i] != want {
			t.Errorf("entry[%d] = %q, want %q", i, gotNames[i], want)
		}
	}
}

// xTestArchiveFSStatImplicitDir reproduces https://github.com/cloudfra/ufs/pull/250:
// Stat() on an implicit directory reports IsDir() = false before the archive
// has been indexed. Disabled (x-prefixed) until a fix lands.
//
//nolint:unused // disabled reproduction test, see comment above
func xTestArchiveFSStatImplicitDir(t *testing.T) {
	fsys := mustNoDirArchiveFS(t)

	info, err := fsys.Stat("onetwothree")
	if err != nil {
		t.Fatalf("Stat(\"onetwothree\") = %v, want nil", err)
	}
	if !info.IsDir() {
		t.Error("Stat(\"onetwothree\") should be a directory but IsDir() = false")
	}
}

// xTestArchiveFSReadDirImplicitDir accompanies the other implicit-dir
// reproduction tests. Disabled (x-prefixed) alongside them for consistency.
//
//nolint:unused // disabled reproduction test, see comment above
func xTestArchiveFSReadDirImplicitDir(t *testing.T) {
	fsys := mustNoDirArchiveFS(t)

	rfs, ok := fsys.(fs.ReadDirFS)
	if !ok {
		t.Fatal("archiveFS does not implement fs.ReadDirFS")
	}

	entries, err := rfs.ReadDir("onetwothree")
	if err != nil {
		t.Fatalf("ReadDir(\"onetwothree\") = %v, want nil", err)
	}
	if len(entries) != 3 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("ReadDir(\"onetwothree\") got %d entries %v, want 3", len(entries), names)
	}
}

// createArchiveWithEntries builds a temp zip containing exactly the named
// entries and returns its path. An entry whose name ends with "/" becomes a
// directory entry; every other entry is a file carrying a small non-empty
// payload. The temp file is removed via t.Cleanup. This lets a test precisely
// control which directories are present as explicit entries (trailing slash)
// and which must be inferred from child file paths (implicit).
//
//nolint:unused // only referenced by disabled reproduction tests below
func createArchiveWithEntries(t *testing.T, entries ...string) string {
	t.Helper()

	tmp, err := os.CreateTemp("", "ufstest-*.zip")
	if err != nil {
		t.Fatalf("os.CreateTemp = %v, want nil", err)
	}
	tmpName := tmp.Name()
	t.Cleanup(func() {
		if err := os.Remove(tmpName); err != nil {
			t.Fatalf("os.Remove(%q) = %v, want nil", tmpName, err)
		}
	})

	zw := zip.NewWriter(tmp)
	for _, name := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip Create(%q) = %v, want nil", name, err)
		}
		if !strings.HasSuffix(name, "/") {
			if _, err := w.Write([]byte("ufstest-" + name)); err != nil {
				t.Fatalf("zip Write(%q) = %v, want nil", name, err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close = %v, want nil", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("temp file Close = %v, want nil", err)
	}
	return tmpName
}

// mustArchiveFromEntries mounts a freshly built zip (from the given entries) as
// an archiveFS and registers cleanup to close it, mirroring mustArchiveFS and
// mustNoDirArchiveFS but giving tests full control over the directory entries.
//
//nolint:unused // only referenced by disabled reproduction tests below
func mustArchiveFromEntries(t *testing.T, entries ...string) FS {
	t.Helper()

	zipPath := createArchiveWithEntries(t, entries...)
	fsys, err := newArchiveFSFromLocalFS(t.Context(), zipPath)
	if err != nil {
		t.Fatalf("newArchiveFSFromLocalFS(%q) = %v, want nil", zipPath, err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("failed to close archive FS: %v", err)
		}
	})
	return fsys
}

// TestArchiveFSImplicitDirMultipleLayers covers an archive with no explicit
// directory entries and several layers of nesting. Open and Stat on each
// implicit directory level must report a directory, and this must hold even
// when Open/Stat run before any ReadDir has populated the archive index.
// xTestArchiveFSImplicitDirMultipleLayers reproduces
// https://github.com/cloudfra/ufs/pull/250 across several layers of nested
// implicit directories. Disabled (x-prefixed) until a fix lands.
//
//nolint:unused // disabled reproduction test, see comment above
func xTestArchiveFSImplicitDirMultipleLayers(t *testing.T) {
	fsys := mustArchiveFromEntries(t,
		"deep/5.txt",
		"deep/x/3.txt",
		"deep/x/y/1.txt",
		"deep/x/y/2.txt",
		"deep/z/4.txt",
	)

	// Stat each implicit directory level; Stat exercises the lazy-index path
	// on a fresh archive before any ReadDir has occurred.
	for _, p := range []string{"deep", "deep/x", "deep/x/y"} {
		info, err := fsys.Stat(p)
		if err != nil {
			t.Fatalf("Stat(%q) = %v, want nil", p, err)
		}
		if !info.IsDir() {
			t.Errorf("Stat(%q).IsDir() = false, want true", p)
		}
	}

	// Open each implicit directory level and confirm it is a directory.
	for _, p := range []string{"deep", "deep/x", "deep/x/y"} {
		f, err := fsys.Open(p)
		if err != nil {
			t.Fatalf("Open(%q) = %v, want nil", p, err)
		}
		defer validateClose(t, f)()
		info, err := f.Stat()
		if err != nil {
			t.Fatalf("Open(%q).Stat() = %v, want nil", p, err)
		}
		if !info.IsDir() {
			t.Errorf("Open(%q).Stat().IsDir() = false, want true", p)
		}
	}

	// Directory listing at multiple levels.
	assertDir(t, fsys, "deep", []string{"5.txt", "x", "z"})
	assertDir(t, fsys, "deep/x", []string{"3.txt", "y"})
	assertDir(t, fsys, "deep/x/y", []string{"1.txt", "2.txt"})
	assertDir(t, fsys, "deep/z", []string{"4.txt"})

	// Files nested under implicit directories must still be readable.
	data, err := fs.ReadFile(fsys, "deep/x/y/1.txt")
	if err != nil {
		t.Fatalf("fs.ReadFile(\"deep/x/y/1.txt\") = %v, want nil", err)
	}
	if len(data) == 0 {
		t.Error("fs.ReadFile(\"deep/x/y/1.txt\") returned empty data, want non-empty")
	}
}

// TestArchiveFSImplicitDirMixedExplicit covers an archive where some
// directories have explicit entries and some do not. Listing and opening an
// implicit directory that sits under an explicit parent directory must work
// even when Open/Stat runs before any ReadDir has occurred.
// xTestArchiveFSImplicitDirMixedExplicit reproduces
// https://github.com/cloudfra/ufs/pull/250 for an implicit directory nested
// under an explicit parent directory. Disabled (x-prefixed) until a fix lands.
//
//nolint:unused // disabled reproduction test, see comment above
func xTestArchiveFSImplicitDirMixedExplicit(t *testing.T) {
	// "onetwothree/" is an explicit directory entry; "onetwothree/sixseven"
	// has no directory entry of its own, so it is implicit.
	fsys := mustArchiveFromEntries(t,
		"onetwothree/",
		"onetwothree/1.txt",
		"onetwothree/2.txt",
		"onetwothree/sixseven/6.txt",
		"onetwothree/sixseven/7.txt",
	)

	// The implicit directory under an explicit parent triggers the lazy-index
	// path when Stat runs before any ReadDir.
	info, err := fsys.Stat("onetwothree/sixseven")
	if err != nil {
		t.Fatalf("Stat(\"onetwothree/sixseven\") = %v, want nil", err)
	}
	if !info.IsDir() {
		t.Error("Stat(\"onetwothree/sixseven\").IsDir() = false, want true")
	}

	f, err := fsys.Open("onetwothree/sixseven")
	if err != nil {
		t.Fatalf("Open(\"onetwothree/sixseven\") = %v, want nil", err)
	}
	defer validateClose(t, f)()
	openInfo, err := f.Stat()
	if err != nil {
		t.Fatalf("Open(\"onetwothree/sixseven\").Stat() = %v, want nil", err)
	}
	if !openInfo.IsDir() {
		t.Error("Open(\"onetwothree/sixseven\") should be a directory, IsDir() = false")
	}

	// Directory listing at the mixed level: the implicit "sixseven" appears
	// alongside the explicit files under the explicit parent directory.
	assertDir(t, fsys, "onetwothree", []string{"1.txt", "2.txt", "sixseven"})
	assertDir(t, fsys, "onetwothree/sixseven", []string{"6.txt", "7.txt"})
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
