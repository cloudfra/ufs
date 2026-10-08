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
	"fmt"
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
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

	wantFiles := loadTestAssets(t)

	testCases := []struct {
		name     string
		createFS func(tb testing.TB) (WriteFS, error)
	}{
		{
			name: "localFS",
			createFS: func(tb testing.TB) (WriteFS, error) {
				return newLocalFS(tb.Context(), testAssetsFilesDir)
			},
		},
		{
			name: "memFS",
			createFS: func(tb testing.TB) (WriteFS, error) {
				fsys, err := newMemFS(tb.Context(), "memory://")
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
			createFS: func(tb testing.TB) (WriteFS, error) {
				return newMemArchiveFS(tb, "testassets.zip", zipBytesFromDir(tb, testAssetsFilesDir))
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
	src := osutil.DirFS(testAssetsFilesDir)
	result := make(map[string][]byte)
	err := fs.WalkDir(src, pathutil.CwdPath, func(p string, d fs.DirEntry, err error) error {
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
func copyFSToFS(src fs.FS, dst WriteFS) error {
	return fs.WalkDir(src, pathutil.CwdPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == pathutil.CwdPath {
			return err
		}
		if d.IsDir() {
			return dst.MkdirAll(p, fs.ModePerm)
		}
		return Copy(src, p, dst, p)
	})
}

// zipEntry is one entry of an archive built by zipBytes. A name ending in "/"
// is a directory entry and its data is ignored.
type zipEntry struct {
	name string
	data []byte
}

// zipBytes returns a zip archive holding entries, built in memory.
func zipBytes(tb testing.TB, entries ...zipEntry) []byte {
	tb.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		w, err := zw.Create(entry.name)
		if err != nil {
			tb.Fatalf("zip Create(%q) = %v, want nil", entry.name, err)
		}
		if strings.HasSuffix(entry.name, "/") {
			continue
		}
		if _, err := w.Write(entry.data); err != nil {
			tb.Fatalf("zip Write(%q) = %v, want nil", entry.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		tb.Fatalf("zip Close() = %v, want nil", err)
	}
	return buf.Bytes()
}

// zipBytesFromDir returns a zip archive, built in memory, of every file under
// the local directory dir.
func zipBytesFromDir(tb testing.TB, dir string) []byte {
	tb.Helper()
	src := osutil.DirFS(dir)

	var entries []zipEntry
	err := fs.WalkDir(src, pathutil.CwdPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == pathutil.CwdPath {
			return err
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		entries = append(entries, zipEntry{name: p, data: data})
		return nil
	})
	if err != nil {
		tb.Fatalf("zipBytesFromDir: walk: %v", err)
	}
	return zipBytes(tb, entries...)
}

// writeTestFile writes data to name in fsys, creating its parent directories.
func writeTestFile(tb testing.TB, fsys WriteFS, name string, data []byte) {
	tb.Helper()
	if dir := path.Dir(name); dir != pathutil.CwdPath {
		if err := fsys.MkdirAll(dir, osutil.DefaultDirectoryPermissions); err != nil {
			tb.Fatalf("MkdirAll(%q) = %v, want nil", dir, err)
		}
	}
	f, err := fsys.Create(name)
	if err != nil {
		tb.Fatalf("Create(%q) = %v, want nil", name, err)
	}
	if _, err := f.Write(data); err != nil {
		tb.Errorf("Write(%q) = %v, want nil", name, err)
	}
	if err := f.Close(); err != nil {
		tb.Fatalf("Close(%q) = %v, want nil", name, err)
	}
}

// newMemArchiveFS mounts the archive in data, held in memory under name, as an
// archiveFS. The caller closes the result; nothing is written to disk.
func newMemArchiveFS(tb testing.TB, name string, data []byte) (*archiveFS, error) {
	tb.Helper()
	mfs := makeMemFS("memory:///")
	tb.Cleanup(func() {
		if err := mfs.Close(); err != nil {
			tb.Errorf("failed to close memFS: %v", err)
		}
	})
	writeTestFile(tb, mfs, name, data)
	f, err := mfs.Open(name)
	if err != nil {
		return nil, err
	}
	fsys, err := newArchiveFSFromFile(tb.Context(), f)
	if err != nil {
		return nil, ufserrors.Join(err, f.Close())
	}
	return fsys, nil
}
