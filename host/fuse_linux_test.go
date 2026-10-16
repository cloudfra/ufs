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

package host

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/cloudfra/ufs"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func requireFUSE(t *testing.T) {
	t.Helper()
	if _, err := osutil.Stat("/dev/fuse"); err != nil {
		fuseSkipOrFatal(t, fmt.Sprintf("/dev/fuse not available: %v", err))
	}
}

// fuseReadOnlyFS wraps a types.ReadFS so the result does not satisfy the FS
// interface, forcing the FUSE adapter to treat the mount as read-only.
type fuseReadOnlyFS struct {
	types.ReadFS
}

func testMount(t *testing.T, fsys types.ReadFS) string {
	t.Helper()
	requireFUSE(t)
	mountDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	server, err := Mount(ctx, fsys, mountDir)
	if err != nil {
		cancel()
		fuseSkipOrFatal(t, fmt.Sprintf("Mount: %v", err))
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
	// A node outside an inode tree is the root.
	n := &fuseNode{}
	if got := n.fsPath(); got != "." {
		t.Errorf("fsPath() = %q, want %q", got, ".")
	}
	if got := n.childPath("file.txt"); got != "file.txt" {
		t.Errorf("childPath(%q) = %q, want %q", "file.txt", got, "file.txt")
	}
}

// failRenameFS fails the renames from the path fail.
type failRenameFS struct {
	types.WriteFS
	fail string
}

var errRenameFailed = errors.New("rename failed")

func (fsys failRenameFS) Rename(oldPath, newPath string) error {
	if oldPath == fsys.fail {
		return errRenameFailed
	}
	return ufs.Rename(fsys.WriteFS, oldPath, newPath)
}

func TestRenameReplacing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		oldPath, newPath string
		failRename       string
		wantErr          error
		want             map[string]string
	}{
		{
			name: "New", oldPath: "a.txt", newPath: "new.txt",
			want: map[string]string{"new.txt": "a", "b.txt": "b", "full/f.txt": "f"},
		},
		{
			name: "ReplaceFile", oldPath: "a.txt", newPath: "b.txt",
			want: map[string]string{"b.txt": "a", "full/f.txt": "f"},
		},
		{
			name: "ReplaceEmptyDir", oldPath: "full", newPath: "empty",
			want: map[string]string{"a.txt": "a", "b.txt": "b", "empty/f.txt": "f"},
		},
		{
			name: "Self", oldPath: "a.txt", newPath: "a.txt",
			want: map[string]string{"a.txt": "a", "b.txt": "b", "full/f.txt": "f"},
		},
		{
			name: "NonEmptyDir", oldPath: "empty", newPath: "full", wantErr: syscall.ENOTEMPTY,
			want: map[string]string{"a.txt": "a", "b.txt": "b", "full/f.txt": "f"},
		},
		{
			name: "FileOverDir", oldPath: "a.txt", newPath: "empty", wantErr: syscall.EISDIR,
			want: map[string]string{"a.txt": "a", "b.txt": "b", "full/f.txt": "f"},
		},
		{
			name: "DirOverFile", oldPath: "full", newPath: "b.txt", wantErr: syscall.ENOTDIR,
			want: map[string]string{"a.txt": "a", "b.txt": "b", "full/f.txt": "f"},
		},
		{
			name: "MissingSource", oldPath: "x.txt", newPath: "b.txt", wantErr: fs.ErrNotExist,
			want: map[string]string{"a.txt": "a", "b.txt": "b", "full/f.txt": "f"},
		},
		{
			name: "FailedRenameRestoresFile", oldPath: "a.txt", newPath: "b.txt", failRename: "a.txt", wantErr: errRenameFailed,
			want: map[string]string{"a.txt": "a", "b.txt": "b", "full/f.txt": "f"},
		},
		{
			name: "FailedRenameRestoresDir", oldPath: "full", newPath: "empty", failRename: "full", wantErr: errRenameFailed,
			want: map[string]string{"a.txt": "a", "b.txt": "b", "full/f.txt": "f"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, err := ufs.New(t.Context(), "memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(validateClose(t, base))
			for _, dir := range []string{"empty", "full"} {
				if err := base.MkdirAll(dir, fs.ModePerm); err != nil {
					t.Fatal(err)
				}
			}
			for name, content := range map[string]string{"a.txt": "a", "b.txt": "b", "full/f.txt": "f"} {
				if err := writeFile(base, name, content); err != nil {
					t.Fatal(err)
				}
			}

			err = renameReplacing(t.Context(), failRenameFS{base, tc.failRename}, tc.oldPath, tc.newPath)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("renameReplacing(%q, %q) = %v, want %v", tc.oldPath, tc.newPath, err, tc.wantErr)
			}
			got := map[string]string{}
			err = fs.WalkDir(base, ".", func(name string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				data, err := fs.ReadFile(base, name)
				got[name] = string(data)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("files diff(-want,+got):\n%s", diff)
			}
		})
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
		{ufserrors.ErrDirNotEmpty, syscall.ENOTEMPTY},
		{ufserrors.NewPathError("remove", "dir", ufserrors.ErrDirNotEmpty), syscall.ENOTEMPTY},
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
	fi := ufs.NewFileInfo("hello.txt", 1234, osutil.DefaultFilePermissions, modTime)
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
	fi := ufs.NewFileInfo("subdir", 0, fs.ModeDir|osutil.DefaultDirectoryPermissions, modTime)
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

func TestMountCreateWriteRead(t *testing.T) {
	t.Parallel()
	fsys, err := ufs.New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	mountDir := testMount(t, fsys)

	content := []byte("hello from fuse")
	if err := osutil.WriteFile(filepath.Join(mountDir, "new.txt"), content); err != nil {
		t.Fatal(err)
	}

	data, err := osutil.ReadFile(filepath.Join(mountDir, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(content) {
		t.Errorf("ReadFile = %q, want %q", data, content)
	}
}

func TestMountOpenWriteOnlyNoTrunc(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	filePath := filepath.Join(srcDir, "existing.txt")
	if err := osutil.WriteFile(filePath, []byte("original")); err != nil {
		t.Fatal(err)
	}

	fsys, err := ufs.New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	mountDir := testMount(t, fsys)

	mountedFile := filepath.Join(mountDir, "existing.txt")

	// O_WRONLY without O_TRUNC must be rejected (ufs.Create always truncates).
	fd, err := syscall.Open(mountedFile, syscall.O_WRONLY, 0)
	if err == nil {
		if err := syscall.Close(fd); err != nil {
			t.Errorf("close() failed, %s", err)
		}
		t.Fatal("open O_WRONLY without O_TRUNC succeeded, want error")
	}

	// O_RDWR without O_TRUNC must also be rejected.
	fd, err = syscall.Open(mountedFile, syscall.O_RDWR, 0)
	if err == nil {
		if err := syscall.Close(fd); err != nil {
			t.Errorf("close() failed, %s", err)
		}
		t.Fatal("open O_RDWR without O_TRUNC succeeded, want error")
	}

	// Verify the file content is unchanged.
	data, err := osutil.ReadFile(mountedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Errorf("file content = %q, want %q", data, "original")
	}
}

func TestMountWriteAtOffset(t *testing.T) {
	t.Parallel()
	fsys, err := ufs.New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	mountDir := testMount(t, fsys)

	filePath := filepath.Join(mountDir, "offset.txt")
	content := []byte("hello world")
	if err := osutil.WriteFile(filePath, content); err != nil {
		t.Fatal(err)
	}

	// Re-read via the mount to verify the full write path.
	data, err := osutil.ReadFile(filepath.Clean(filePath))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("ReadFile = %q, want %q", data, "hello world")
	}
}

func TestMountMkdir(t *testing.T) {
	t.Parallel()
	fsys, err := ufs.New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	mountDir := testMount(t, fsys)

	dirPath := filepath.Join(mountDir, "newdir")
	if err := osutil.Mkdir(dirPath); err != nil {
		t.Fatal(err)
	}

	fi, err := osutil.Stat(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Error("created path is not a directory")
	}
}

func TestMountMkdirExisting(t *testing.T) {
	t.Parallel()
	fsys, err := ufs.New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	mountDir := testMount(t, fsys)

	dirPath := filepath.Join(mountDir, "existdir")
	if err := osutil.Mkdir(dirPath); err != nil {
		t.Fatal(err)
	}
	err = osutil.Mkdir(dirPath)
	if err == nil {
		t.Fatal("mkdir on existing directory succeeded, want EEXIST")
	}
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("mkdir existing: got %v, want ErrExist", err)
	}
}

func TestMountRemoveFile(t *testing.T) {
	t.Parallel()
	fsys, err := ufs.New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	mountDir := testMount(t, fsys)

	filePath := filepath.Join(mountDir, "doomed.txt")
	if err := osutil.WriteFile(filePath, []byte("bye")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.Remove(filePath); err != nil {
		t.Fatal(err)
	}
	if _, err := osutil.Stat(filePath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat after remove: got %v, want ErrNotExist", err)
	}
}

func TestMountRemoveDir(t *testing.T) {
	t.Parallel()
	fsys, err := ufs.New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	mountDir := testMount(t, fsys)

	dirPath := filepath.Join(mountDir, "tmpdir")
	if err := osutil.Mkdir(dirPath); err != nil {
		t.Fatal(err)
	}
	if err := osutil.Remove(dirPath); err != nil {
		t.Fatal(err)
	}
	if _, err := osutil.Stat(dirPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat after remove: got %v, want ErrNotExist", err)
	}
}

// TestMountRename renames through the mount: a file, a file over another, and
// a directory whose child was looked up before, which must then be found
// under its new path.
func TestMountRename(t *testing.T) {
	t.Parallel()
	fsys, err := ufs.New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))
	if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"a.txt": "a", "b.txt": "b", "dir/f.txt": "f"} {
		if err := writeFile(fsys, name, content); err != nil {
			t.Fatal(err)
		}
	}

	mountDir := testMount(t, fsys)
	if err := osutil.Rename(filepath.Join(mountDir, "a.txt"), filepath.Join(mountDir, "c.txt")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.Rename(filepath.Join(mountDir, "c.txt"), filepath.Join(mountDir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if data, err := fs.ReadFile(fsys, "b.txt"); err != nil || string(data) != "a" {
		t.Errorf("ReadFile(b.txt) = %q, %v, want %q", data, err, "a")
	}
	for _, name := range []string{"a.txt", "c.txt"} {
		if _, err := fsys.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) = %v, want %v", name, err, fs.ErrNotExist)
		}
	}

	if _, err := osutil.Stat(filepath.Join(mountDir, "dir", "f.txt")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.Rename(filepath.Join(mountDir, "dir"), filepath.Join(mountDir, "moved")); err != nil {
		t.Fatal(err)
	}
	data, err := osutil.ReadFile(filepath.Join(mountDir, "moved", "f.txt"))
	if err != nil || string(data) != "f" {
		t.Errorf("ReadFile(moved/f.txt) = %q, %v, want %q", data, err, "f")
	}
	if _, err := fsys.Stat("dir"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(dir) = %v, want %v", err, fs.ErrNotExist)
	}
}

func TestMountReadOnlyRename(t *testing.T) {
	t.Parallel()
	fsys, err := ufs.New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))
	if err := writeFile(fsys, "keep.txt", "x"); err != nil {
		t.Fatal(err)
	}

	mountDir := testMount(t, &fuseReadOnlyFS{fsys})
	err = osutil.Rename(filepath.Join(mountDir, "keep.txt"), filepath.Join(mountDir, "moved.txt"))
	if !errors.Is(err, syscall.EROFS) {
		t.Errorf("Rename on read-only mount = %v, want %v", err, syscall.EROFS)
	}
	if _, err := fsys.Stat("keep.txt"); err != nil {
		t.Errorf("Stat(keep.txt) after failed rename = %v", err)
	}
}

