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
	"slices"
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

// --- Integration test — end-to-end mount and read-back verification ---

func TestProjFSMountReadBack(t *testing.T) {
	t.Parallel()
	requireProjFS(t)

	type testFile struct {
		path    string
		content []byte
	}

	// Build a set of files with varied sizes and nesting depths.
	files := []testFile{
		{"hello.txt", []byte("hello world")},
		{"empty.txt", []byte{}},
		{"binary.bin", func() []byte {
			b := make([]byte, 256)
			for i := range b {
				b[i] = byte(i)
			}
			return b
		}()},
		{"subdir/nested.txt", []byte("nested content")},
		{"subdir/deep/deeper/leaf.txt", []byte("deep leaf")},
		{"large.dat", bytes.Repeat([]byte("ABCDEFGHIJKLMNOP"), 16384)}, // 256KB
	}

	// Populate a memFS with the test files.
	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	for _, f := range files {
		w, err := fsys.Create(f.path)
		if err != nil {
			t.Fatalf("Create(%q): %v", f.path, err)
		}
		if _, err := w.Write(f.content); err != nil {
			_ = w.Close()
			t.Fatalf("Write(%q): %v", f.path, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close(%q): %v", f.path, err)
		}
	}

	// Mount the memFS via ProjFS.
	mountDir := testProjFSMount(t, fsys)

	// Read every file back through the OS mount and verify content.
	for _, f := range files {
		osPath := filepath.Join(mountDir, filepath.FromSlash(f.path))
		got, err := os.ReadFile(osPath)
		if err != nil {
			t.Errorf("ReadFile(%q): %v", f.path, err)
			continue
		}
		if !bytes.Equal(got, f.content) {
			t.Errorf("content mismatch for %q: got %d bytes, want %d bytes", f.path, len(got), len(f.content))
			if len(got) < 200 && len(f.content) < 200 {
				t.Errorf("  got:  %q", got)
				t.Errorf("  want: %q", f.content)
			}
		}
	}

	// Verify Stat on each file reports correct size and type.
	for _, f := range files {
		osPath := filepath.Join(mountDir, filepath.FromSlash(f.path))
		fi, err := os.Stat(osPath)
		if err != nil {
			t.Errorf("Stat(%q): %v", f.path, err)
			continue
		}
		if fi.IsDir() {
			t.Errorf("Stat(%q).IsDir() = true, want false", f.path)
		}
		if fi.Size() != int64(len(f.content)) {
			t.Errorf("Stat(%q).Size() = %d, want %d", f.path, fi.Size(), len(f.content))
		}
	}

	// Verify directory listings at the root.
	rootEntries, err := os.ReadDir(mountDir)
	if err != nil {
		t.Fatalf("ReadDir(root): %v", err)
	}
	wantRootNames := []string{"binary.bin", "empty.txt", "hello.txt", "large.dat", "subdir"}
	gotRootNames := make([]string, len(rootEntries))
	for i, e := range rootEntries {
		gotRootNames[i] = e.Name()
	}
	sort.Strings(gotRootNames)
	if !slices.Equal(gotRootNames, wantRootNames) {
		t.Errorf("root entries = %v, want %v", gotRootNames, wantRootNames)
	}

	// Verify subdirectory listing.
	subEntries, err := os.ReadDir(filepath.Join(mountDir, "subdir"))
	if err != nil {
		t.Fatalf("ReadDir(subdir): %v", err)
	}
	wantSubNames := []string{"deep", "nested.txt"}
	gotSubNames := make([]string, len(subEntries))
	for i, e := range subEntries {
		gotSubNames[i] = e.Name()
	}
	sort.Strings(gotSubNames)
	if !slices.Equal(gotSubNames, wantSubNames) {
		t.Errorf("subdir entries = %v, want %v", gotSubNames, wantSubNames)
	}

	// Verify non-existent file returns appropriate error.
	_, err = os.Stat(filepath.Join(mountDir, "does-not-exist.txt"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(nonexistent) error = %v, want ErrNotExist", err)
	}
}
