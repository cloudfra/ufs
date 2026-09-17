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

package osutil

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCreateOSTempDirectory(t *testing.T) {
	dir, cleanup, err := NewTempDirectory()
	if err != nil {
		t.Error(err)
	}
	if !Exists(dir) {
		t.Errorf("'%s' does not exist when it should", dir)
	}

	if !strings.Contains(dir, "goapp") {
		t.Errorf("'%s' does not contain 'goapp'", dir)
	}
	if err := cleanup(); err != nil {
		t.Errorf("failed to cleanup temp directory: %v", err)
	}
	if Exists(dir) {
		t.Errorf("'%s' exists when it should not", dir)
	}
}

func TestOSDeleteFile(t *testing.T) {
	t.Run("nonexistent", func(t *testing.T) {
		err := DeleteFile("/nonexistent/path/that/cannot/exist-" + t.Name() + ".txt")
		if err != nil {
			t.Errorf("DeleteFile(nonexistent) = %v, want nil", err)
		}
	})

	t.Run("existing", func(t *testing.T) {
		f, err := CreateTemp("", "ufs-osutil-test-*.txt")
		if err != nil {
			t.Fatal(err)
		}
		p := f.Name()
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		if err := DeleteFile(p); err != nil {
			t.Errorf("DeleteFile(existing) = %v, want nil", err)
		}
		if Exists(p) {
			t.Errorf("%q still exists after DeleteFile", p)
		}
	})
}

func TestTryOSDeleteFile(t *testing.T) {
	f, err := CreateTemp("", "ufs-try-delete-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	p := f.Name()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	TryDeleteFile(p)
	if Exists(p) {
		t.Errorf("%q still exists after TryDeleteFile", p)
	}
}

func TestOSMkdir(t *testing.T) {
	parent, err := MkdirTemp("", "ufs-mkdir-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := RemoveAll(parent); err != nil {
			t.Errorf("cleanup remove %q: %v", parent, err)
		}
	})

	dir := filepath.Join(parent, "newdir")
	if err := Mkdir(dir); err != nil {
		t.Fatalf("Mkdir(existing parent) = %v, want nil", err)
	}
	if _, err := Stat(dir); err != nil {
		t.Errorf("Stat after Mkdir: %v, want the new dir to exist", err)
	}

	if err := Mkdir(dir); err == nil {
		t.Error("Mkdir(existing) = nil, want an error")
	} else if !os.IsExist(err) {
		t.Errorf("Mkdir(existing) = %v, want fs.ErrExist", err)
	}

	if err := Mkdir(filepath.Join(parent, "a", "b")); err == nil {
		t.Error("Mkdir(missing parent) = nil, want an error")
	}
}

func TestOSDeleteDirectoryExists(t *testing.T) {
	dir, err := MkdirTemp("", "ufs-del-dir-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteDirectory(dir); err != nil {
		t.Errorf("DeleteDirectory(existing) = %v, want nil", err)
	}
	if Exists(dir) {
		t.Errorf("%q still exists after DeleteDirectory", dir)
	}
}

func TestTryOSDeleteDirectory(t *testing.T) {
	dir, err := MkdirTemp("", "ufs-try-del-dir-*")
	if err != nil {
		t.Fatal(err)
	}
	TryDeleteDirectory(dir)
	if Exists(dir) {
		t.Errorf("%q still exists after TryDeleteDirectory", dir)
	}
}

