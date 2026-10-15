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
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"testing"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

// noRenameFS hides the Rename method of the file system it wraps, so that
// [Rename] has to copy and remove.
type noRenameFS struct {
	WriteFS
}

// failRemoveFS is a noRenameFS whose Remove and RemoveAll fail for the path
// fail.
type failRemoveFS struct {
	noRenameFS
	fail string
}

var errRemove = errors.New("remove failed")

func (fsys failRemoveFS) Remove(name string) error {
	if name == fsys.fail {
		return errRemove
	}
	return fsys.noRenameFS.Remove(name)
}

func (fsys failRemoveFS) RemoveAll(name string) error {
	if name == fsys.fail {
		return errRemove
	}
	return fsys.noRenameFS.RemoveAll(name)
}

func writeTestFile(tb testing.TB, fsys WriteFS, name string, data []byte) {
	tb.Helper()
	f, err := fsys.Create(name)
	if err != nil {
		tb.Fatalf("Create(%q) = %v", name, err)
	}
	if _, err := f.Write(data); err != nil {
		tb.Fatalf("Write(%q) = %v", name, err)
	}
	ufsTesting.Must(tb, f.Close())
}

func assertFileContent(tb testing.TB, fsys fs.FS, name, want string) {
	tb.Helper()
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		tb.Errorf("ReadFile(%q) = %v, want nil", name, err)
		return
	}
	if string(data) != want {
		tb.Errorf("ReadFile(%q) = %q, want %q", name, data, want)
	}
}

func assertNotExist(tb testing.TB, fsys fs.FS, name string) {
	tb.Helper()
	if _, err := fs.Stat(fsys, name); !errors.Is(err, fs.ErrNotExist) {
		tb.Errorf("Stat(%q) = %v, want %v", name, err, fs.ErrNotExist)
	}
}

// makeTestZip returns a zip archive holding in.txt with content.
func makeTestZip(tb testing.TB, content string) []byte {
	tb.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("in.txt")
	ufsTesting.Must(tb, err)
	_, err = w.Write([]byte(content))
	ufsTesting.Must(tb, err)
	ufsTesting.Must(tb, zw.Close())
	return buf.Bytes()
}

