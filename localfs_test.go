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
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testLocalFSName = "testing/testassets"
)

func TestLocalFSString(t *testing.T) {
	fsys, err := makeLocalFS(testLocalFSName)
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
		{name: cwdPath, want: true},
		{name: "/root/user", want: true},
		{name: "/tmp", want: true},
		{name: "mem://", want: false},
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

func TestLocalFS(t *testing.T) {
	dir := mustTemp(t)
	testFileSystem(t, newFSFuncWithoutContext(newLocalFS), dir)
}

func TestLocalFSLstat(t *testing.T) {
	dir := mustTemp(t)
	fsys := mustFS(t, newFSFuncWithoutContext(newLocalFS), dir)
	defer validateClose(t, fsys)()

	f, err := fsys.Create("lstat_file.txt")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := os.Symlink("lstat_file.txt", filepath.Join(dir, "lstat_link.txt")); err != nil {
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
	dir := mustTemp(t)
	fsys := mustFS(t, newFSFuncWithoutContext(newLocalFS), dir)
	defer validateClose(t, fsys)()

	f, err := fsys.Create("target.txt")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := os.Symlink("target.txt", filepath.Join(dir, "link.txt")); err != nil {
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
	dir := mustTemp(t)
	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	// Create a file and a subdirectory with a child.
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "child.txt"), []byte("child"), 0o644); err != nil {
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
	dir := mustTemp(t)
	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	if err := os.MkdirAll(filepath.Join(dir, "tree", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tree", "deep", "leaf.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o644); err != nil {
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
	fsys, err := os.OpenRoot(cwdPath)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()
	f, err := fsys.Open(cwdPath)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, f)()
	entries, err := f.ReadDir(-1)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == cwdPath {
			t.Errorf("entry list contains '.', %v", entries)
		}
	}
}
