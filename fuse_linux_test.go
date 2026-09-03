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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
)

func requireFUSE(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/dev/fuse"); err != nil {
		fuseSkipOrFatal(t, fmt.Sprintf("/dev/fuse not available: %v", err))
	}
}

// fuseReadOnlyFS wraps a ReadFS so the result does not satisfy the FS
// interface, forcing the FUSE adapter to treat the mount as read-only.
type fuseReadOnlyFS struct {
	ReadFS
}

func testHostMount(t *testing.T, fsys ReadFS) string {
	t.Helper()
	requireFUSE(t)
	mountDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	server, err := HostMount(ctx, fsys, mountDir)
	if err != nil {
		cancel()
		fuseSkipOrFatal(t, fmt.Sprintf("HostMount: %v", err))
		return ""
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("unmount cleanup: %v", err)
		}
		cancel()
	})
	return mountDir
}

// --- Unit tests (no FUSE mount required) ---

func TestFuseNodeChildPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		parent string
		child  string
		want   string
	}{
		{".", "file.txt", "file.txt"},
		{"", "file.txt", "file.txt"},
		{"dir", "file.txt", "dir/file.txt"},
		{"a/b", "c", "a/b/c"},
		{"top", "sub", "top/sub"},
	}
	for _, tc := range tests {
		n := &fuseNode{path: tc.parent}
		got := n.childPath(tc.child)
		if got != tc.want {
			t.Errorf("fuseNode{path:%q}.childPath(%q) = %q, want %q", tc.parent, tc.child, got, tc.want)
		}
	}
}

