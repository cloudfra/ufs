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
	"io"
	"io/fs"
	"testing"
)

func TestNewNestFS(t *testing.T) {
	fsys, err := newNestFS("memfs://")
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

func TestNestFS(t *testing.T) {
	testFileSystem(t, newNestFS, "memfs://")
}

func TestNestFSClose(t *testing.T) {
	fsys, err := newNestFS("memfs://")
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestNestFSOpenInvalidPath(t *testing.T) {
	fsys, err := newNestFS("memfs://")
	if err != nil {
		t.Fatal(err)
	}

	invalidPaths := []string{
		"/absolute/path",
		"../relative/path",
		"invalid/../path",
	}
	for _, p := range invalidPaths {
		_, err := fsys.Open(p)
		if err == nil {
			t.Errorf("Open(%q) succeeded, want error", p)
		} else if _, ok := err.(*fs.PathError); !ok {
			t.Errorf("Open(%q) returned %T, want *fs.PathError", p, err)
		}
	}
}

func TestNestFSCreateInvalidPath(t *testing.T) {
	fsys, err := newNestFS("memfs://")
	if err != nil {
		t.Fatal(err)
	}

	invalidPaths := []string{
		"/absolute/path",
		"../relative/path",
		"invalid/../path",
	}
	for _, p := range invalidPaths {
		_, err := fsys.Create(p)
		if err == nil {
			t.Errorf("Create(%q) succeeded, want error", p)
		} else if _, ok := err.(*fs.PathError); !ok {
			t.Errorf("Create(%q) returned %T, want *fs.PathError", p, err)
		}
	}
}

func TestNestFSMkdirAll(t *testing.T) {
	fsys, err := newNestFS("memfs://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	nfs := fsys.(*nestFS)
	if err := nfs.MkdirAll("a/b/c", fs.ModePerm); err != nil {
		t.Fatalf("MkdirAll() = %v, want nil", err)
	}

	// Verify the directory is accessible.
	f, err := fsys.Open("a/b/c")
	if err != nil {
		t.Fatalf("Open(a/b/c) = %v, want nil", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}
	if !info.IsDir() {
		t.Error("IsDir() = false, want true")
	}
}

func TestNestFSReadDir(t *testing.T) {
	fsys, err := newNestFS("memfs://")
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
	fsys, err := newNestFS("memfs://")
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

func TestNestFSStat(t *testing.T) {
	fsys, err := newNestFS("memfs://")
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

func TestNestFSRead(t *testing.T) {
	fsys, err := newNestFS("memfs://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	// nestFS.Read delegates to the underlying FS only when it implements fs.File.
	// memFS does not implement fs.File, so Read returns 0 bytes and no error.
	nfs := fsys.(*nestFS)
	buf := make([]byte, 16)
	n, err := nfs.Read(buf)
	if err != nil {
		t.Errorf("Read() error = %v, want nil", err)
	}
	if n != 0 {
		t.Errorf("Read() = %d, want 0", n)
	}
}
