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
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func dirEntryToNames(entries []fs.DirEntry) []string {
	result := make([]string, len(entries))
	for idx, entry := range entries {
		result[idx] = entry.Name()
	}
	return result
}

func TestNestedFS(t *testing.T) {
	fsys, err := New("memory://?a=file:///&mounted/null=null://&mounted/angry=angry://")
	if err != nil {
		t.Fatal(err)
	}
	testCases := []struct {
		dir         string
		wantEntries []string
	}{
		{
			dir:         ".",
			wantEntries: []string{"a", "mounted"},
		},
		{
			dir:         "mounted",
			wantEntries: []string{"angry", "null"},
		},
		{
			dir:         "mounted/null",
			wantEntries: []string{"."},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.dir, func(t *testing.T) {
			entries, err := fs.ReadDir(fsys, tc.dir)
			if err != nil {
				t.Fatal(err)
			}
			got := dirEntryToNames(entries)
			if diff := cmp.Diff(got, tc.wantEntries); diff != "" {
				t.Errorf("ReadDir(.), got: %v want: %v, diff: %s", got, tc.wantEntries, diff)
			}
		})
	}
}

func TestNewNestFS(t *testing.T) {
	fsys, err := newNestFS("memory://")
	if err != nil {
		t.Fatal(err)
	}
	if fsys == nil {
		t.Fatal("fsys is nil")
	}
}

func TestNewNestFSInvalid(t *testing.T) {
	_, err := newNestFS("invalid://scheme")
	if err == nil {
		t.Fatal("newNestFS with invalid scheme should return an error")
	}
}

func xTestNestFSFull(t *testing.T) {
	fsys, err := newNestFS("/home/coder/project/hand/ufs")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, fsys, "testing/testassets/files/index.html", "testing/testassets/files/index.html")
	assertContains(t, fsys, "testing/testassets/archives/nested-testassets.zip.d/site.js", "testing/testassets/files/site.js")
	assertContains(t, fsys, "testing/testassets/archives/nested-testassets.zip.d/single-testassets.zip.d/index.html", "testing/testassets/files/index.html")
}

func assertContains(t *testing.T, fsys FS, name string, substr string) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), substr) {
		t.Errorf("%q does not contain %q, (len: %d) %q", name, substr, len(data), string(data))
	}
}

func TestNestFSReadDir(t *testing.T) {
	fsys, err := newNestFS("memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	nfs := fsys.(*nestFS)
	if err := nfs.MkdirAll("subdir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("subdir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	entries, err := nfs.ReadDir("subdir")
	if err != nil {
		t.Fatalf("ReadDir(subdir) = %v, want nil", err)
	}
	if len(entries) != 1 {
		t.Errorf("ReadDir(subdir) returned %d entries, want 1", len(entries))
	}
	if entries[0].Name() != "file.txt" {
		t.Errorf("ReadDir entry name = %q, want %q", entries[0].Name(), "file.txt")
	}
}

func TestNestFSReadDirOnFile(t *testing.T) {
	fsys, err := newNestFS("memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	nfs := fsys.(*nestFS)
	f, err := fsys.Create("regular.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, err = nfs.ReadDir("regular.txt")
	if err == nil {
		t.Error("ReadDir on a regular file should return an error")
	}
}

func TestGetPotentialArchives(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		input string
		want  []string
	}{
		{
			input: "",
			want:  []string{},
		},
		{
			input: ".",
			want:  []string{},
		},
		{
			input: "testing/testassets/files/index.html",
			want:  []string{},
		},
		{
			input: "testing/testassets/archives/nested-testassets.zip.d/site.js",
			want:  []string{"testing/testassets/archives/nested-testassets.zip.d"},
		},
		{
			input: "testing/testassets/archives/nested-testassets.zip.d/single-testassets.zip.d/index.html",
			want:  []string{"testing/testassets/archives/nested-testassets.zip.d", "testing/testassets/archives/nested-testassets.zip.d/single-testassets.zip.d"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			got := getPotentialArchives(tc.input)
			if diff := cmp.Diff(got, tc.want); diff != "" {
				t.Errorf("got: %q, want: %q, diff: %q", got, tc.want, diff)
			}
		})
	}
}

func TestRemovePathComponent(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		mountPath string
		want      string
	}{
		{
			name:      "",
			mountPath: "",
			want:      "",
		},
		{
			name:      ".",
			mountPath: "abc/def",
			want:      "abc/def",
		},
		{
			name:      "a/b/c",
			mountPath: "a/b",
			want:      "c",
		},
		{
			name:      "a/b/c",
			mountPath: "a/b/",
			want:      "c",
		},
		{
			name:      "a/b/c/",
			mountPath: "a/b",
			want:      "c/",
		},
		{
			name:      "a/b/c/",
			mountPath: "a/b/",
			want:      "c/",
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s - %s", tc.name, tc.mountPath), func(t *testing.T) {
			t.Parallel()
			got := removePathComponent(tc.name, tc.mountPath)
			if got != tc.want {
				t.Errorf("got: %q, want: %q", got, tc.want)
			}
		})
	}
}

func TestNestFSStat(t *testing.T) {
	fsys, err := newNestFS("memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	nfs := fsys.(*nestFS)
	wf, err := fsys.Create("statme.txt")
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(wf, "hello")
	wf.Close()

	info, err := nfs.Stat("statme.txt")
	if err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}
	if info.Name() != "statme.txt" {
		t.Errorf("Stat().Name() = %q, want %q", info.Name(), "statme.txt")
	}
	if info.IsDir() {
		t.Error("Stat().IsDir() = true, want false")
	}
	if info.Size() != 5 {
		t.Errorf("Stat().Size() = %d, want 5", info.Size())
	}
}

func TestNestFS(t *testing.T) {
	testFileSystem(t, newNestFS, "memory://")
}
