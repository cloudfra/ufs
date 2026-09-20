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
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// uncleanPath returns dir/sub/../name, which resolves to dir/name only if the
// wrapper cleans its argument (or the OS resolves ".." itself).
func uncleanPath(dir, name string) string {
	return dir + string(filepath.Separator) + "sub" + string(filepath.Separator) + ".." + string(filepath.Separator) + name
}

func mustWrite(tb testing.TB, name string, data []byte) {
	tb.Helper()
	if err := os.WriteFile(name, data, DefaultFilePermissions); err != nil {
		tb.Fatal(err)
	}
}

func TestMkdir(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()

	dir := filepath.Join(parent, "newdir")
	if err := Mkdir(dir); err != nil {
		t.Fatalf("Mkdir(existing parent) = %v, want nil", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat after Mkdir: %v, want the new dir to exist", err)
	}
	if !fi.IsDir() {
		t.Errorf("%q is not a directory", dir)
	}

	if err := Mkdir(dir); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Mkdir(existing) = %v, want fs.ErrExist", err)
	}
	if err := Mkdir(filepath.Join(parent, "a", "b")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Mkdir(missing parent) = %v, want fs.ErrNotExist", err)
	}
}

func TestMkdirPermissions(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not report unix permission bits")
	}
	dir := filepath.Join(t.TempDir(), "perm")
	if err := Mkdir(dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The process umask can only clear bits, never set them.
	if got := fi.Mode().Perm(); got&^DefaultDirectoryPermissions != 0 {
		t.Errorf("Mkdir mode = %#o, want a subset of %#o", got, DefaultDirectoryPermissions)
	}
}

func TestMkdirAll(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()

	deep := filepath.Join(parent, "a", "b", "c")
	if err := MkdirAll(deep); err != nil {
		t.Fatalf("MkdirAll(nested) = %v, want nil", err)
	}
	if fi, err := os.Stat(deep); err != nil || !fi.IsDir() {
		t.Errorf("Stat(%q) = %v, %v, want a directory", deep, fi, err)
	}
	if err := MkdirAll(deep); err != nil {
		t.Errorf("MkdirAll(existing) = %v, want nil", err)
	}

	file := filepath.Join(parent, "file")
	mustWrite(t, file, []byte("x"))
	if err := MkdirAll(filepath.Join(file, "child")); err == nil {
		t.Error("MkdirAll(under a file) = nil, want an error")
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	file := filepath.Join(dir, "file")
	mustWrite(t, file, []byte("x"))
	if err := Remove(file); err != nil {
		t.Fatalf("Remove(file) = %v, want nil", err)
	}
	if _, err := os.Stat(file); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat after Remove = %v, want fs.ErrNotExist", err)
	}
	if err := Remove(file); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Remove(missing) = %v, want fs.ErrNotExist", err)
	}

	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, DefaultDirectoryPermissions); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(sub, "child"), []byte("x"))
	if err := Remove(sub); err == nil {
		t.Error("Remove(non-empty dir) = nil, want an error")
	}
}

func TestRemoveAll(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(tree, "a", "b"), DefaultDirectoryPermissions); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(tree, "a", "b", "file"), []byte("x"))

	if err := RemoveAll(tree); err != nil {
		t.Fatalf("RemoveAll(tree) = %v, want nil", err)
	}
	if _, err := os.Stat(tree); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat after RemoveAll = %v, want fs.ErrNotExist", err)
	}
	if err := RemoveAll(tree); err != nil {
		t.Errorf("RemoveAll(missing) = %v, want nil", err)
	}
}

func TestCreate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	name := filepath.Join(dir, "created")
	mustWrite(t, name, []byte("old contents"))
	f, err := Create(name)
	if err != nil {
		t.Fatalf("Create = %v, want nil", err)
	}
	if _, err := f.WriteString("new"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Clean(name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("contents after Create = %q, want the file to be truncated to %q", got, "new")
	}

	if _, err := Create(filepath.Join(dir, "missing", "file")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Create(missing parent) = %v, want fs.ErrNotExist", err)
	}
}

func TestReadFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := []byte("hello world")
	mustWrite(t, filepath.Join(dir, "file"), want)

	got, err := ReadFile(filepath.Join(dir, "file"))
	if err != nil {
		t.Fatalf("ReadFile = %v, want nil", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("ReadFile = %q, want %q", got, want)
	}

	if got, err := ReadFile(uncleanPath(dir, "file")); err != nil || !bytes.Equal(got, want) {
		t.Errorf("ReadFile(unclean path) = %q, %v, want %q, nil", got, err, want)
	}
	if _, err := ReadFile(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile(missing) = %v, want fs.ErrNotExist", err)
	}
}

func TestReadDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "b.txt"), []byte("b"))
	mustWrite(t, filepath.Join(dir, "a.txt"), []byte("a"))
	if err := os.Mkdir(filepath.Join(dir, "sub"), DefaultDirectoryPermissions); err != nil {
		t.Fatal(err)
	}

	entries, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir = %v, want nil", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// os.ReadDir returns entries sorted by filename.
	want := []string{"a.txt", "b.txt", "sub"}
	if len(names) != len(want) {
		t.Fatalf("ReadDir names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("ReadDir names = %v, want %v", names, want)
		}
	}

	if _, err := ReadDir(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadDir(missing) = %v, want fs.ErrNotExist", err)
	}
}

