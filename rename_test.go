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
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	w, err := zw.Create("in.txt")
	ufsTesting.Must(t, err)
	_, err = w.Write([]byte("in"))
	ufsTesting.Must(t, err)
	ufsTesting.Must(t, zw.Close())

	var other bytes.Buffer
	zw = zip.NewWriter(&other)
	w, err = zw.Create("in.txt")
	ufsTesting.Must(t, err)
	_, err = w.Write([]byte("other"))
	ufsTesting.Must(t, err)
	ufsTesting.Must(t, zw.Close())

	fsys := mustNestFS(t, "memory://")
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	ufsTesting.Must(t, fsys.MkdirAll("dir", fs.ModePerm))
	writeTestFile(t, fsys, "dir/a.zip", archive.Bytes())
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
	writeTestFile(t, fsys, "old.zip", archive.Bytes())
	assertFileContent(t, fsys, "old.zip.d/in.txt", "in")
	ufsTesting.Must(t, fsys.Remove("old.zip"))
	writeTestFile(t, fsys, "new.zip", other.Bytes())
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

func TestNestFSRenameClosed(t *testing.T) {
	t.Parallel()
	fsys := mustNestFS(t, "memory://")
	ufsTesting.Must(t, fsys.Close())
	if err := Rename(fsys, "a", "b"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Rename(a, b) = %v, want %v", err, fs.ErrClosed)
	}
}
