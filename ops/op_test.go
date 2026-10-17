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

package ops_test

import (
	"archive/zip"
	"context"
	"io"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/core"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/ops"
)

const testLocalFSName = "../testing/testassets"

func newTestLocalFS(tb testing.TB) types.WriteFS {
	tb.Helper()
	fsys, err := ufs.New(tb.Context(), tb.TempDir())
	if err != nil {
		tb.Fatalf("cannot create localFS file system, %s", err)
	}
	return fsys
}

func newTestTempMountFS(tb testing.TB) types.WriteFS {
	tb.Helper()
	fsys, err := core.NewTempMountFS(tb.Context(), "test://", func(string) error { return nil })
	if err != nil {
		tb.Fatalf("cannot create tempMountFS file system, %s", err)
	}
	return fsys
}

func newTestMemFS(_ testing.TB) types.WriteFS {
	return core.MakeMemFS("memory://")
}

func newMemFS(name string) (types.WriteFS, error) {
	return core.MakeMemFS(name), nil
}

func newAngryFS(name string) (types.WriteFS, error) {
	return ufs.New(context.Background(), name)
}

// testAssetsFS returns the test assets as the source of the Rsync tests. It
// fails the test when the directory is missing or empty: os.DirFS does not
// report a missing directory, and Rsync of nothing would pass every check.
func testAssetsFS(t *testing.T) fs.FS {
	t.Helper()
	srcFS := osutil.DirFS(testLocalFSName)
	files, err := ops.ListFiles(srcFS, pathutil.CwdPath)
	if err != nil {
		t.Fatalf("ListFiles(%q) = %v, want nil", testLocalFSName, err)
	}
	if len(files) == 0 {
		t.Fatalf("%q holds no files, want the test assets", testLocalFSName)
	}
	return srcFS
}

// setupNestFSWithArchive creates a temp directory containing a regular file
// and a zip archive with one entry, then wraps it as a nestFS for Scan tests.
func setupNestFSWithArchive(t *testing.T) types.WriteFS {
	t.Helper()
	dir := t.TempDir()

	if err := osutil.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello")); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(dir, "data.zip")
	zf, err := osutil.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("inside.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "content"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}

	nfs, err := ufs.New(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := nfs.Close(); err != nil {
			t.Errorf("failed to close nest FS: %v", err)
		}
	})
	return nfs
}