func TestRenameByCopy(t *testing.T) {
	t.Parallel()
	base := mustBaseFS(t, "memory://")
	t.Cleanup(ufsTesting.ValidateClose(t, base))
	fsys := noRenameFS{base}
	ufsTesting.Must(t, fsys.MkdirAll("a/sub", fs.ModePerm))
	writeTestFile(t, fsys, "a/f.txt", []byte("f"))
	writeTestFile(t, fsys, "a/sub/g.txt", []byte("g"))
	writeTestFile(t, fsys, "c.txt", []byte("c"))

	if err := Rename(fsys, "c.txt", "d.txt"); err != nil {
		t.Fatalf("Rename(c.txt, d.txt) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "d.txt", "c")
	assertNotExist(t, fsys, "c.txt")

	if err := Rename(fsys, "a", "b"); err != nil {
		t.Fatalf("Rename(a, b) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "b/f.txt", "f")
	assertFileContent(t, fsys, "b/sub/g.txt", "g")
	assertNotExist(t, fsys, "a")

	if err := Rename(fsys, "b", "b/sub/b"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Rename(b, b/sub/b) = %v, want %v", err, fs.ErrInvalid)
	}
	assertFileContent(t, fsys, "b/sub/g.txt", "g")
}

// TestRenameByCopyRemoveFails checks that a failed Remove of a file leaves
// only the original, and that of a directory keeps the copy.
func TestRenameByCopyRemoveFails(t *testing.T) {
	t.Parallel()
	base := mustBaseFS(t, "memory://")
	t.Cleanup(ufsTesting.ValidateClose(t, base))
	ufsTesting.Must(t, base.MkdirAll("a", fs.ModePerm))
	writeTestFile(t, base, "a/f.txt", []byte("f"))
	writeTestFile(t, base, "c.txt", []byte("c"))
	err := Rename(failRemoveFS{noRenameFS{base}, "c.txt"}, "c.txt", "d.txt")
	var linkErr *os.LinkError
	if !errors.Is(err, errRemove) || !errors.As(err, &linkErr) {
		t.Fatalf("Rename(c.txt, d.txt) = %v, want an *os.LinkError for %v", err, errRemove)
	}
	assertFileContent(t, base, "c.txt", "c")
	assertNotExist(t, base, "d.txt")

	if err := Rename(failRemoveFS{noRenameFS{base}, "a"}, "a", "b"); !errors.Is(err, errRemove) {
		t.Fatalf("Rename(a, b) = %v, want %v", err, errRemove)
	}
	assertFileContent(t, base, "a/f.txt", "f")
	assertFileContent(t, base, "b/f.txt", "f")
}

func TestNestFSRenameAcrossMounts(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory://?one=memory://&two=memory://")
	ufsTesting.Must(t, err)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	ufsTesting.Must(t, fsys.MkdirAll("one/dir/sub", fs.ModePerm))
	writeTestFile(t, fsys, "one/dir/f.txt", []byte("f"))
	writeTestFile(t, fsys, "one/dir/sub/g.txt", []byte("g"))
	writeTestFile(t, fsys, "base.txt", []byte("base"))

	if err := Rename(fsys, "one/dir", "two/moved"); err != nil {
		t.Fatalf("Rename(one/dir, two/moved) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "two/moved/f.txt", "f")
	assertFileContent(t, fsys, "two/moved/sub/g.txt", "g")
	assertNotExist(t, fsys, "one/dir")

	if err := Rename(fsys, "base.txt", "one/base.txt"); err != nil {
		t.Fatalf("Rename(base.txt, one/base.txt) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "one/base.txt", "base")
	assertNotExist(t, fsys, "base.txt")

	if err := Rename(fsys, "one/base.txt", "two/moved/f.txt"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Rename(one/base.txt, two/moved/f.txt) = %v, want %v", err, fs.ErrExist)
	}
	assertFileContent(t, fsys, "one/base.txt", "base")
}

func TestNestFSRenameMountPoints(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory://?mnt/one=memory://")
	ufsTesting.Must(t, err)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	writeTestFile(t, fsys, "x.txt", []byte("x"))

	tests := []struct {
		oldPath, newPath string
		wantErr          error
	}{
		{oldPath: "mnt/one", newPath: "y", wantErr: fs.ErrPermission},
		{oldPath: "mnt", newPath: "y", wantErr: fs.ErrPermission},
		{oldPath: "x.txt", newPath: "mnt/one", wantErr: fs.ErrPermission},
		{oldPath: "x.txt", newPath: "mnt", wantErr: fs.ErrExist},
	}
	for _, tc := range tests {
		if err := Rename(fsys, tc.oldPath, tc.newPath); !errors.Is(err, tc.wantErr) {
			t.Errorf("Rename(%q, %q) = %v, want %v", tc.oldPath, tc.newPath, err, tc.wantErr)
		}
	}
	assertFileContent(t, fsys, "x.txt", "x")
}

// TestNestFSRenameArchive checks that renaming a mounted archive, or a
// directory holding one, unmounts it so that it is read from its new path.
func TestNestFSRenameArchive(t *testing.T) {
	t.Parallel()
	archive := makeTestZip(t, "in")
	other := makeTestZip(t, "other")

	fsys := mustNestFS(t, "memory://")
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	ufsTesting.Must(t, fsys.MkdirAll("dir", fs.ModePerm))
	writeTestFile(t, fsys, "dir/a.zip", archive)
	assertFileContent(t, fsys, "dir/a.zip.d/in.txt", "in")

	if err := Rename(fsys, "dir/a.zip", "dir/b.zip"); err != nil {
		t.Fatalf("Rename(dir/a.zip, dir/b.zip) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "dir/b.zip.d/in.txt", "in")
	assertNotExist(t, fsys, "dir/a.zip.d/in.txt")

	if err := Rename(fsys, "dir", "dir2"); err != nil {
		t.Fatalf("Rename(dir, dir2) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "dir2/b.zip.d/in.txt", "in")
	assertNotExist(t, fsys, "dir/b.zip.d/in.txt")

	// A mount left from an archive that was removed is not used for an
	// archive renamed to its path.
	writeTestFile(t, fsys, "old.zip", archive)
	assertFileContent(t, fsys, "old.zip.d/in.txt", "in")
	ufsTesting.Must(t, fsys.Remove("old.zip"))
	writeTestFile(t, fsys, "new.zip", other)
	if err := Rename(fsys, "new.zip", "old.zip"); err != nil {
		t.Fatalf("Rename(new.zip, old.zip) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "old.zip.d/in.txt", "other")

	if err := Rename(fsys, "dir2/b.zip.d", "c"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Rename(dir2/b.zip.d, c) = %v, want %v", err, fs.ErrPermission)
	}
	if err := Rename(fsys, "dir2/b.zip.d/in.txt", "in.txt"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Rename(dir2/b.zip.d/in.txt, in.txt) = %v, want %v", err, fs.ErrPermission)
	}
	assertNotExist(t, fsys, "in.txt")
}

// TestNestFSRenameNestedMounts checks renames through a nestFS mounted as a
// file system, which has mounts of its own: its mount points cannot be moved
// or replaced, and a copy to another mount holds the files of the moved
// directory but not the archive content shown below it.
func TestNestFSRenameNestedMounts(t *testing.T) {
	t.Parallel()
	inner, err := New(t.Context(), "memory://?n=memory://&d/deep=memory://")
	ufsTesting.Must(t, err)
	other := mustBaseFS(t, "memory://")
	fsys, err := NewFSBuilder("memory://").MountFS("m", inner).MountFS("other", other).Build(t.Context())
	ufsTesting.Must(t, err)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))

	ufsTesting.Must(t, fsys.MkdirAll("m/dir/sub", fs.ModePerm))
	writeTestFile(t, fsys, "m/dir/f.txt", []byte("f"))
	writeTestFile(t, fsys, "m/dir/sub/g.txt", []byte("g"))
	writeTestFile(t, fsys, "m/dir/a.zip", makeTestZip(t, "in"))
	writeTestFile(t, fsys, "m/x.txt", []byte("x"))
	assertFileContent(t, fsys, "m/dir/a.zip.d/in.txt", "in")

	failures := []struct {
		oldPath, newPath string
		wantErr          error
	}{
		{oldPath: "m", newPath: "z", wantErr: fs.ErrPermission},
		{oldPath: "m/n", newPath: "m/y", wantErr: fs.ErrPermission},
		{oldPath: "m/d", newPath: "m/y", wantErr: fs.ErrPermission},
		{oldPath: "m/x.txt", newPath: "m/n", wantErr: fs.ErrPermission},
		{oldPath: "m/x.txt", newPath: "m/d", wantErr: fs.ErrExist},
		{oldPath: "m/x.txt", newPath: "other", wantErr: fs.ErrPermission},
		{oldPath: "m/x.txt", newPath: "m/dir/a.zip.d/x.txt", wantErr: fs.ErrPermission},
	}
	for _, tc := range failures {
		if err := Rename(fsys, tc.oldPath, tc.newPath); !errors.Is(err, tc.wantErr) {
			t.Errorf("Rename(%q, %q) = %v, want %v", tc.oldPath, tc.newPath, err, tc.wantErr)
		}
	}
	assertFileContent(t, fsys, "m/x.txt", "x")

	if err := Rename(fsys, "m/x.txt", "m/n/x.txt"); err != nil {
		t.Fatalf("Rename(m/x.txt, m/n/x.txt) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "m/n/x.txt", "x")
	assertNotExist(t, fsys, "m/x.txt")

	if err := Rename(fsys, "m/dir", "other/dir"); err != nil {
		t.Fatalf("Rename(m/dir, other/dir) = %v, want nil", err)
	}
	assertFileContent(t, other, "dir/f.txt", "f")
	assertFileContent(t, other, "dir/sub/g.txt", "g")
	assertNotExist(t, other, "dir/a.zip.d")
	assertFileContent(t, fsys, "other/dir/a.zip.d/in.txt", "in")
	assertNotExist(t, fsys, "m/dir")
	assertNotExist(t, fsys, "m/dir/a.zip.d/in.txt")
}

// TestNestFSRenameShadowing checks that a rename cannot put a file where a
// mount or an archive would hide it, or hide an existing file itself, and that
// an archive left mounted below a removed directory does not hide the one
// renamed there.
func TestNestFSRenameShadowing(t *testing.T) {
	t.Parallel()
	fsys := mustNestFS(t, "memory://")
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	writeTestFile(t, fsys, "a.zip", makeTestZip(t, "in"))
	assertFileContent(t, fsys, "a.zip.d/in.txt", "in")
	writeTestFile(t, fsys, "x.txt", []byte("x"))
	ufsTesting.Must(t, fsys.MkdirAll("y.zip.d", fs.ModePerm))
	writeTestFile(t, fsys, "y.zip.d/keep.txt", []byte("keep"))

	failures := []struct {
		oldPath, newPath string
		wantErr          error
	}{
		{oldPath: "x.txt", newPath: "y.zip", wantErr: fs.ErrExist},
		{oldPath: "x.txt", newPath: "a.zip.d", wantErr: fs.ErrPermission},
		{oldPath: "x.txt", newPath: "a.zip.d/x.txt", wantErr: fs.ErrPermission},
	}
	for _, tc := range failures {
		if err := Rename(fsys, tc.oldPath, tc.newPath); !errors.Is(err, tc.wantErr) {
			t.Errorf("Rename(%q, %q) = %v, want %v", tc.oldPath, tc.newPath, err, tc.wantErr)
		}
	}
	assertFileContent(t, fsys, "x.txt", "x")
	assertFileContent(t, fsys, "y.zip.d/keep.txt", "keep")
	assertNotExist(t, fsys, "y.zip")

	ufsTesting.Must(t, fsys.MkdirAll("s", fs.ModePerm))
	writeTestFile(t, fsys, "s/b.zip", makeTestZip(t, "in"))
	assertFileContent(t, fsys, "s/b.zip.d/in.txt", "in")
	ufsTesting.Must(t, fsys.RemoveAll("s"))
	ufsTesting.Must(t, fsys.MkdirAll("t", fs.ModePerm))
	writeTestFile(t, fsys, "t/b.zip", makeTestZip(t, "other"))
	if err := Rename(fsys, "t", "s"); err != nil {
		t.Fatalf("Rename(t, s) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "s/b.zip.d/in.txt", "other")
}

func TestNestFSRenameClosed(t *testing.T) {
	t.Parallel()
	fsys := mustNestFS(t, "memory://")
	ufsTesting.Must(t, fsys.Close())
	if err := Rename(fsys, "a", "b"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Rename(a, b) = %v, want %v", err, fs.ErrClosed)
	}
}
