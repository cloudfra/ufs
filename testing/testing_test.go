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

package testing

import (
	"archive/zip"
	"io"
	"path/filepath"
	"testing"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/google/go-cmp/cmp"
)

// writeTestDir writes files, keyed by slash-separated path, below a new
// temporary directory and returns the directory.
func writeTestDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		filename := filepath.Join(dir, filepath.FromSlash(name))
		if err := osutil.MkdirAll(filepath.Dir(filename)); err != nil {
			t.Fatal(err)
		}
		if err := osutil.WriteFile(filename, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReadDirFiles(t *testing.T) {
	dir := writeTestDir(t, map[string]string{"a.txt": "A", "sub/b.txt": "B"})
	want := map[string][]byte{"a.txt": []byte("A"), "sub/b.txt": []byte("B")}
	if diff := cmp.Diff(want, ReadDirFiles(t, dir)); diff != "" {
		t.Errorf("ReadDirFiles() mismatch (-want +got):\n%s", diff)
	}
}

func TestZipDir(t *testing.T) {
	dir := writeTestDir(t, map[string]string{"a.txt": "A", "sub/b.txt": "B"})
	zr, err := zip.OpenReader(ZipDir(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	defer ValidateClose(t, zr)()

	got := map[string]string{}
	for _, zf := range zr.File {
		r, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		got[zf.Name] = string(data)
	}
	want := map[string]string{"a.txt": "A", "sub/b.txt": "B"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ZipDir() content mismatch (-want +got):\n%s", diff)
	}
}

func TestTestAssetsFS(t *testing.T) {
	fsys := TestAssetsFS()
	want := "testing/testassets/files/index.html"
	data, err := fsys.ReadFile("testassets/files/index.html")
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if got != want {
		t.Errorf("got: %q, want: %q", got, want)
	}
}

func TestTestAssetsArchivesFS(t *testing.T) {
	fsys := TestAssetsArchivesFS()
	data, err := fsys.ReadFile("testassets/archives/testassets.tar.xz")
	if err != nil {
		t.Fatal(err)
	}
	gotFileSize := len(data)
	if gotFileSize < 1000 {
		t.Errorf("got: %d, want: > %d", gotFileSize, 1000)
	}
}
