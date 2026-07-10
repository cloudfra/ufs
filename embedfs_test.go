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
	"embed"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

//go:embed testing/testassets/files
var embedTestFiles embed.FS

func makeTestEmbedFS(t *testing.T, name string) FS {
	t.Helper()
	fsys := NewEmbedFS(name, embedTestFiles)
	t.Cleanup(func() { fsys.Close() })
	return fsys
}

func TestNewEmbedFSURI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		wantURI string
	}{
		{"", "embed:///?ro=true"},
		{"assets", "embed:///assets?ro=true"},
		{"data/files", "embed:///data/files?ro=true"},
	} {
		fsys := NewEmbedFS(tc.name, embedTestFiles)
		if got := fsys.URI().String(); got != tc.wantURI {
			t.Errorf("NewEmbedFS(%q).URI() = %q, want %q", tc.name, got, tc.wantURI)
		}
		if got := fsys.String(); !strings.Contains(got, "embedFS(") {
			t.Errorf("NewEmbedFS(%q).String() = %q, want embedFS(...) wrapper", tc.name, got)
		}
		fsys.Close()
	}
}

func TestEmbedFSOpen(t *testing.T) {
	t.Parallel()
	fsys := makeTestEmbedFS(t, "")

	f, err := fsys.Open("testing/testassets/files/index.html")
	if err != nil {
		t.Fatalf("Open() = %v, want nil", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Fatalf("failed to close file: %v", err)
		}
	}()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll() = %v, want nil", err)
	}
	if len(data) == 0 {
		t.Error("Open() returned empty file, want non-empty")
	}
}

func TestEmbedFSOpenDir(t *testing.T) {
	t.Parallel()
	fsys := makeTestEmbedFS(t, "")

	f, err := fsys.Open("testing/testassets/files")
	if err != nil {
		t.Fatalf("Open(dir) = %v, want nil", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Fatalf("failed to close file: %v", err)
		}
	}()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}
	if !info.IsDir() {
		t.Error("Open(dir).Stat().IsDir() = false, want true")
	}
}

func TestEmbedFSClose(t *testing.T) {
	t.Parallel()
	fsys := NewEmbedFS("", embedTestFiles)
	if err := fsys.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestEmbedFSReadFile(t *testing.T) {
	t.Parallel()
	fsys := makeTestEmbedFS(t, "")

	data, err := fsys.ReadFile("testing/testassets/files/index.html")
	if err != nil {
		t.Fatalf("ReadFile() = %v, want nil", err)
	}
	if len(data) == 0 {
		t.Error("ReadFile() returned empty data, want non-empty")
	}
}

func TestEmbedFSReadDir(t *testing.T) {
	t.Parallel()
	fsys := makeTestEmbedFS(t, "")

	entries, err := fsys.ReadDir("testing/testassets/files")
	if err != nil {
		t.Fatalf("ReadDir() = %v, want nil", err)
	}
	if len(entries) == 0 {
		t.Error("ReadDir() returned empty entries, want non-empty")
	}
}

func TestEmbedFSStat(t *testing.T) {
	t.Parallel()
	fsys := makeTestEmbedFS(t, "")

	info, err := fsys.Stat("testing/testassets/files/index.html")
	if err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}
	if info.IsDir() {
		t.Error("Stat(file).IsDir() = true, want false")
	}
	if info.Size() == 0 {
		t.Error("Stat(file).Size() = 0, want non-zero")
	}
}

func TestEmbedFSLstat(t *testing.T) {
	t.Parallel()
	fsys := makeTestEmbedFS(t, "")

	info, err := fsys.Lstat("testing/testassets/files/index.html")
	if err != nil {
		t.Fatalf("Lstat() = %v, want nil", err)
	}
	if info.IsDir() {
		t.Error("Lstat(file).IsDir() = true, want false")
	}
}

func TestEmbedFSReadLink(t *testing.T) {
	t.Parallel()
	fsys := makeTestEmbedFS(t, "")

	_, err := fsys.ReadLink("testing/testassets/files/index.html")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("ReadLink() = %v, want fs.ErrInvalid", err)
	}
}

func TestEmbedFSCreate(t *testing.T) {
	t.Parallel()
	fsys := makeTestEmbedFS(t, "")

	_, err := fsys.Create("newfile.txt")
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Create() = %v, want fs.ErrPermission", err)
	}
}

func TestEmbedFSMkdirAll(t *testing.T) {
	t.Parallel()
	fsys := makeTestEmbedFS(t, "")

	err := fsys.MkdirAll("newdir", fs.ModePerm)
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("MkdirAll() = %v, want fs.ErrPermission", err)
	}
}

func TestEmbedFSRemove(t *testing.T) {
	t.Parallel()
	fsys := makeTestEmbedFS(t, "")

	err := fsys.Remove("testing/testassets/files/index.html")
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Remove() = %v, want fs.ErrPermission", err)
	}
}

func TestEmbedFSRemoveAll(t *testing.T) {
	t.Parallel()
	fsys := makeTestEmbedFS(t, "")

	err := fsys.RemoveAll("testing/testassets/files")
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("RemoveAll() = %v, want fs.ErrPermission", err)
	}
}

func TestEmbedFSTestFS(t *testing.T) {
	t.Parallel()
	fsys := makeTestEmbedFS(t, "")

	if err := fstest.TestFS(
		fsys,
		"testing/testassets/files/index.html",
		"testing/testassets/files/site.js",
	); err != nil {
		t.Errorf("fstest.TestFS: %v", err)
	}
}

func TestEmbedFSInvalidPaths(t *testing.T) {
	t.Parallel()
	invalidPaths := []string{
		"/absolute/path",
		"../relative/path",
		"invalid/../path",
	}
	tests := []struct {
		name string
		op   func(fsys FS, path string) error
	}{
		{"Open", func(fsys FS, path string) error { _, err := fsys.Open(path); return err }},
		{"Stat", func(fsys FS, path string) error { _, err := fsys.Stat(path); return err }},
		{"Lstat", func(fsys FS, path string) error { _, err := fsys.Lstat(path); return err }},
		{"ReadFile", func(fsys FS, path string) error { _, err := fsys.ReadFile(path); return err }},
		{"ReadDir", func(fsys FS, path string) error { _, err := fsys.ReadDir(path); return err }},
		{"ReadLink", func(fsys FS, path string) error { _, err := fsys.ReadLink(path); return err }},
		{"Create", func(fsys FS, path string) error { _, err := fsys.Create(path); return err }},
		{"MkdirAll", func(fsys FS, path string) error { return fsys.MkdirAll(path, fs.ModePerm) }},
		{"Remove", func(fsys FS, path string) error { return fsys.Remove(path) }},
		{"RemoveAll", func(fsys FS, path string) error { return fsys.RemoveAll(path) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := makeTestEmbedFS(t, "")
			for _, p := range invalidPaths {
				t.Run(p, func(t *testing.T) {
					if err := tc.op(fsys, p); err == nil {
						t.Errorf("%s(%q) succeeded, want error", tc.name, p)
					}
				})
			}
		})
	}
}