// makeFileAndParents writes data at path, creating any missing parent
// directories first, and fails the test on any error.
func makeFileAndParents(t *testing.T, path string, data string) {
	t.Helper()
	if err := MkdirAll(filepath.Dir(path)); err != nil {
		t.Fatalf("MkdirAll(dir for %q) = %v", path, err)
	}
	if err := WriteFile(path, []byte(data)); err != nil {
		t.Fatalf("WriteFile(%q) = %v", path, err)
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()

	existing := filepath.Join(dir, "file.txt")
	if err := WriteFile(existing, []byte("hi")); err != nil {
		t.Fatal(err)
	}

	if !Exists(existing) {
		t.Errorf("Exists(%q) = false, want true", existing)
	}
	if !Exists(dir) {
		t.Errorf("Exists(%q) = false, want true", dir)
	}
	if got := Exists(filepath.Join(dir, "does-not-exist-"+t.Name())); got {
		t.Errorf("Exists(nonexistent) = true, want false")
	}
}

func TestMkdirAll(t *testing.T) {
	parent := t.TempDir()

	nested := filepath.Join(parent, "a", "b", "c")
	if err := MkdirAll(nested); err != nil {
		t.Fatalf("MkdirAll(%q) = %v, want nil", nested, err)
	}
	fi, err := Stat(nested)
	if err != nil {
		t.Fatalf("Stat after MkdirAll = %v, want the dir to exist", err)
	}
	if !fi.IsDir() {
		t.Errorf("%q is not a directory", nested)
	}

	// Each intermediate level must have been created as well.
	for _, mid := range []string{
		filepath.Join(parent, "a"),
		filepath.Join(parent, "a", "b"),
	} {
		fi, err := Stat(mid)
		if err != nil {
			t.Errorf("Stat(%q) = %v, want it to exist", mid, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("%q is not a directory", mid)
		}
	}

	// Creating a path underneath a regular file must fail.
	file := filepath.Join(parent, "plain.txt")
	if err := WriteFile(file, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAll(filepath.Join(file, "child")); err == nil {
		t.Errorf("MkdirAll(below a file) = nil, want an error")
	}
}

func TestCreate(t *testing.T) {
	dir := t.TempDir()

	file := filepath.Join(dir, "created.txt")
	f, err := Create(file)
	if err != nil {
		t.Fatalf("Create(%q) = %v, want nil", file, err)
	}
	if !Exists(file) {
		t.Errorf("Create did not create %q", file)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// Create must fail when a path component is a regular file (not a directory).
	plainFile := filepath.Join(dir, "plain.txt")
	if err := WriteFile(plainFile, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(filepath.Join(plainFile, "child")); err == nil {
		t.Errorf("Create(below a file) = nil, want an error")
	}
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()

	file := filepath.Join(dir, "data.txt")
	want := []byte("hello world")
	if err := WriteFile(file, want); err != nil {
		t.Fatal(err)
	}

	got, err := ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile(%q) = %v, want nil", file, err)
	}
	if string(got) != string(want) {
		t.Errorf("ReadFile = %q, want %q", got, want)
	}

	if _, err := ReadFile(filepath.Join(dir, "no-such-file-"+t.Name())); err == nil {
		t.Errorf("ReadFile(nonexistent) = nil, want an error")
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()

	file := filepath.Join(dir, "wf.txt")
	data := []byte("some data")
	if err := WriteFile(file, data); err != nil {
		t.Fatalf("WriteFile(%q) = %v, want nil", file, err)
	}

	got, err := ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile after WriteFile = %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("ReadFile = %q, want %q", got, data)
	}

	// WriteFile applies the project's secure file permissions. Windows reports
	// 0o666 for regular files regardless of the mode passed to os.WriteFile, so
	// the permission bits can only be verified on platforms that track them.
	if runtime.GOOS != "windows" {
		fi, err := Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != FilePermissions {
			t.Errorf("WriteFile applied %v, want %v", got, FilePermissions)
		}
	}

	// WriteFile truncates pre-existing content.
	if err := WriteFile(file, []byte("x")); err != nil {
		t.Fatal(err)
	}
	got, err = ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "x" {
		t.Errorf("ReadFile after rewrite = %q, want %q", got, "x")
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()

	file := filepath.Join(dir, "remove-me.txt")
	if err := WriteFile(file, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := Remove(file); err != nil {
		t.Errorf("Remove(existing file) = %v, want nil", err)
	}
	if Exists(file) {
		t.Errorf("%q still exists after Remove", file)
	}

	// Unlike DeleteFile, Remove propagates the underlying error and does not
	// tolerate a missing path.
	if err := Remove(filepath.Join(dir, "no-such-file-"+t.Name())); err == nil {
		t.Errorf("Remove(nonexistent) = nil, want an error")
	}
}

func TestRemoveAll(t *testing.T) {
	parent := t.TempDir()

	tree := filepath.Join(parent, "tree")
	makeFileAndParents(t, filepath.Join(tree, "sub", "file.txt"), "data")
	empty := filepath.Join(tree, "empty")
	if err := Mkdir(empty); err != nil {
		t.Fatal(err)
	}

	if err := RemoveAll(tree); err != nil {
		t.Errorf("RemoveAll(existing tree) = %v, want nil", err)
	}
	if Exists(tree) {
		t.Errorf("%q still exists after RemoveAll", tree)
	}

	// A path that does not exist is tolerated (mirroring os.RemoveAll).
	if err := RemoveAll(filepath.Join(parent, "no-such-dir-"+t.Name())); err != nil {
		t.Errorf("RemoveAll(nonexistent) = %v, want nil", err)
	}
	// Removing a path below a regular file errors on POSIX (ENOTDIR); Windows
	// tolerates it, so only assert the error on platforms that report one.
	if runtime.GOOS != "windows" {
		plainFile := filepath.Join(parent, "plain.txt")
		if err := WriteFile(plainFile, []byte("x")); err != nil {
			t.Fatal(err)
		}
		if err := RemoveAll(filepath.Join(plainFile, "child")); err == nil {
			t.Errorf("RemoveAll(below a file) = nil, want an error")
		}
	}
}

func TestReadDir(t *testing.T) {
	dir := t.TempDir()

	names := []string{"a.txt", "b.txt"}
	for _, n := range names {
		if err := WriteFile(filepath.Join(dir, n), []byte(n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := Mkdir(filepath.Join(dir, "subdir")); err != nil {
		t.Fatal(err)
	}

	entries, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q) = %v, want nil", dir, err)
	}

	got := make(map[string]bool, len(entries))
	for _, e := range entries {
		got[e.Name()] = true
	}
	for _, n := range append(names, "subdir") {
		if !got[n] {
			t.Errorf("ReadDir entries missing %q", n)
		}
	}
	if len(entries) != len(names)+1 {
		t.Errorf("ReadDir returned %d entries, want %d", len(entries), len(names)+1)
	}

	if _, err := ReadDir(filepath.Join(dir, "no-such-dir-"+t.Name())); err == nil {
		t.Errorf("ReadDir(nonexistent) = nil, want an error")
	}
}

func TestDirFS(t *testing.T) {
	dir := t.TempDir()

	makeFileAndParents(t, filepath.Join(dir, "sub", "hello.txt"), "hello")
	if err := WriteFile(filepath.Join(dir, "root.txt"), []byte("top")); err != nil {
		t.Fatal(err)
	}

	fsys := DirFS(dir)

	// Read both a top-level file and one in a subdirectory via the fs.FS
	// interface.
	for name, want := range map[string]string{
		"root.txt":      "top",
		"sub/hello.txt": "hello",
	} {
		got, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Errorf("fs.ReadFile(%q) = %v, want nil", name, err)
			continue
		}
		if string(got) != want {
			t.Errorf("fs.ReadFile(%q) = %q, want %q", name, got, want)
		}
	}

	// A path that escapes the root (..) is invalid for a fs.FS.
	if _, err := fs.ReadFile(fsys, "../root.txt"); err == nil {
		t.Errorf("fs.ReadFile(\"../root.txt\") = nil, want the traversal to be rejected")
	}
}

func TestSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links on Windows requires the Create-Symlink privilege or Developer Mode")
	}
	dir := t.TempDir()

	target := filepath.Join(dir, "target.txt")
	if err := WriteFile(target, []byte("linked")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link-to-target")
	if err := Symlink(target, link); err != nil {
		t.Fatalf("Symlink(%q -> %q) = %v, want nil", target, link, err)
	}

	// The newname path must be a symbolic link. osutil.Stat follows the link
	// (matching os.Stat), so we need os.Lstat to inspect the link itself.
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("os.Lstat(%q) = %v, want the link to exist", link, err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("os.Lstat(%q).Mode() = %v, want the symlink bit set", link, fi.Mode())
	}

	// Reading through the link must resolve to the target's contents.
	got, err := ReadFile(link)
	if err != nil {
		t.Fatalf("ReadFile through symlink = %v", err)
	}
	if string(got) != "linked" {
		t.Errorf("ReadFile through symlink = %q, want %q", got, "linked")
	}
}

func TestOpenRoot(t *testing.T) {
	dir := t.TempDir()

	makeFileAndParents(t, filepath.Join(dir, "sub", "file.txt"), "inside")
	if err := WriteFile(filepath.Join(dir, "root.txt"), []byte("top")); err != nil {
		t.Fatal(err)
	}

	root, err := OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot(%q) = %v, want nil", dir, err)
	}
	// Release the handle to dir before the test ends so t.TempDir() can remove
	// it (Windows refuses to delete a directory that still has an open handle).
	defer func() {
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Open a relative path through the root reference and read it back.
	f, err := root.Open("sub/file.txt")
	if err != nil {
		t.Fatalf("root.Open(%q) = %v, want nil", "sub/file.txt", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("io.ReadAll = %v", err)
	}
	if string(got) != "inside" {
		t.Errorf("read via root = %q, want %q", got, "inside")
	}

	// Traversal out of the root must be rejected.
	if _, err := root.Open("../outside.txt"); err == nil {
		t.Errorf("root.Open(\"../outside.txt\") succeeded, want the traversal to be rejected")
	}
}

func TestOpen(t *testing.T) {
	dir := t.TempDir()

	file := filepath.Join(dir, "open.txt")
	if err := WriteFile(file, []byte("open me")); err != nil {
		t.Fatal(err)
	}

	f, err := Open(file)
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil", file, err)
	}
	if _, err := io.ReadAll(f); err != nil {
		t.Fatalf("io.ReadAll = %v", err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}

	if _, err := Open(filepath.Join(dir, "no-such-file-"+t.Name())); err == nil {
		t.Errorf("Open(nonexistent) = nil, want an error")
	}
}

func TestCreateTemp(t *testing.T) {
	dir := t.TempDir()

	f, err := CreateTemp(dir, "osutil-createtime-*")
	if err != nil {
		t.Fatalf("CreateTemp(%q, %q) = %v, want nil", dir, "osutil-*", err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// It must be inside the requested directory (absolute dir is honoured).
	if d := filepath.Dir(name); d != filepath.Clean(dir) {
		t.Errorf("CreateTemp created %q, want it inside %q", name, dir)
	}
	// The name must use the pattern's fixed prefix.
	if prefix := strings.TrimSuffix("osutil-*", "*"); !strings.HasPrefix(filepath.Base(name), prefix) {
		t.Errorf("CreateTemp name %q does not have prefix %q", filepath.Base(name), prefix)
	}
}

func TestMkdirTemp(t *testing.T) {
	dir := t.TempDir()

	got, err := MkdirTemp(dir, "osutil-mkrtmp-*")
	if err != nil {
		t.Fatalf("MkdirTemp(%q, %q) = %v, want nil", dir, "osutil-*", err)
	}
	if p := filepath.Dir(got); p != filepath.Clean(dir) {
		t.Errorf("MkdirTemp created %q, want it inside %q", got, dir)
	}
	if prefix := strings.TrimSuffix("osutil-mkrtmp-*", "*"); !strings.HasPrefix(filepath.Base(got), prefix) {
		t.Errorf("MkdirTemp name %q does not have prefix %q", filepath.Base(got), prefix)
	}
	if !Exists(got) {
		t.Errorf("%q does not exist after MkdirTemp", got)
	}
}
