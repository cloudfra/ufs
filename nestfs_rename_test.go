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
	"testing"

	"github.com/cloudfra/ufs/ops"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

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

func TestNestFSRenameAcrossMounts(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory://?one=memory://&two=memory://")
	ufsTesting.Must(t, err)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	ufsTesting.Must(t, fsys.MkdirAll("one/dir/sub", fs.ModePerm))
	writeTestFile(t, fsys, "one/dir/f.txt", []byte("f"))
	writeTestFile(t, fsys, "one/dir/sub/g.txt", []byte("g"))
	writeTestFile(t, fsys, "base.txt", []byte("base"))

	if err := ops.Rename(fsys, "one/dir", "two/moved"); err != nil {
		t.Fatalf("Rename(one/dir, two/moved) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "two/moved/f.txt", "f")
	assertFileContent(t, fsys, "two/moved/sub/g.txt", "g")
	assertNotExist(t, fsys, "one/dir")

	if err := ops.Rename(fsys, "base.txt", "one/base.txt"); err != nil {
		t.Fatalf("Rename(base.txt, one/base.txt) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "one/base.txt", "base")
	assertNotExist(t, fsys, "base.txt")

	if err := ops.Rename(fsys, "one/base.txt", "two/moved/f.txt"); !errors.Is(err, fs.ErrExist) {
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
		if err := ops.Rename(fsys, tc.oldPath, tc.newPath); !errors.Is(err, tc.wantErr) {
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

	if err := ops.Rename(fsys, "dir/a.zip", "dir/b.zip"); err != nil {
		t.Fatalf("Rename(dir/a.zip, dir/b.zip) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "dir/b.zip.d/in.txt", "in")
	assertNotExist(t, fsys, "dir/a.zip.d/in.txt")

	if err := ops.Rename(fsys, "dir", "dir2"); err != nil {
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
	if err := ops.Rename(fsys, "new.zip", "old.zip"); err != nil {
		t.Fatalf("Rename(new.zip, old.zip) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "old.zip.d/in.txt", "other")

	if err := ops.Rename(fsys, "dir2/b.zip.d", "c"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Rename(dir2/b.zip.d, c) = %v, want %v", err, fs.ErrPermission)
	}
	if err := ops.Rename(fsys, "dir2/b.zip.d/in.txt", "in.txt"); !errors.Is(err, fs.ErrPermission) {
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
		if err := ops.Rename(fsys, tc.oldPath, tc.newPath); !errors.Is(err, tc.wantErr) {
			t.Errorf("Rename(%q, %q) = %v, want %v", tc.oldPath, tc.newPath, err, tc.wantErr)
		}
	}
	assertFileContent(t, fsys, "m/x.txt", "x")

	if err := ops.Rename(fsys, "m/x.txt", "m/n/x.txt"); err != nil {
		t.Fatalf("Rename(m/x.txt, m/n/x.txt) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "m/n/x.txt", "x")
	assertNotExist(t, fsys, "m/x.txt")

	if err := ops.Rename(fsys, "m/dir", "other/dir"); err != nil {
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
		if err := ops.Rename(fsys, tc.oldPath, tc.newPath); !errors.Is(err, tc.wantErr) {
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
	if err := ops.Rename(fsys, "t", "s"); err != nil {
		t.Fatalf("Rename(t, s) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "s/b.zip.d/in.txt", "other")
}

func TestNestFSRenameClosed(t *testing.T) {
	t.Parallel()
	fsys := mustNestFS(t, "memory://")
	ufsTesting.Must(t, fsys.Close())
	if err := ops.Rename(fsys, "a", "b"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Rename(a, b) = %v, want %v", err, fs.ErrClosed)
	}
}
