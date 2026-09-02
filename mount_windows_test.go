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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"golang.org/x/sys/windows"
)

func requireProjFS(t *testing.T) {
	t.Helper()
	if err := projfsAvailable(); err != nil {
		t.Skipf("ProjFS not available: %v", err)
	}
}

// projfsReadOnlyFS wraps a ReadFS so the result does not satisfy the FS
// interface, forcing the mount to be treated as read-only.
type projfsReadOnlyFS struct {
	ReadFS
}

func testProjFSMount(t *testing.T, fsys ReadFS) string {
	t.Helper()
	requireProjFS(t)
	mountDir := t.TempDir()
	server, err := HostMount(t.Context(), fsys, mountDir)
	if err != nil {
		t.Fatalf("HostMount: %v", err)
		return ""
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("unmount cleanup: %v", err)
		}
	})
	return mountDir
}

// --- Unit tests (no ProjFS required) ---

func TestProjfsPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input *uint16
		want  string
	}{
		{nil, "."},
	}
	for _, tc := range tests {
		got := projfsPath(tc.input)
		if got != tc.want {
			t.Errorf("projfsPath(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}

	// Test with actual string pointers — backslash conversion.
	for _, tc := range []struct {
		name string
		want string
	}{
		{`dir1\dir2\file.txt`, "dir1/dir2/file.txt"},
		{`file.txt`, "file.txt"},
		{`a\b`, "a/b"},
	} {
		ptr, _ := windows.UTF16PtrFromString(tc.name)
		got := projfsPath(ptr)
		if got != tc.want {
			t.Errorf("projfsPath(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestProjfsHRESULT(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want uintptr
	}{
		{nil, 0},
		{fs.ErrNotExist, 0x80070002},
		{fs.ErrPermission, 0x80070005},
		{fs.ErrExist, 0x80070050},
		{errors.New("unknown"), 0x80004005},
		{fmt.Errorf("wrap: %w", fs.ErrNotExist), 0x80070002},
	}
	for _, tc := range tests {
		got := projfsHRESULT(tc.err)
		if got != tc.want {
			t.Errorf("projfsHRESULT(%v) = %#x, want %#x", tc.err, got, tc.want)
		}
	}
}

// --- Integration tests (require ProjFS mount) — read operations ---

func TestHostMountReadFile(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "hello.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}

	fsys, err := New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testProjFSMount(t, fsys)

	data, err := os.ReadFile(filepath.Join(mountDir, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "world" {
		t.Errorf("ReadFile = %q, want %q", data, "world")
	}
}

func TestHostMountStat(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	content := []byte("test content")
	if err := os.WriteFile(filepath.Join(srcDir, "file.txt"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	fsys, err := New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testProjFSMount(t, fsys)

	fi, err := os.Stat(filepath.Join(mountDir, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.IsDir() {
		t.Error("file.txt reported as directory")
	}
	if fi.Size() != int64(len(content)) {
		t.Errorf("Size = %d, want %d", fi.Size(), len(content))
	}

	di, err := os.Stat(filepath.Join(mountDir, "subdir"))
	if err != nil {
		t.Fatal(err)
	}
	if !di.IsDir() {
		t.Error("subdir not reported as directory")
	}
}

func TestHostMountReadDir(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	fsys, err := New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testProjFSMount(t, fsys)

	entries, err := os.ReadDir(mountDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	want := []string{"a.txt", "b.txt", "c.txt", "sub"}
	if len(names) != len(want) {
		t.Fatalf("ReadDir got %v, want %v", names, want)
	}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("entry[%d] = %q, want %q", i, n, want[i])
		}
	}
}

func TestHostMountStatNotExist(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()

	fsys, err := New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testProjFSMount(t, fsys)

	_, err = os.Stat(filepath.Join(mountDir, "nonexistent.txt"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(nonexistent) = %v, want ErrNotExist", err)
	}
}

func TestHostMountNestedRead(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	nested := filepath.Join(srcDir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "deep.txt"), []byte("deep"), 0o644); err != nil {
		t.Fatal(err)
	}

	fsys, err := New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testProjFSMount(t, fsys)

	data, err := os.ReadFile(filepath.Join(mountDir, "a", "b", "deep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "deep" {
		t.Errorf("ReadFile = %q, want %q", data, "deep")
	}
}

func TestHostMountLargeFile(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	size := 256 * 1024
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i % 251)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "large.bin"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	fsys, err := New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testProjFSMount(t, fsys)

	data, err := os.ReadFile(filepath.Join(mountDir, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != size {
		t.Fatalf("len = %d, want %d", len(data), size)
	}
	for i, b := range data {
		if b != byte(i%251) {
			t.Fatalf("byte[%d] = %d, want %d", i, b, byte(i%251))
		}
	}
}

// --- Integration tests — lifecycle ---

func TestHostMountClose(t *testing.T) {
	t.Parallel()
	requireProjFS(t)

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fsys, err := New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := t.TempDir()
	server, err := HostMount(t.Context(), fsys, mountDir)
	if err != nil {
		t.Fatalf("HostMount: %v", err)
	}

	if _, err := os.Stat(filepath.Join(mountDir, "f.txt")); err != nil {
		t.Fatalf("Stat before close: %v", err)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestHostMountContextCancel(t *testing.T) {
	t.Parallel()
	requireProjFS(t)

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fsys, err := New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	mountDir := t.TempDir()
	server, err := HostMount(ctx, fsys, mountDir)
	if err != nil {
		t.Fatalf("HostMount: %v", err)
	}
	defer func() { _ = server.Close() }()

	if _, err := os.Stat(filepath.Join(mountDir, "f.txt")); err != nil {
		t.Fatalf("Stat before cancel: %v", err)
	}

	cancel()
}

// --- Integration tests — Rsync through ProjFS ---

func TestHostMountRsyncArchive(t *testing.T) {
	t.Parallel()

	archivePath := filepath.Join("testing", "testassets", "archives", "testassets.tar.gz")
	archiveFS, err := New(t.Context(), "archive://"+archivePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = archiveFS.Close() })

	mountDir := testProjFSMount(t, archiveFS)

	memFS, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = memFS.Close() }()

	if err := Rsync(os.DirFS(mountDir), memFS, "."); err != nil {
		t.Fatalf("Rsync: %v", err)
	}

	wantFiles := loadTestAssets(t)
	for filePath, wantData := range wantFiles {
		got, err := fs.ReadFile(memFS, filePath)
		if err != nil {
			t.Errorf("ReadFile(%q): %v", filePath, err)
			continue
		}
		if !bytes.Equal(got, wantData) {
			t.Errorf("ReadFile(%q): got %d bytes, want %d bytes", filePath, len(got), len(wantData))
		}
	}
}

// --- Integration tests — read-only enforcement and special backends ---

func TestHostMountNullFS(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "null:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testProjFSMount(t, fsys)

	entries, err := os.ReadDir(mountDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir returned %d entries, want 0", len(entries))
	}
}

func TestHostMountNestedOverlay(t *testing.T) {
	t.Parallel()
	uri, err := CreateURI("memory://", map[string]string{
		"cache": "null:",
	})
	if err != nil {
		t.Fatal(err)
	}
	fsys, err := New(t.Context(), uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testProjFSMount(t, fsys)

	cacheDir := filepath.Join(mountDir, "cache")
	fi, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatalf("Stat cache: %v", err)
	}
	if !fi.IsDir() {
		t.Errorf("cache IsDir = false, want true")
	}
}

func TestHostMountReadOnly(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()

	fsys, err := New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	roFS := &projfsReadOnlyFS{fsys}
	mountDir := testProjFSMount(t, roFS)
	t.Skip("ProjFS cannot intercept new file creation or mkdir — writes materialize to local NTFS")

	err = os.WriteFile(filepath.Join(mountDir, "nope.txt"), []byte("data"), 0o644)
	if err == nil {
		t.Fatal("write to read-only mount succeeded, want error")
	}
}

func TestHostMountReadOnlyMkdir(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	roFS := &projfsReadOnlyFS{fsys}
	mountDir := testProjFSMount(t, roFS)
	t.Skip("ProjFS cannot intercept new file creation or mkdir — writes materialize to local NTFS")

	err = os.Mkdir(filepath.Join(mountDir, "nope"), 0o755)
	if err == nil {
		t.Fatal("mkdir on read-only mount succeeded, want error")
	}
}

func TestHostMountReadOnlyRemove(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fsys, err := New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testProjFSMount(t, fsys)

	err = os.Remove(filepath.Join(mountDir, "keep.txt"))
	if err == nil {
		t.Fatal("remove on mount succeeded, want access denied")
	}

	data, err := os.ReadFile(filepath.Join(mountDir, "keep.txt"))
	if err != nil {
		t.Fatalf("ReadFile after failed remove: %v", err)
	}
	if string(data) != "x" {
		t.Errorf("content = %q, want %q", data, "x")
	}
}
