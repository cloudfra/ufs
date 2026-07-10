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
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"testing"
)

const testAssetsFilesDir = "testing/testassets/files"

// TestAssets verifies that each FS backend serves the testassets files with
// correct contents. Each backend loads the assets in the fastest way available:
// localFS mounts the directory directly, memFS copies files into memory, and
// archiveFS builds a zip on the fly and mounts it.
func TestAssets(t *testing.T) {
	t.Parallel()

	wantFiles := loadTestAssets(t)

	testCases := []struct {
		name     string
		createFS func(tb testing.TB) (FS, error)
	}{
		{
			name: "localFS",
			createFS: func(_ testing.TB) (FS, error) {
				return newLocalFS(testAssetsFilesDir)
			},
		},
		{
			name: "memFS",
			createFS: func(_ testing.TB) (FS, error) {
				fsys, err := newMemFS("mem://")
				if err != nil {
					return nil, err
				}
				if err := copyFSToFS(os.DirFS(testAssetsFilesDir), fsys); err != nil {
					if closeErr := fsys.Close(); closeErr != nil {
						return nil, joinErrors(err, fmt.Errorf("failed to close FS after error: %v", closeErr))
					}
					return nil, err
				}
				return fsys, nil
			},
		},
		{
			name: "archiveFS",
			createFS: func(tb testing.TB) (FS, error) {
				zipPath := createZipFromDir(tb, testAssetsFilesDir)
				return newArchiveFSFromLocalFS(context.Background(), zipPath)
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
			defer func() {
				if err := fsys.Close(); err != nil {
					t.Errorf("failed to close FS: %v", err)
				}
			}()

			for filePath, wantData := range wantFiles {
				t.Run(filePath, func(t *testing.T) {
					got, err := fs.ReadFile(fsys, filePath)
					if err != nil {
						t.Fatalf("ReadFile(%q) = %v, want nil", filePath, err)
					}
					if !bytes.Equal(got, wantData) {
						t.Errorf("ReadFile(%q): got %d bytes, want %d bytes", filePath, len(got), len(wantData))
					}
				})
			}
		})
	}
}

// loadTestAssets walks testAssetsFilesDir and returns a path→content map for every file.
func loadTestAssets(tb testing.TB) map[string][]byte {
	tb.Helper()
	src := os.DirFS(testAssetsFilesDir)
	result := make(map[string][]byte)
	err := fs.WalkDir(src, cwdPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		result[p] = data
		return nil
	})
	if err != nil {
		tb.Fatalf("loadTestAssets: %v", err)
	}
	return result
}

// copyFSToFS copies all files and directories from src into dst.
func copyFSToFS(src fs.FS, dst FS) error {
	return fs.WalkDir(src, cwdPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == cwdPath {
			return err
		}
		if d.IsDir() {
			return dst.MkdirAll(p, fs.ModePerm)
		}
		return Copy(src, p, dst, p)
	})
}

// createZipFromDir walks dir, writes all files into a temp zip, and returns its path.
// The caller does not need to remove the file; tb.Cleanup handles it.
func createZipFromDir(tb testing.TB, dir string) string {
	tb.Helper()
	src := os.DirFS(dir)

	tmp, err := os.CreateTemp("", "testassets-*.zip")
	if err != nil {
		tb.Fatalf("createZipFromDir: CreateTemp: %v", err)
	}
	tmpName := tmp.Name()
	tb.Cleanup(func() {
		if err := os.Remove(tmpName); err != nil {
			tb.Fatalf("createZipFromDir: Cleanup: %v", err)
		}
	})

	zw := zip.NewWriter(tmp)
	err = fs.WalkDir(src, cwdPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == cwdPath {
			return err
		}
		w, err := zw.Create(p)
		if err != nil {
			return err
		}
		f, err := src.Open(p)
		if err != nil {
			return err
		}
		defer func() {
			if err := f.Close(); err != nil {
				tb.Errorf("createZipFromDir: failed to close file: %v", err)
			}
		}()
		_, err = io.Copy(w, f)
		return err
	})
	if err != nil {
		tb.Fatalf("createZipFromDir: walk: %v", err)
	}
	if err := zw.Close(); err != nil {
		tb.Fatalf("createZipFromDir: close zip: %v", err)
	}
	if err := tmp.Close(); err != nil {
		tb.Fatalf("createZipFromDir: close file: %v", err)
	}
	return tmpName
}
