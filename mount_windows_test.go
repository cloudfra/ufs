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
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// --- Integration tests — read-only enforcement and special backends ---

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
