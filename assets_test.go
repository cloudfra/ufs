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
	"bytes"
	"fmt"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/testing/archivetest"
	"github.com/cloudfra/ufs/internal/ufserrors"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

const testAssetsFilesDir = "testing/testassets/files"

// TestAssets verifies that each FS backend serves the testassets files with
// correct contents. Each backend loads the assets in the fastest way available:
// localFS mounts the directory directly, memFS copies files into memory, and
// archiveFS builds a zip on the fly and mounts it.
func TestAssets(t *testing.T) {
	t.Parallel()

	wantFiles := ufsTesting.ReadDirFiles(t, testAssetsFilesDir)

	testCases := []struct {
		name     string
		createFS func(tb testing.TB) (types.WriteFS, error)
	}{
		{
			name: "localFS",
			createFS: func(tb testing.TB) (types.WriteFS, error) {
				return newBaseFS(tb.Context(), testAssetsFilesDir)
			},
		},
		{
			name: "memFS",
			createFS: func(tb testing.TB) (types.WriteFS, error) {
				fsys, err := newBaseFS(tb.Context(), "memory://")
				if err != nil {
					return nil, err
				}
				if err := copyFSToFS(osutil.DirFS(testAssetsFilesDir), fsys); err != nil {
					if closeErr := fsys.Close(); closeErr != nil {
						return nil, ufserrors.Join(err, fmt.Errorf("failed to close FS after error: %v", closeErr))
					}
					return nil, err
				}
				return fsys, nil
			},
		},
		{
			name: "archiveFS",
			createFS: func(tb testing.TB) (types.WriteFS, error) {
				return newMemArchiveFS(tb, "testassets.zip", archivetest.ZipDir(tb, testAssetsFilesDir))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys, err := tc.createFS(t)
			if err != nil {
				t.Fatalf("create FS: %v", err)
			}
			defer ufsTesting.ValidateClose(t, fsys)()

			for filePath, wantData := range wantFiles {
				t.Run(filePath, func(t *testing.T) {
					got, err := fs.ReadFile(fsys, filePath)
					if err != nil {
						t.Fatalf("types.ReadFile(%q) = %v, want nil", filePath, err)
					}
					if !bytes.Equal(got, wantData) {
						t.Errorf("types.ReadFile(%q): got %d bytes, want %d bytes", filePath, len(got), len(wantData))
					}
				})
			}
		})
	}
}

// copyFSToFS copies all files and directories from src into dst.
func copyFSToFS(src fs.FS, dst types.WriteFS) error {
	return fs.WalkDir(src, pathutil.CwdPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == pathutil.CwdPath {
			return err
		}
		if d.IsDir() {
			return dst.MkdirAll(p, fs.ModePerm)
		}
		return copyFileForTest(src, dst, p)
	})
}

// copyFileForTest copies the file name from src to the same path in dst.
func copyFileForTest(src fs.FS, dst types.WriteFS, name string) error {
	data, err := fs.ReadFile(src, name)
	if err != nil {
		return err
	}
	f, err := dst.Create(name)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return ufserrors.Join(err, f.Close())
	}
	return f.Close()
}

// newMemArchiveFS mounts the archive in data, held in memory under name,
// through the archive driver. The caller closes the result; nothing is written
// to disk.
func newMemArchiveFS(tb testing.TB, name string, data []byte) (types.WriteFS, error) {
	tb.Helper()
	return newBaseFSFromFile(tb.Context(), archiveURIPrefix+name, archivetest.NewFile(name, data))
}
