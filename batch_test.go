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

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// noBatchFS hides the BatchCreate of the file system it wraps, so that
// [BatchCreate] falls back to Create.
type noBatchFS struct {
	types.WriteFS
}

// batchRecorderFS implements types.BatchCreateFS by recording its batches.
type batchRecorderFS struct {
	types.WriteFS
	batches [][]types.BatchFile
}

func (fsys *batchRecorderFS) BatchCreate(files []types.BatchFile) error {
	fsys.batches = append(fsys.batches, files)
	return nil
}

func TestBatchCreateUsesBatchCreateFS(t *testing.T) {
	t.Parallel()
	base := mustBaseFS(t, "memory://")
	t.Cleanup(ufsTesting.ValidateClose(t, base))
	fsys := &batchRecorderFS{WriteFS: base}
	files := []types.BatchFile{{Name: "a/b.txt", Data: []byte("b")}, {Name: "c.txt"}}
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
		types.BatchFile{Name: "a.txt", Data: []byte("a")},
		types.BatchFile{Name: "dir", Data: []byte("dir")},
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