func TestMountReadOnly(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()

	fsys, err := ufs.New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	roFS := &fuseReadOnlyFS{fsys}
	mountDir := testMount(t, roFS)

	err = osutil.WriteFile(filepath.Join(mountDir, "nope.txt"), []byte("data"))
	if err == nil {
		t.Fatal("write to read-only mount succeeded, want error")
	}
}

func TestMountClose(t *testing.T) {
	t.Parallel()
	requireFUSE(t)

	srcDir := t.TempDir()
	if err := osutil.WriteFile(filepath.Join(srcDir, "f.txt"), []byte("x")); err != nil {
		t.Fatal(err)
	}

	fsys, err := ufs.New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	mountDir := t.TempDir()
	server, err := Mount(t.Context(), fsys, mountDir)
	if err != nil {
		fuseSkipOrFatal(t, fmt.Sprintf("Mount: %v", err))
		return
	}

	if _, err := osutil.Stat(filepath.Join(mountDir, "f.txt")); err != nil {
		t.Fatalf("Stat before close: %v", err)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := osutil.ReadDir(mountDir)
	if err != nil {
		return
	}
	if len(entries) != 0 {
		t.Errorf("mount dir has %d entries after unmount, want 0", len(entries))
	}
}

func TestMountContextCancel(t *testing.T) {
	t.Parallel()
	requireFUSE(t)

	srcDir := t.TempDir()
	if err := osutil.WriteFile(filepath.Join(srcDir, "f.txt"), []byte("x")); err != nil {
		t.Fatal(err)
	}

	fsys, err := ufs.New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	ctx, cancel := context.WithCancel(t.Context())
	mountDir := t.TempDir()
	server, err := Mount(ctx, fsys, mountDir)
	if err != nil {
		fuseSkipOrFatal(t, fmt.Sprintf("Mount: %v", err))
		return
	}
	defer validateClose(t, server)()

	if _, err := osutil.Stat(filepath.Join(mountDir, "f.txt")); err != nil {
		t.Fatalf("Stat before cancel: %v", err)
	}

	cancel()
	time.Sleep(200 * time.Millisecond)

	entries, err := osutil.ReadDir(mountDir)
	if err != nil {
		return
	}
	if len(entries) != 0 {
		t.Errorf("mount dir has %d entries after context cancel, want 0", len(entries))
	}
}

func TestMountReadOnlyMkdir(t *testing.T) {
	t.Parallel()
	fsys, err := ufs.New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	roFS := &fuseReadOnlyFS{fsys}
	mountDir := testMount(t, roFS)

	err = osutil.Mkdir(filepath.Join(mountDir, "nope"))
	if err == nil {
		t.Fatal("mkdir on read-only mount succeeded, want EROFS")
	}
}

func TestMountReadOnlyRemove(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	if err := osutil.WriteFile(filepath.Join(srcDir, "keep.txt"), []byte("x")); err != nil {
		t.Fatal(err)
	}

	fsys, err := ufs.New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	roFS := &fuseReadOnlyFS{fsys}
	mountDir := testMount(t, roFS)

	err = osutil.Remove(filepath.Join(mountDir, "keep.txt"))
	if err == nil {
		t.Fatal("remove on read-only mount succeeded, want EROFS")
	}

	// File should still be accessible.
	data, err := osutil.ReadFile(filepath.Join(mountDir, "keep.txt"))
	if err != nil {
		t.Fatalf("ReadFile after failed remove: %v", err)
	}
	if string(data) != "x" {
		t.Errorf("content = %q, want %q", data, "x")
	}
}
