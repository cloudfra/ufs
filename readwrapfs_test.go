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
	"io"
	"io/fs"
	"testing"
	"testing/fstest"
)

var testMapFS = fstest.MapFS{
	"hello.txt":       {Data: []byte("hello world")},
	"dir/nested.txt":  {Data: []byte("nested content")},
	"dir/another.txt": {Data: []byte("another file")},
}

func makeTestStdFS(t *testing.T) ReadFS {
	t.Helper()
	fsys := FromFS(testMapFS)
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	return fsys
}

func TestFromFSString(t *testing.T) {
	t.Parallel()
	fsys := FromFS(testMapFS)
	defer validateClose(t, fsys)()

	want := "readWrapFS(fstest.MapFS)"
	if got := fsys.(interface{ String() string }).String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestFromFSOpen(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	f, err := fsys.Open("hello.txt")
	if err != nil {
		t.Fatalf("Open() = %v, want nil", err)
	}
	defer validateClose(t, f)()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll() = %v, want nil", err)
	}
	if string(data) != "hello world" {
		t.Errorf("Open() content = %q, want %q", data, "hello world")
	}
}

func TestFromFSOpenDir(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	f, err := fsys.Open("dir")
	if err != nil {
		t.Fatalf("Open(dir) = %v, want nil", err)
	}
	defer validateClose(t, f)()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}
	if !info.IsDir() {
		t.Error("Open(dir).Stat().IsDir() = false, want true")
	}
}

func TestFromFSReadFile(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	data, err := fsys.ReadFile("hello.txt")
	if err != nil {
		t.Fatalf("ReadFile() = %v, want nil", err)
	}
	if string(data) != "hello world" {
		t.Errorf("ReadFile() = %q, want %q", data, "hello world")
	}
}

func TestFromFSReadDir(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	entries, err := fsys.ReadDir("dir")
	if err != nil {
		t.Fatalf("ReadDir() = %v, want nil", err)
	}
	if len(entries) != 2 {
		t.Errorf("ReadDir() returned %d entries, want 2", len(entries))
	}
}

func TestFromFSStat(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	info, err := fsys.Stat("hello.txt")
	if err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}
	if info.IsDir() {
		t.Error("Stat(file).IsDir() = true, want false")
	}
	if info.Size() != int64(len("hello world")) {
		t.Errorf("Stat(file).Size() = %d, want %d", info.Size(), len("hello world"))
	}
}

func TestFromFSLstat(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	info, err := fsys.Lstat("hello.txt")
	if err != nil {
		t.Fatalf("Lstat() = %v, want nil", err)
	}
	if info.IsDir() {
		t.Error("Lstat(file).IsDir() = true, want false")
	}
}

func TestFromFSReadLink(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	_, err := fsys.ReadLink("hello.txt")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("ReadLink() = %v, want fs.ErrInvalid", err)
	}
}

func TestFromFSCloseNoop(t *testing.T) {
	t.Parallel()
	fsys := FromFS(testMapFS)
	if err := fsys.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

type closableMapFS struct {
	fstest.MapFS
	closed bool
}

func (c *closableMapFS) Close() error {
	c.closed = true
	return nil
}

func TestFromFSCloseDelegates(t *testing.T) {
	t.Parallel()
	inner := &closableMapFS{MapFS: testMapFS}
	fsys := FromFS(inner)

	if err := fsys.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if !inner.closed {
		t.Error("Close() did not delegate to underlying io.Closer")
	}
}

func TestFromFSForEachFilename(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	var files []string
	err := ForEachFilename(fsys, ".", func(name string) error {
		files = append(files, name)
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachFilename() = %v, want nil", err)
	}
	if len(files) != 3 {
		t.Errorf("ForEachFilename() found %d files, want 3: %v", len(files), files)
	}
}

func TestFromFSTestFS(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	if err := fstest.TestFS(fsys, "hello.txt", "dir/nested.txt"); err != nil {
		t.Errorf("fstest.TestFS: %v", err)
	}
}

func TestFromFSInvalidPaths(t *testing.T) {
	t.Parallel()
	invalidPaths := []string{
		"/absolute/path",
		"../relative/path",
		"invalid/../path",
	}
	tests := []struct {
		name string
		op   func(fsys ReadFS, path string) error
	}{
		{"Open", func(fsys ReadFS, path string) error { _, err := fsys.Open(path); return err }},
		{"Stat", func(fsys ReadFS, path string) error { _, err := fsys.Stat(path); return err }},
		{"Lstat", func(fsys ReadFS, path string) error { _, err := fsys.Lstat(path); return err }},
		{"ReadFile", func(fsys ReadFS, path string) error { _, err := fsys.ReadFile(path); return err }},
		{"ReadDir", func(fsys ReadFS, path string) error { _, err := fsys.ReadDir(path); return err }},
		{"ReadLink", func(fsys ReadFS, path string) error { _, err := fsys.ReadLink(path); return err }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := makeTestStdFS(t)
			for _, p := range invalidPaths {
				t.Run(p, func(t *testing.T) {
					if err := tc.op(fsys, p); err == nil {
						t.Errorf("%s(%q) succeeded, want error", tc.name, p)
					}
				})
			}
		})
	}
}

func TestFromFSEmbedFS(t *testing.T) {
	t.Parallel()
	fsys := FromFS(embedTestFiles)
	defer validateClose(t, fsys)()

	data, err := fsys.ReadFile("testing/testassets/files/index.html")
	if err != nil {
		t.Fatalf("ReadFile() = %v, want nil", err)
	}
	if len(data) == 0 {
		t.Error("ReadFile() returned empty data, want non-empty")
	}
}