func TestStat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "file"), []byte("12345"))

	fi, err := Stat(filepath.Join(dir, "file"))
	if err != nil {
		t.Fatalf("Stat(file) = %v, want nil", err)
	}
	if fi.IsDir() || fi.Size() != 5 {
		t.Errorf("Stat(file) = dir:%v size:%d, want a 5 byte file", fi.IsDir(), fi.Size())
	}
	if fi, err := Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("Stat(dir) = %v, %v, want a directory", fi, err)
	}
	if _, err := Stat(uncleanPath(dir, "file")); err != nil {
		t.Errorf("Stat(unclean path) = %v, want nil", err)
	}
	if _, err := Stat(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(missing) = %v, want fs.ErrNotExist", err)
	}
}

func TestWriteFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := filepath.Join(dir, "file")

	if err := WriteFile(name, []byte("first contents")); err != nil {
		t.Fatalf("WriteFile(new) = %v, want nil", err)
	}
	if err := WriteFile(name, []byte("second")); err != nil {
		t.Fatalf("WriteFile(existing) = %v, want nil", err)
	}
	got, err := os.ReadFile(filepath.Clean(name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Errorf("contents = %q, want %q", got, "second")
	}

	if err := WriteFile(uncleanPath(dir, "cleaned"), []byte("x")); err != nil {
		t.Errorf("WriteFile(unclean path) = %v, want nil", err)
	}
	if err := WriteFile(filepath.Join(dir, "missing", "file"), nil); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("WriteFile(missing parent) = %v, want fs.ErrNotExist", err)
	}
}

func TestDirFS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "file"), []byte("contents"))

	fsys := DirFS(dir)
	got, err := fs.ReadFile(fsys, "file")
	if err != nil {
		t.Fatalf("ReadFile via DirFS = %v, want nil", err)
	}
	if string(got) != "contents" {
		t.Errorf("contents = %q, want %q", got, "contents")
	}
	if _, err := fs.ReadFile(fsys, "missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile(missing) via DirFS = %v, want fs.ErrNotExist", err)
	}

	if _, err := fs.Stat(DirFS(uncleanPath(dir, "")), "file"); err != nil {
		t.Errorf("DirFS(unclean path) Stat = %v, want nil", err)
	}
}

func TestOpenRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "file"), []byte("contents"))

	root, err := OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("Close root: %v", err)
		}
	})
	if _, err := root.Stat("file"); err != nil {
		t.Errorf("root.Stat(file) = %v, want nil", err)
	}
	if _, err := root.Open("../outside"); err == nil {
		t.Error("root.Open(escaping path) = nil, want an error")
	}

	if _, err := OpenRoot(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OpenRoot(missing) = %v, want fs.ErrNotExist", err)
	}
}

func TestSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	mustWrite(t, target, []byte("contents"))

	link := filepath.Join(dir, "link")
	if err := Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("cannot create symlinks on this Windows configuration: %v", err)
		}
		t.Fatalf("Symlink = %v, want nil", err)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Errorf("Readlink = %q, want %q", got, target)
	}
	if data, err := os.ReadFile(filepath.Clean(link)); err != nil || string(data) != "contents" {
		t.Errorf("ReadFile via link = %q, %v, want %q, nil", data, err, "contents")
	}

	if err := Symlink(target, link); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Symlink(existing) = %v, want fs.ErrExist", err)
	}
}

func TestOpen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "file"), []byte("contents"))

	f, err := Open(uncleanPath(dir, "file"))
	if err != nil {
		t.Fatalf("Open = %v, want nil", err)
	}
	buf := make([]byte, 16)
	n, err := f.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "contents" {
		t.Errorf("Read = %q, want %q", buf[:n], "contents")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open(missing) = %v, want fs.ErrNotExist", err)
	}
}

func TestCreateTemp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	f, err := CreateTemp(dir, "osutil-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp(dir) = %v, want nil", err)
	}
	if got := filepath.Dir(f.Name()); got != dir {
		t.Errorf("CreateTemp dir = %q, want %q", got, dir)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	def, err := CreateTemp("", "osutil-default-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp(default dir) = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(def.Name()); err != nil {
			t.Errorf("remove %q: %v", def.Name(), err)
		}
	})
	if err := def.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := CreateTemp(filepath.Join(dir, "missing"), "x-*"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("CreateTemp(missing dir) = %v, want fs.ErrNotExist", err)
	}
}

func TestMkdirTemp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	got, err := MkdirTemp(dir, "osutil-*")
	if err != nil {
		t.Fatalf("MkdirTemp(dir) = %v, want nil", err)
	}
	if parent := filepath.Dir(got); parent != dir {
		t.Errorf("MkdirTemp parent = %q, want %q", parent, dir)
	}
	if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
		t.Errorf("Stat(%q) = %v, %v, want a directory", got, fi, err)
	}

	def, err := MkdirTemp("", "osutil-default-*")
	if err != nil {
		t.Fatalf("MkdirTemp(default dir) = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(def); err != nil {
			t.Errorf("remove %q: %v", def, err)
		}
	})

	if _, err := MkdirTemp(filepath.Join(dir, "missing"), "x-*"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("MkdirTemp(missing dir) = %v, want fs.ErrNotExist", err)
	}
}
