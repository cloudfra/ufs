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

package host

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudfra/ufs"
)

const (
	testFilePermissions      = 0o600
	testDirectoryPermissions = 0o750
)

func osMkdir(name string) error {
	return os.Mkdir(filepath.Clean(name), testDirectoryPermissions)
}

func osMkdirAll(name string) error {
	return os.MkdirAll(filepath.Clean(name), testDirectoryPermissions)
}

func osRemove(name string) error {
	return os.Remove(filepath.Clean(name))
}

func osReadFile(name string) ([]byte, error) {
	return os.ReadFile(filepath.Clean(name))
}

func osStat(name string) (os.FileInfo, error) {
	return os.Stat(filepath.Clean(name))
}

func osWriteFile(name string, data []byte) error {
	return os.WriteFile(filepath.Clean(name), data, testFilePermissions)
}

func osReadDir(name string) ([]os.DirEntry, error) {
	return os.ReadDir(filepath.Clean(name))
}

func osDirFS(name string) fs.FS {
	return os.DirFS(filepath.Clean(name))
}

func validateClose(tb testing.TB, closer io.Closer) func() {
	return func() {
		tb.Helper()
		if closer != nil {
			if err := closer.Close(); err != nil {
				tb.Errorf("failed to close %s, %s", closer, err)
			}
		}
	}
}

// fakeInfo is a minimal fs.FileInfo for unit tests.
type fakeInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

func (fi *fakeInfo) Name() string       { return fi.name }
func (fi *fakeInfo) Size() int64        { return fi.size }
func (fi *fakeInfo) Mode() fs.FileMode  { return fi.mode }
func (fi *fakeInfo) ModTime() time.Time { return fi.modTime }
func (fi *fakeInfo) IsDir() bool        { return fi.isDir }
func (fi *fakeInfo) Sys() any           { return nil }

// testAssetsFilesDir is the directory of files that were archived into
// testassets.tar.gz by the test asset generation step.
var testAssetsFilesDir = filepath.Join("..", "testing", "testassets", "files")

// loadTestAssets walks testAssetsFilesDir and returns a path→content map for every file.
func loadTestAssets(tb testing.TB) map[string][]byte {
	tb.Helper()
	src := osDirFS(testAssetsFilesDir)
	result := make(map[string][]byte)
	err := fs.WalkDir(src, ufs.CwdPath, func(p string, d fs.DirEntry, err error) error {
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
