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
	"testing"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

// noBatchFS hides the BatchCreate of the file system it wraps, so that
// [BatchCreate] falls back to Create.
type noBatchFS struct {
	WriteFS
}

// batchRecorderFS implements BatchCreateFS by recording its batches.
type batchRecorderFS struct {
	WriteFS
	batches [][]BatchFile
}

func (fsys *batchRecorderFS) BatchCreate(files []BatchFile) error {
	fsys.batches = append(fsys.batches, files)
	return nil
}

func TestBatchCreateUsesBatchCreateFS(t *testing.T) {
	t.Parallel()
	base := mustBaseFS(t, "memory://")
	t.Cleanup(ufsTesting.ValidateClose(t, base))
	fsys := &batchRecorderFS{WriteFS: base}
	files := []BatchFile{{Name: "a/b.txt", Data: []byte("b")}, {Name: "c.txt"}}
	if err := BatchCreate(fsys, files...); err != nil {
		t.Fatalf("BatchCreate() = %v, want nil", err)
	}
	if len(fsys.batches) != 1 || len(fsys.batches[0]) != len(files) {
		t.Fatalf("batches = %v, want one batch of %d files", fsys.batches, len(files))
	}
	// The batch is the file system's to write; the fallback did not run.
	assertNotExist(t, base, "a/b.txt")
}

// TestBatchCreateFallbackError checks that the fallback names the file that
// failed, and leaves the files before it written.
func TestBatchCreateFallbackError(t *testing.T) {
	t.Parallel()
	fsys := mustBaseFS(t, "memory://")
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	ufsTesting.Must(t, fsys.MkdirAll("dir", fs.ModePerm))

	err := BatchCreate(noBatchFS{fsys},
		BatchFile{Name: "a.txt", Data: []byte("a")},
		BatchFile{Name: "dir", Data: []byte("dir")},
	)
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || pathErr.Op != batchCreateOp || pathErr.Path != "dir" {
		t.Fatalf("BatchCreate() = %#v, want an *fs.PathError for %s of dir", err, batchCreateOp)
	}
	var inner *fs.PathError
	if errors.As(pathErr.Err, &inner) {
		t.Errorf("BatchCreate() = %v, wraps a second *fs.PathError", err)
	}
	assertFileContent(t, fsys, "a.txt", "a")
}

// TestBatchCreateThroughMounts checks that a batch is split by mount: each
// mount's files are written by its own BatchCreate, so atomically, in the
// order of their first file, with errors naming the full paths, and a mount
// without BatchCreate is written file by file.
func TestBatchCreateThroughMounts(t *testing.T) {
	t.Parallel()
	plain := mustBaseFS(t, "memory://")
	fsys, err := NewFSBuilder("memory://").Mount("m", "memory://").MountFS("plain", noBatchFS{plain}).Build(t.Context())
	ufsTesting.Must(t, err)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	ufsTesting.Must(t, fsys.MkdirAll("m/dir", fs.ModePerm))
	if _, ok := fsys.(BatchCreateFS); ok {
		t.Fatalf("%T implements BatchCreateFS, but cannot write across mounts atomically", fsys)
	}

	err = BatchCreate(fsys,
		BatchFile{Name: "m/a.txt", Data: []byte("a")},
		BatchFile{Name: "base/b.txt", Data: []byte("b")},
		BatchFile{Name: "plain/c.txt", Data: []byte("c")},
		BatchFile{Name: "m/sub/d.txt", Data: []byte("d")},
	)
	if err != nil {
		t.Fatalf("BatchCreate() across mounts = %v, want nil", err)
	}
	for name, want := range map[string]string{"m/a.txt": "a", "base/b.txt": "b", "plain/c.txt": "c", "m/sub/d.txt": "d"} {
		assertFileContent(t, fsys, name, want)
	}
	assertFileContent(t, plain, "c.txt", "c")

	// The base part comes first and is written; the part for m fails as a
	// whole, before m/new/x.txt; the plain part after it is not written.
	err = BatchCreate(fsys,
		BatchFile{Name: "base/ok.txt", Data: []byte("ok")},
		BatchFile{Name: "m/new/x.txt", Data: []byte("x")},
		BatchFile{Name: "plain/later.txt", Data: []byte("later")},
		BatchFile{Name: "m/dir", Data: []byte("conflict")},
	)
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || pathErr.Path != "m/dir" || pathErr.Op != batchCreateOp {
		t.Fatalf("BatchCreate() = %v, want an *fs.PathError for %s of m/dir", err, batchCreateOp)
	}
	assertFileContent(t, fsys, "base/ok.txt", "ok")
	assertNotExist(t, fsys, "m/new")
	assertNotExist(t, fsys, "plain/later.txt")
}

// TestBatchCreateMountChecks checks that a file inside an archive, at a mount
// point or above one fails before any file of the batch is written.
func TestBatchCreateMountChecks(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory://?mnt/one=memory://")
	ufsTesting.Must(t, err)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	writeTestFile(t, fsys, "a.zip", makeTestZip(t, "in"))

	tests := []struct {
		name    string
		wantErr error
	}{
		{name: "a.zip.d/x.txt", wantErr: fs.ErrPermission},
		{name: "a.zip.d", wantErr: fs.ErrPermission},
		{name: "mnt/one", wantErr: fs.ErrInvalid},
		{name: "mnt", wantErr: fs.ErrExist},
	}
	for _, tc := range tests {
		err := BatchCreate(fsys, BatchFile{Name: "first.txt"}, BatchFile{Name: tc.name})
		var pathErr *fs.PathError
		if !errors.Is(err, tc.wantErr) || !errors.As(err, &pathErr) || pathErr.Path != tc.name {
			t.Errorf("BatchCreate(%q) = %v, want an *fs.PathError for it wrapping %v", tc.name, err, tc.wantErr)
		}
		assertNotExist(t, fsys, "first.txt")
	}
}

// TestBatchCreateReplacesArchive checks that an archive replaced by a batch
// is read again, not from the mount of the archive it replaced.
func TestBatchCreateReplacesArchive(t *testing.T) {
	t.Parallel()
	fsys := mustNestFS(t, "memory://")
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	writeTestFile(t, fsys, "dir/a.zip", makeTestZip(t, "in"))
	assertFileContent(t, fsys, "dir/a.zip.d/in.txt", "in")

	ufsTesting.Must(t, BatchCreate(fsys, BatchFile{Name: "dir/a.zip", Data: makeTestZip(t, "other")}))
	assertFileContent(t, fsys, "dir/a.zip.d/in.txt", "other")
}