func TestFuseErrno(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want syscall.Errno
	}{
		{nil, 0},
		{fs.ErrNotExist, syscall.ENOENT},
		{fs.ErrPermission, syscall.EACCES},
		{fs.ErrExist, syscall.EEXIST},
		{fs.ErrInvalid, syscall.EINVAL},
		{fs.ErrClosed, syscall.EBADF},
		{syscall.ENOMEM, syscall.ENOMEM},
		{syscall.ENOSPC, syscall.ENOSPC},
		{errors.New("unknown"), syscall.EIO},
		{fmt.Errorf("wrap: %w", fs.ErrNotExist), syscall.ENOENT},
		{fmt.Errorf("wrap: %w", syscall.EPERM), syscall.EACCES},
	}
	for _, tc := range tests {
		got := fuseErrno(tc.err)
		if got != tc.want {
			t.Errorf("fuseErrno(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestFuseAttrFromFileInfoRegularFile(t *testing.T) {
	t.Parallel()
	modTime := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	fi := &fsInfo{
		name:    "hello.txt",
		size:    1234,
		mode:    0o644,
		modTime: modTime,
		isDir:   false,
	}
	var attr fuse.Attr
	fuseAttrFromFileInfo(fi, &attr)

	if attr.Size != 1234 {
		t.Errorf("Size = %d, want 1234", attr.Size)
	}
	wantMode := fuseMode(fi.Mode())
	if attr.Mode != wantMode {
		t.Errorf("Mode = %#o, want %#o", attr.Mode, wantMode)
	}
	if attr.Nlink != 1 {
		t.Errorf("Nlink = %d, want 1", attr.Nlink)
	}
	wantBlocks := (uint64(1234) + 511) / 512
	if attr.Blocks != wantBlocks {
		t.Errorf("Blocks = %d, want %d", attr.Blocks, wantBlocks)
	}
	if got := attr.ModTime(); !got.Equal(modTime) {
		t.Errorf("ModTime = %v, want %v", got, modTime)
	}
}

func TestFuseAttrFromFileInfoDirectory(t *testing.T) {
	t.Parallel()
	modTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fi := &fsInfo{
		name:    "subdir",
		size:    0,
		mode:    fs.ModeDir | 0o755,
		modTime: modTime,
		isDir:   true,
	}
	var attr fuse.Attr
	fuseAttrFromFileInfo(fi, &attr)

	wantMode := fuseMode(fi.Mode())
	if attr.Mode != wantMode {
		t.Errorf("Mode = %#o, want %#o", attr.Mode, wantMode)
	}
	if attr.Nlink != 2 {
		t.Errorf("Nlink = %d, want 2", attr.Nlink)
	}
}

// --- Integration tests (require FUSE mount) ---

func TestHostMountCreateWriteRead(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testHostMount(t, fsys)

	content := []byte("hello from fuse")
	if err := os.WriteFile(filepath.Join(mountDir, "new.txt"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(mountDir, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(content) {
		t.Errorf("ReadFile = %q, want %q", data, content)
	}
}

func TestHostMountOpenWriteOnlyNoTrunc(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	filePath := filepath.Join(srcDir, "existing.txt")
	if err := os.WriteFile(filePath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	fsys, err := New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testHostMount(t, fsys)

	mountedFile := filepath.Join(mountDir, "existing.txt")

	// O_WRONLY without O_TRUNC must be rejected (ufs.Create always truncates).
	fd, err := syscall.Open(mountedFile, syscall.O_WRONLY, 0)
	if err == nil {
		_ = syscall.Close(fd)
		t.Fatal("open O_WRONLY without O_TRUNC succeeded, want error")
	}

	// O_RDWR without O_TRUNC must also be rejected.
	fd, err = syscall.Open(mountedFile, syscall.O_RDWR, 0)
	if err == nil {
		_ = syscall.Close(fd)
		t.Fatal("open O_RDWR without O_TRUNC succeeded, want error")
	}

	// Verify the file content is unchanged.
	data, err := os.ReadFile(mountedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Errorf("file content = %q, want %q", data, "original")
	}
}

func TestHostMountWriteAtOffset(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testHostMount(t, fsys)

	filePath := filepath.Join(mountDir, "offset.txt")
	content := []byte("hello world")
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-read via the mount to verify the full write path.
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("ReadFile = %q, want %q", data, "hello world")
	}
}

func TestHostMountMkdir(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testHostMount(t, fsys)

	dirPath := filepath.Join(mountDir, "newdir")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Error("created path is not a directory")
	}
}

func TestHostMountMkdirExisting(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testHostMount(t, fsys)

	dirPath := filepath.Join(mountDir, "existdir")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	err = os.Mkdir(dirPath, 0o755)
	if err == nil {
		t.Fatal("mkdir on existing directory succeeded, want EEXIST")
	}
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("mkdir existing: got %v, want ErrExist", err)
	}
}

func TestHostMountRemoveFile(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testHostMount(t, fsys)

	filePath := filepath.Join(mountDir, "doomed.txt")
	if err := os.WriteFile(filePath, []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filePath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filePath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat after remove: got %v, want ErrNotExist", err)
	}
}

func TestHostMountRemoveDir(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	mountDir := testHostMount(t, fsys)

	dirPath := filepath.Join(mountDir, "tmpdir")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dirPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat after remove: got %v, want ErrNotExist", err)
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

	roFS := &fuseReadOnlyFS{fsys}
	mountDir := testHostMount(t, roFS)

	err = os.WriteFile(filepath.Join(mountDir, "nope.txt"), []byte("data"), 0o644)
	if err == nil {
		t.Fatal("write to read-only mount succeeded, want error")
	}
}

func TestHostMountClose(t *testing.T) {
	t.Parallel()
	requireFUSE(t)

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
		fuseSkipOrFatal(t, fmt.Sprintf("HostMount: %v", err))
		return
	}

	if _, err := os.Stat(filepath.Join(mountDir, "f.txt")); err != nil {
		t.Fatalf("Stat before close: %v", err)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := os.ReadDir(mountDir)
	if err != nil {
		return
	}
	if len(entries) != 0 {
		t.Errorf("mount dir has %d entries after unmount, want 0", len(entries))
	}
}

func TestHostMountContextCancel(t *testing.T) {
	t.Parallel()
	requireFUSE(t)

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
		fuseSkipOrFatal(t, fmt.Sprintf("HostMount: %v", err))
		return
	}
	defer func() { _ = server.Close() }()

	if _, err := os.Stat(filepath.Join(mountDir, "f.txt")); err != nil {
		t.Fatalf("Stat before cancel: %v", err)
	}

	cancel()
	time.Sleep(200 * time.Millisecond)

	entries, err := os.ReadDir(mountDir)
	if err != nil {
		return
	}
	if len(entries) != 0 {
		t.Errorf("mount dir has %d entries after context cancel, want 0", len(entries))
	}
}

func TestHostMountReadOnlyMkdir(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })

	roFS := &fuseReadOnlyFS{fsys}
	mountDir := testHostMount(t, roFS)

	err = os.Mkdir(filepath.Join(mountDir, "nope"), 0o755)
	if err == nil {
		t.Fatal("mkdir on read-only mount succeeded, want EROFS")
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

	roFS := &fuseReadOnlyFS{fsys}
	mountDir := testHostMount(t, roFS)

	err = os.Remove(filepath.Join(mountDir, "keep.txt"))
	if err == nil {
		t.Fatal("remove on read-only mount succeeded, want EROFS")
	}

	// File should still be accessible.
	data, err := os.ReadFile(filepath.Join(mountDir, "keep.txt"))
	if err != nil {
		t.Fatalf("ReadFile after failed remove: %v", err)
	}
	if string(data) != "x" {
		t.Errorf("content = %q, want %q", data, "x")
	}
}
