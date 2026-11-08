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

package ufs

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs/ops"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestNestFSCopyFileAcrossMounts checks that a copy between two mounts reads
// from one file system and writes into the other, leaving the source behind.
func TestNestFSCopyFileAcrossMounts(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory://?one=memory://&two=memory://")
	ufsTesting.Must(t, err)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	ufsTesting.Must(t, fsys.MkdirAll("two/dir", fs.ModePerm))
	writeTestFile(t, fsys, "one/f.txt", []byte("f"))
	writeTestFile(t, fsys, "two/dir/that.txt", []byte("that"))

	if err := ops.Copy(fsys, "one/f.txt", fsys, "two/dir/copy.txt"); err != nil {
		t.Fatalf("Copy(one/f.txt, two/dir/copy.txt) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "two/dir/copy.txt", "f")
	assertFileContent(t, fsys, "one/f.txt", "f")

	if err := ops.Copy(fsys, "one/f.txt", fsys, "two/dir/that.txt"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Copy(one/f.txt, two/dir/that.txt) = %v, want %v", err, fs.ErrExist)
	}
	assertFileContent(t, fsys, "two/dir/that.txt", "that")

	if err := ops.Copy(fsys, "one/missing.txt", fsys, "two/copy.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Copy(one/missing.txt, two/copy.txt) = %v, want %v", err, fs.ErrNotExist)
	}
	assertNotExist(t, fsys, "two/copy.txt")
}

// TestNestFSCopyFileWithinMount checks a copy made inside one mounted file
// system, which keeps the source and refuses an existing destination.
func TestNestFSCopyFileWithinMount(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory://?one=memory://")
	ufsTesting.Must(t, err)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	ufsTesting.Must(t, fsys.MkdirAll("one/dir", fs.ModePerm))
	writeTestFile(t, fsys, "one/f.txt", []byte("f"))
	writeTestFile(t, fsys, "one/dir/that.txt", []byte("that"))

	if err := ops.Copy(fsys, "one/f.txt", fsys, "one/dir/copy.txt"); err != nil {
		t.Fatalf("Copy(one/f.txt, one/dir/copy.txt) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "one/dir/copy.txt", "f")
	assertFileContent(t, fsys, "one/f.txt", "f")

	if err := ops.Copy(fsys, "one/f.txt", fsys, "one/dir/that.txt"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Copy(one/f.txt, one/dir/that.txt) = %v, want %v", err, fs.ErrExist)
	}
	assertFileContent(t, fsys, "one/dir/that.txt", "that")
}

// TestNestFSCopyFileMountPoints checks that a copy refuses a mount point and a
// path that is a directory in the nest only because a mount is below it.
func TestNestFSCopyFileMountPoints(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory://?mnt/one=memory://&other=memory://")
	ufsTesting.Must(t, err)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	writeTestFile(t, fsys, "x.txt", []byte("x"))
	writeTestFile(t, fsys, "mnt/one/f.txt", []byte("f"))

	tests := []struct {
		srcPath, dstPath string
		wantErr          error
	}{
		{srcPath: "mnt/one", dstPath: "y.txt", wantErr: fs.ErrPermission},
		{srcPath: "mnt", dstPath: "y.txt", wantErr: fs.ErrPermission},
		{srcPath: "x.txt", dstPath: "mnt/one", wantErr: fs.ErrPermission},
		{srcPath: "x.txt", dstPath: "mnt", wantErr: fs.ErrExist},
		{srcPath: "x.txt", dstPath: "other", wantErr: fs.ErrPermission},
	}
	for _, tc := range tests {
		if err := ops.Copy(fsys, tc.srcPath, fsys, tc.dstPath); !errors.Is(err, tc.wantErr) {
			t.Errorf("Copy(%q, %q) = %v, want %v", tc.srcPath, tc.dstPath, err, tc.wantErr)
		}
	}
	assertFileContent(t, fsys, "x.txt", "x")
	assertFileContent(t, fsys, "mnt/one/f.txt", "f")
	assertNotExist(t, fsys, "y.txt")
}

// TestNestFSCopyFileNestedMount checks a copy whose path passes through a
// nestFS mounted as a file system, which has mounts of its own.
func TestNestFSCopyFileNestedMount(t *testing.T) {
	t.Parallel()
	inner, err := New(t.Context(), "memory://?n=memory://&d/deep=memory://")
	ufsTesting.Must(t, err)
	other := mustBaseFS(t, "memory://")
	fsys, err := NewFSBuilder("memory://").MountFS("m", inner).MountFS("other", other).Build(t.Context())
	ufsTesting.Must(t, err)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))

	writeTestFile(t, fsys, "m/x.txt", []byte("x"))

	if err := ops.Copy(fsys, "m/x.txt", fsys, "m/n/copy.txt"); err != nil {
		t.Fatalf("Copy(m/x.txt, m/n/copy.txt) = %v, want nil", err)
	}
	assertFileContent(t, inner, "n/copy.txt", "x")
	assertFileContent(t, fsys, "m/x.txt", "x")

	failures := []struct {
		srcPath, dstPath string
		wantErr          error
	}{
		{srcPath: "m/x.txt", dstPath: "m/n", wantErr: fs.ErrPermission},
		{srcPath: "m/x.txt", dstPath: "m/d", wantErr: fs.ErrExist},
		{srcPath: "m/n", dstPath: "other/x.txt", wantErr: fs.ErrPermission},
		{srcPath: "m/d", dstPath: "other/x.txt", wantErr: fs.ErrPermission},
	}
	for _, tc := range failures {
		if err := ops.Copy(fsys, tc.srcPath, fsys, tc.dstPath); !errors.Is(err, tc.wantErr) {
			t.Errorf("Copy(%q, %q) = %v, want %v", tc.srcPath, tc.dstPath, err, tc.wantErr)
		}
	}
	assertNotExist(t, other, "x.txt")
}

// TestNestFSCopyFileArchive checks that a copy onto an archive file whose
// content is still mounted from a removed archive remounts that content, that
// the content itself cannot be written to, and that an archive cannot hide a
// content directory that is already used.
func TestNestFSCopyFileArchive(t *testing.T) {
	t.Parallel()
	fsys := mustNestFS(t, "memory://")
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	writeTestFile(t, fsys, "a.zip", makeTestZip(t, "in"))
	// Reading the content directory of an archive mounts it.
	assertFileContent(t, fsys, "a.zip.d/in.txt", "in")
	writeTestFile(t, fsys, "b.zip", makeTestZip(t, "other"))
	ufsTesting.Must(t, fsys.RemoveAll("a.zip"))

	if err := ops.Copy(fsys, "b.zip", fsys, "a.zip"); err != nil {
		t.Fatalf("Copy(b.zip, a.zip) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "a.zip.d/in.txt", "other")

	ufsTesting.Must(t, fsys.MkdirAll("y.zip.d", fs.ModePerm))
	writeTestFile(t, fsys, "y.zip.d/keep.txt", []byte("keep"))
	if err := ops.Copy(fsys, "b.zip", fsys, "y.zip"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Copy(b.zip, y.zip) = %v, want %v", err, fs.ErrExist)
	}
	assertNotExist(t, fsys, "y.zip")
	assertFileContent(t, fsys, "y.zip.d/keep.txt", "keep")

	if err := ops.Copy(fsys, "b.zip", fsys, "a.zip.d/new.txt"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Copy(b.zip, a.zip.d/new.txt) = %v, want %v", err, fs.ErrPermission)
	}
	assertFileContent(t, fsys, "a.zip.d/in.txt", "other")
}

// TestNestFSCopyFileClosed checks that a copy through a closed nestFS fails
// without touching any mount.
func TestNestFSCopyFileClosed(t *testing.T) {
	t.Parallel()
	fsys := mustNestFS(t, "memory://?one=memory://")
	ufsTesting.Must(t, fsys.Close())
	if err := ops.Copy(fsys, "a.txt", fsys, "one/b.txt"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Copy(a.txt, one/b.txt) = %v, want %v", err, fs.ErrClosed)
	}
}
