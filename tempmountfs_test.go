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
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestNewTempMountFS(t *testing.T) {
	fsys, err := newTempMountFS("test://", func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if fsys == nil {
		t.Fatal("fsys is nil")
	}
	if err := fsys.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestTempMountFSFileSystem(t *testing.T) {
	testFileSystem(t, func(_ context.Context, name string) (FS, error) {
		return newTempMountFS(name, func(string) error { return nil })
	}, "temp://")
}

func TestTempMountFSCleanup(t *testing.T) {
	var capturedDir string
	fsys, err := newTempMountFS("test://", func(dir string) error {
		capturedDir = dir
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !osExists(capturedDir) {
		t.Error("temp dir should exist before Close")
	}
	if err := fsys.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if osExists(capturedDir) {
		t.Error("temp dir should be deleted after Close")
	}
}

func TestTempMountFSCloseError(t *testing.T) {
	// Use an angry FS so that lfs.Close() returns an error.
	angry := makeAngryFS(angryFSPrefix)
	tfs := makeTempMountFS(angry, "test://", "test://", func() error { return nil })
	err := tfs.Close()
	if err == nil {
		t.Fatal("Close() = nil, want error from angry lfs")
	}
}

func TestTempMountFSPrepareError(t *testing.T) {
	var capturedDir string
	wantErr := errors.New("prepare failed")
	_, err := newTempMountFS("test://", func(dir string) error {
		capturedDir = dir
		return wantErr
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if capturedDir != "" && osExists(capturedDir) {
		t.Error("temp dir should be cleaned up after prepare error")
	}
}

func TestTempMountFSRemove(t *testing.T) {
	fsys, err := newTempMountFS("test://", func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	f, err := fsys.Create("remove_me.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	t.Run("file_exists", func(t *testing.T) {
		if err := fsys.Remove("remove_me.txt"); err != nil {
			t.Fatalf("Remove() = %v, want nil", err)
		}
		if _, err := fsys.Stat("remove_me.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("after Remove, Stat = %v, want ErrNotExist", err)
		}
	})

	t.Run("not_exist", func(t *testing.T) {
		if err := fsys.Remove("ghost.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Remove(nonexistent) = %v, want ErrNotExist", err)
		}
	})
}

func TestTempMountFSRemoveAll(t *testing.T) {
	fsys, err := newTempMountFS("test://", func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	if err := fsys.MkdirAll("sub/dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	child, err := fsys.Create("sub/dir/leaf.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Close(); err != nil {
		t.Errorf("failed to close child file: %v", err)
	}
	keep, err := fsys.Create("keep.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := keep.Close(); err != nil {
		t.Errorf("failed to close keep file: %v", err)
	}

	t.Run("subtree", func(t *testing.T) {
		if err := fsys.RemoveAll("sub"); err != nil {
			t.Fatalf("RemoveAll('sub') = %v, want nil", err)
		}
		if _, err := fsys.Stat("sub"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("after RemoveAll('sub'), Stat = %v, want ErrNotExist", err)
		}
		if _, err := fsys.Stat("keep.txt"); err != nil {
			t.Errorf("RemoveAll('sub') unexpectedly removed keep.txt: %v", err)
		}
	})

	t.Run("not_exist_is_noop", func(t *testing.T) {
		if err := fsys.RemoveAll("ghost"); err != nil {
			t.Errorf("RemoveAll(nonexistent) = %v, want nil", err)
		}
	})
}

func TestTempMountFSReadLink(t *testing.T) {
	skipTestOnWindows(t)
	var tempDir string
	fsys, err := newTempMountFS("test://", func(dir string) error {
		tempDir = dir
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	f, err := fsys.Create("target.txt")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	f.Close()

	if err := os.Symlink("target.txt", filepath.Join(tempDir, "link.txt")); err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}

	rlfs, ok := fsys.(fs.ReadLinkFS)
	if !ok {
		t.Fatal("tempMountFS does not implement fs.ReadLinkFS")
	}
	got, err := rlfs.ReadLink("link.txt")
	if err != nil {
		t.Fatalf("ReadLink failed: %v", err)
	}
	if got != "target.txt" {
		t.Errorf("ReadLink = %q, want %q", got, "target.txt")
	}
}

func TestTempMountFSLstat(t *testing.T) {
	skipTestOnWindows(t)
	var tempDir string
	fsys, err := newTempMountFS("test://", func(dir string) error {
		tempDir = dir
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	f, err := fsys.Create("lstat_file.txt")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	f.Close()

	if err := os.Symlink("lstat_file.txt", filepath.Join(tempDir, "lstat_link.txt")); err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}

	rlfs, ok := fsys.(fs.ReadLinkFS)
	if !ok {
		t.Fatal("tempMountFS does not implement fs.ReadLinkFS")
	}

	t.Run("regular_file", func(t *testing.T) {
		info, err := rlfs.Lstat("lstat_file.txt")
		if err != nil {
			t.Fatalf("Lstat failed: %v", err)
		}
		if info.IsDir() {
			t.Error("IsDir() = true, want false")
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			t.Errorf("Mode() has ModeSymlink set on a regular file: %v", info.Mode())
		}
	})

	t.Run("symlink", func(t *testing.T) {
		info, err := rlfs.Lstat("lstat_link.txt")
		if err != nil {
			t.Fatalf("Lstat on symlink failed: %v", err)
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("Mode() missing ModeSymlink for symlink: %v", info.Mode())
		}
	})
}
