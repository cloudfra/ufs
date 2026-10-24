// Copyright 2026 Cloudfra
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

package core

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

const (
	testLocalFSName = "testing/testassets"
)

func TestLocalFSString(t *testing.T) {
	fsys, err := makeLocalFS("../../" + testLocalFSName)
	if err != nil {
		t.Fatal(err)
	}
	if got := fsys.String(); !strings.Contains(got, testLocalFSName) {
		t.Errorf("String() should contain %q, got: %q", testLocalFSName, got)
	}
}

func TestIsLocalFSUri(t *testing.T) {
	testCases := []struct {
		name string
		want bool
	}{
		{name: "file:", want: true},
		{name: "file://", want: true},
		{name: "filefs://", want: false},
		{name: pathutil.CwdPath, want: true},
		{name: "/root/user", want: true},
		{name: "/tmp", want: true},
		{name: "memory://", want: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isLocalFSUri(tc.name)
			if got != tc.want {
				t.Errorf("got: %t, want: %t", got, tc.want)
			}
		})
	}
}

func TestLocalFSLstat(t *testing.T) {
	dir := t.TempDir()
	fsys, err := newLocalFS(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	f, err := fsys.Create("lstat_file.txt")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := osutil.Symlink("lstat_file.txt", filepath.Join(dir, "lstat_link.txt")); err != nil {
		t.Skipf("skipping: symlink creation requires elevated privileges or Developer Mode on Windows: %v", err)
	}

	lfs, ok := fsys.(fs.ReadLinkFS)
	if !ok {
		t.Fatal("localFS does not implement fs.ReadLinkFS")
	}

	t.Run("regular_file", func(t *testing.T) {
		info, err := lfs.Lstat("lstat_file.txt")
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
		info, err := lfs.Lstat("lstat_link.txt")
		if err != nil {
			t.Fatalf("Lstat on symlink failed: %v", err)
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("Mode() missing ModeSymlink for symlink: %v", info.Mode())
		}
	})
}

func TestLocalFSReadLink(t *testing.T) {
	dir := t.TempDir()
	fsys, err := newLocalFS(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	f, err := fsys.Create("target.txt")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := osutil.Symlink("target.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("skipping: symlink creation requires elevated privileges or Developer Mode on Windows: %v", err)
	}

	lfs, ok := fsys.(fs.ReadLinkFS)
	if !ok {
		t.Fatal("localFS does not implement fs.ReadLinkFS")
	}

	got, err := lfs.ReadLink("link.txt")
	if err != nil {
		t.Fatalf("ReadLink failed: %v", err)
	}
	if got != "target.txt" {
		t.Errorf("ReadLink = %q, want %q", got, "target.txt")
	}
}

func TestLocalFSRemove(t *testing.T) {
	dir := t.TempDir()
	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	// Create a file and a subdirectory with a child.
	if err := osutil.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.MkdirAll(filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.WriteFile(filepath.Join(dir, "sub", "child.txt"), []byte("child")); err != nil {
		t.Fatal(err)
	}

	t.Run("file_exists", func(t *testing.T) {
		if err := fsys.Remove("hello.txt"); err != nil {
			t.Fatalf("Remove('hello.txt') = %v, want nil", err)
		}
		if _, err := fsys.Stat("hello.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("after Remove, Stat = %v, want ErrNotExist", err)
		}
	})

	t.Run("non_empty_dir_fails", func(t *testing.T) {
		if err := fsys.Remove("sub"); err == nil {
			t.Error("Remove(non-empty dir) succeeded, want error")
		}
	})

	t.Run("not_exist", func(t *testing.T) {
		if err := fsys.Remove("ghost.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Remove(nonexistent) = %v, want ErrNotExist", err)
		}
	})
}

func TestLocalFSRemoveAll(t *testing.T) {
	dir := t.TempDir()
	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	if err := osutil.MkdirAll(filepath.Join(dir, "tree", "deep")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.WriteFile(filepath.Join(dir, "tree", "deep", "leaf.txt"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep")); err != nil {
		t.Fatal(err)
	}

	t.Run("subtree", func(t *testing.T) {
		if err := fsys.RemoveAll("tree"); err != nil {
			t.Fatalf("RemoveAll('tree') = %v, want nil", err)
		}
		if _, err := fsys.Stat("tree"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("after RemoveAll('tree'), Stat = %v, want ErrNotExist", err)
		}
		if _, err := fsys.Stat("keep.txt"); err != nil {
			t.Errorf("RemoveAll('tree') unexpectedly removed keep.txt: %v", err)
		}
	})

	t.Run("not_exist_is_noop", func(t *testing.T) {
		if err := fsys.RemoveAll("ghost"); err != nil {
			t.Errorf("RemoveAll(nonexistent) = %v, want nil", err)
		}
	})
}

func TestLocalFSReadDirDoesNotContainCwd(t *testing.T) {
	fsys, err := osutil.OpenRoot(pathutil.CwdPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()
	f, err := fsys.Open(pathutil.CwdPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, f)()
	entries, err := f.ReadDir(-1)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == pathutil.CwdPath {
			t.Errorf("entry list contains '.', %v", entries)
		}
	}
}

func TestLocalFSOpenFile(t *testing.T) {
	dir := t.TempDir()
	fsys, err := newLocalFS(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	ofs, ok := fsys.(types.OpenFileFS)
	if !ok {
		t.Fatal("localFS does not implement types.OpenFileFS")
	}

	if err := osutil.WriteFile(filepath.Join(dir, "existing.txt"), []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.MkdirAll(filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}

	writeAndClose := func(t *testing.T, f types.File, data string) {
		t.Helper()
		if _, err := f.WriteString(data); err != nil {
			t.Fatalf("WriteString failed: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
	}
	readFile := func(t *testing.T, name string) string {
		t.Helper()
		got, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Fatalf("ReadFile(%q) failed: %v", name, err)
		}
		return string(got)
	}

	t.Run("read_only", func(t *testing.T) {
		f, err := ofs.OpenFile("existing.txt", os.O_RDONLY, 0)
		if err != nil {
			t.Fatalf("OpenFile failed: %v", err)
		}
		defer ufsTesting.ValidateClose(t, f)()
		got, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("ReadAll failed: %v", err)
		}
		if string(got) != "hello" {
			t.Errorf("read %q, want %q", got, "hello")
		}
		if _, err := f.WriteString("x"); err == nil {
			t.Error("WriteString on O_RDONLY file succeeded, want error")
		}
	})

	t.Run("create", func(t *testing.T) {
		f, err := ofs.OpenFile("created.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			t.Fatalf("OpenFile failed: %v", err)
		}
		writeAndClose(t, f, "new")
		if got := readFile(t, "created.txt"); got != "new" {
			t.Errorf("content = %q, want %q", got, "new")
		}
	})

	t.Run("create_in_subdir", func(t *testing.T) {
		f, err := ofs.OpenFile("sub/child.txt", os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			t.Fatalf("OpenFile failed: %v", err)
		}
		writeAndClose(t, f, "child")
		if got := readFile(t, "sub/child.txt"); got != "child" {
			t.Errorf("content = %q, want %q", got, "child")
		}
	})

	t.Run("excl_existing", func(t *testing.T) {
		_, err := ofs.OpenFile("existing.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if !errors.Is(err, fs.ErrExist) {
			t.Errorf("OpenFile(O_EXCL, existing) = %v, want ErrExist", err)
		}
	})

	t.Run("not_exist", func(t *testing.T) {
		_, err := ofs.OpenFile("ghost.txt", os.O_RDONLY, 0)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("OpenFile(nonexistent) = %v, want ErrNotExist", err)
		}
	})

	t.Run("missing_parent", func(t *testing.T) {
		_, err := ofs.OpenFile("nodir/file.txt", os.O_WRONLY|os.O_CREATE, 0o644)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("OpenFile(missing parent) = %v, want ErrNotExist", err)
		}
	})

	t.Run("truncate", func(t *testing.T) {
		if err := osutil.WriteFile(filepath.Join(dir, "trunc.txt"), []byte("long content")); err != nil {
			t.Fatal(err)
		}
		f, err := ofs.OpenFile("trunc.txt", os.O_WRONLY|os.O_TRUNC, 0)
		if err != nil {
			t.Fatalf("OpenFile failed: %v", err)
		}
		writeAndClose(t, f, "short")
		if got := readFile(t, "trunc.txt"); got != "short" {
			t.Errorf("content = %q, want %q", got, "short")
		}
	})

	t.Run("append", func(t *testing.T) {
		if err := osutil.WriteFile(filepath.Join(dir, "append.txt"), []byte("first")); err != nil {
			t.Fatal(err)
		}
		f, err := ofs.OpenFile("append.txt", os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatalf("OpenFile failed: %v", err)
		}
		writeAndClose(t, f, "second")
		if got := readFile(t, "append.txt"); got != "firstsecond" {
			t.Errorf("content = %q, want %q", got, "firstsecond")
		}
	})

	t.Run("read_write_seek", func(t *testing.T) {
		if err := osutil.WriteFile(filepath.Join(dir, "rw.txt"), []byte("abcdef")); err != nil {
			t.Fatal(err)
		}
		f, err := ofs.OpenFile("rw.txt", os.O_RDWR, 0)
		if err != nil {
			t.Fatalf("OpenFile failed: %v", err)
		}
		if _, err := f.Seek(2, io.SeekStart); err != nil {
			t.Fatalf("Seek failed: %v", err)
		}
		writeAndClose(t, f, "XY")
		if got := readFile(t, "rw.txt"); got != "abXYef" {
			t.Errorf("content = %q, want %q", got, "abXYef")
		}
	})

	t.Run("directory", func(t *testing.T) {
		f, err := ofs.OpenFile("sub", os.O_RDONLY, 0)
		if err != nil {
			t.Fatalf("OpenFile(dir) failed: %v", err)
		}
		defer ufsTesting.ValidateClose(t, f)()
		info, err := f.Stat()
		if err != nil {
			t.Fatalf("Stat failed: %v", err)
		}
		if !info.IsDir() {
			t.Error("IsDir() = false, want true")
		}
		if info.Size() != 0 && runtime.GOOS == "windows" {
			t.Errorf("Size() = %d, want 0 for a directory on Windows", info.Size())
		}
	})

	t.Run("invalid_paths", func(t *testing.T) {
		names := []string{"", "/abs.txt", "../escape.txt", "sub/../existing.txt", "sub/"}
		if runtime.GOOS == "windows" {
			names = append(names, `sub\child.txt`)
		}
		for _, name := range names {
			_, err := ofs.OpenFile(name, os.O_RDONLY, 0)
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("OpenFile(%q) = %v, want ErrInvalid", name, err)
			}
			var pathErr *fs.PathError
			if errors.As(err, &pathErr) && pathErr.Op != "open" {
				t.Errorf("OpenFile(%q) error Op = %q, want %q", name, pathErr.Op, "open")
			}
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("file created outside the root: %v", err)
		}
	})

	t.Run("symlink_escape", func(t *testing.T) {
		outside := t.TempDir()
		if err := osutil.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
			t.Skipf("skipping: symlink creation requires elevated privileges or Developer Mode on Windows: %v", err)
		}
		if _, err := ofs.OpenFile("escape/file.txt", os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			t.Error("OpenFile through a symlink escaping the root succeeded, want error")
		}
		if _, err := os.Stat(filepath.Join(outside, "file.txt")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("file created outside the root: %v", err)
		}
	})
}
