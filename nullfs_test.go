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
	"strings"
	"testing"
)

func TestNullFSString(t *testing.T) {
	fsys := makeNullFS(nullFSPrefix)
	if got := fsys.String(); !strings.Contains(got, nullFSPrefix) {
		t.Errorf("String() should contain %q, got: %q", nullFSPrefix, got)
	}
}

func TestIsNullFSUri(t *testing.T) {
	testCases := []struct {
		name string
		want bool
	}{
		{
			name: "null:",
			want: true,
		},
		{
			name: "null://",
			want: true,
		},
		{
			name: "nullfs://",
			want: false,
		},
		{
			name: cwdPath,
			want: false,
		},
		{
			name: "mem://",
			want: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isNullFSUri(tc.name)
			if got != tc.want {
				t.Errorf("got: %t, want: %t", got, tc.want)
			}
		})
	}
}

func TestNewNullFS(t *testing.T) {
	fsys, err := newNullFS("null://")
	if err != nil {
		t.Fatal(err)
	}
	if fsys == nil {
		t.Fatal("fsys is nil")
	}

	f, err := fsys.Open("foo.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	nf, ok := f.(io.Writer)
	if !ok {
		t.Fatal("file does not implement Write")
	}

	data := []byte("hello world")
	n, err := nf.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(data) {
		t.Errorf("Write() = %d, want %d", n, len(data))
	}
}

func TestNullFSReadFile(t *testing.T) {
	nfs := mustNullFS(t)
	got, err := nfs.ReadFile("foo.txt")
	if err != nil {
		t.Fatalf("ReadFile() = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadFile() = %q, want empty", got)
	}
}

func TestNullFSReadLink(t *testing.T) {
	nfs := mustNullFS(t)
	_, err := nfs.ReadLink("link.txt")
	if err == nil {
		t.Fatal("ReadLink() = nil error, want error (nullFS has no symlinks)")
	}
}

func TestNullFSLstat(t *testing.T) {
	nfs := mustNullFS(t)

	t.Run("file", func(t *testing.T) {
		info, err := nfs.Lstat("foo.txt")
		if err != nil {
			t.Fatalf("Lstat() = %v, want nil", err)
		}
		if info.IsDir() {
			t.Error("IsDir() = true, want false")
		}
	})

	t.Run("dir", func(t *testing.T) {
		// "dir" is a valid fs.FS path; nullFS has no storage so Lstat returns file info.
		info, err := nfs.Lstat("dir")
		if err != nil {
			t.Fatalf("Lstat() = %v, want nil", err)
		}
		if info == nil {
			t.Fatal("Lstat() returned nil info")
		}
	})

	t.Run("cwd", func(t *testing.T) {
		// cwdPath (".") satisfies isDirName, so mode and size should reflect a directory.
		info, err := nfs.Lstat(cwdPath)
		if err != nil {
			t.Fatalf("Lstat(%q) = %v, want nil", cwdPath, err)
		}
		if !info.IsDir() {
			t.Errorf("Lstat(%q).IsDir() = false, want true", cwdPath)
		}
		if info.Mode()&fs.ModeDir == 0 {
			t.Errorf("Lstat(%q).Mode() missing ModeDir: %v", cwdPath, info.Mode())
		}
	})
}

func TestNullFSReadDir(t *testing.T) {
	nfs := mustNullFS(t)
	entries, err := nfs.ReadDir(cwdPath)
	if err != nil {
		t.Fatalf("ReadDir() = %v, want nil", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir() = %d entries, want 0", len(entries))
	}
	f, err := nfs.Open(cwdPath)
	if err != nil {
		t.Errorf("Open('.') returned error, %s", err)
	}
	rdf, ok := f.(fs.ReadDirFile)
	if ok {
		if readBytes, err := rdf.Read(nil); err == nil || readBytes != 0 {
			t.Errorf("fs.ReadDirFile.Read() want: (0, error) got: (%d, %s)", readBytes, err)
		}
		if dirs, err := rdf.ReadDir(-1); err != nil {
			t.Errorf("fs.ReadDirFile.ReadDir(-1) returned error, %s", err)
		} else if len(dirs) != 0 {
			t.Errorf("fs.ReadDirFile.ReadDir(-1) want: [], got: %v", dirs)
		}
		if stat, err := rdf.Stat(); err != nil {
			t.Errorf("fs.ReadDirFile.Stat() returned error, %s", err)
		} else if stat != nullDirStat {
			t.Errorf("fs.ReadDirFile.Stat() want: %v, got: %v", nullDirStat, stat)
		}
		if err := rdf.Close(); err != nil {
			t.Errorf("fs.ReadDirFile returned error, %s", err)
		}
	} else {
		t.Errorf("%+v is not a fs.ReadDirFile", rdf)
	}
}

func TestNullFSGlob(t *testing.T) {
	nfs := mustNullFS(t)
	matches, err := nfs.Glob("*.txt")
	if err != nil {
		t.Fatalf("Glob() = %v, want nil", err)
	}
	if len(matches) != 0 {
		t.Errorf("Glob() = %d matches, want 0", len(matches))
	}
}

func TestNullFileStatDir(t *testing.T) {
	f := newNullFile("subdir" + unixPathSeparator)
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("IsDir() = false, want true for trailing-slash name")
	}
	if info.Mode()&fs.ModeDir == 0 {
		t.Errorf("Mode() missing ModeDir: %v", info.Mode())
	}
}

func TestNullFileSeek(t *testing.T) {
	f := newNullFile("seek.txt")
	pos, err := f.Seek(100, io.SeekStart)
	if err != nil {
		t.Fatalf("Seek() = %v, want nil", err)
	}
	if pos != 0 {
		t.Errorf("Seek() = %d, want 0", pos)
	}
}

func TestNullFileReadAt(t *testing.T) {
	f := newNullFile("readat.txt")
	buf := make([]byte, 10)
	n, err := f.ReadAt(buf, 0)
	if err != io.EOF {
		t.Errorf("ReadAt() error = %v, want io.EOF", err)
	}
	if n != 0 {
		t.Errorf("ReadAt() = %d, want 0", n)
	}
}

func TestNullFileWriteString(t *testing.T) {
	f := newNullFile("write.txt")
	n, err := f.WriteString("hello world")
	if err != nil {
		t.Fatalf("WriteString() = %v, want nil", err)
	}
	if n != 11 {
		t.Errorf("WriteString() = %d, want 11", n)
	}
}

func TestNullFSCreate(t *testing.T) {
	fsys, err := newNullFS("null://")
	if err != nil {
		t.Fatal(err)
	}

	// Valid Create
	f, err := fsys.Create("created.txt")
	if err != nil {
		t.Fatalf("Create(\"created.txt\") failed: %v", err)
	}
	defer f.Close()

	if f == nil {
		t.Fatal("Created file is nil")
	}

	// Invalid Create
	invalidPaths := []string{
		"/absolute/path",
		"../relative/path",
		"invalid/../path",
	}

	for _, path := range invalidPaths {
		_, err := fsys.Create(path)
		if err == nil {
			t.Errorf("Create(%q) succeeded, want error", path)
		}
	}
}

func TestNullFileOperations(t *testing.T) {
	fsys, err := newNullFS("null://")
	if err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Open("testops.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Read
	b := make([]byte, 10)
	n, err := f.Read(b)
	if n != 0 {
		t.Errorf("Read() = %d, want 0", n)
	}
	if err != io.EOF {
		t.Errorf("Read() error = %v, want io.EOF", err)
	}

	// Close
	if err := f.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}

	// Stat
	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() failed: %v", err)
	}
	if info.Name() != "testops.txt" {
		t.Errorf("Name() = %q, want %q", info.Name(), "testops.txt")
	}
	if info.Size() != 0 {
		t.Errorf("Size() = %d, want 0", info.Size())
	}
	if info.IsDir() {
		t.Error("IsDir() = true, want false")
	}
	if info.Mode() != fs.ModePerm {
		t.Errorf("Mode() = %v, want %v", info.Mode(), fs.ModePerm)
	}
}

func TestNullFSStat(t *testing.T) {
	nfs := mustNullFS(t)

	info, err := nfs.Stat(cwdPath)
	if err != nil {
		t.Fatalf("Stat(%q) = %v, want nil", cwdPath, err)
	}
	if !info.IsDir() {
		t.Error("Stat('.').IsDir() = false, want true")
	}
}

func TestNullFSStatInvalid(t *testing.T) {
	nfs := mustNullFS(t)
	for _, path := range []string{"/absolute", "../parent", "invalid/../path"} {
		if _, err := nfs.Stat(path); err == nil {
			t.Errorf("Stat(%q) = nil error, want error", path)
		}
	}
}

func TestNullFSRemove(t *testing.T) {
	nfs := mustNullFS(t)
	if err := nfs.Remove("file.txt"); err != nil {
		t.Errorf("Remove() = %v, want nil", err)
	}
}

func TestNullFSRemoveAll(t *testing.T) {
	nfs := mustNullFS(t)
	if err := nfs.RemoveAll("dir"); err != nil {
		t.Errorf("RemoveAll() = %v, want nil", err)
	}
}

func mustNullFS(tb testing.TB) *nullFS {
	fsys := makeNullFS(nullFSPrefix)
	if fsys == nil {
		tb.Fatal("makeNullFS() returned nil")
	}
	return fsys
}

func TestNullFileStatBaseName(t *testing.T) {
	// FileInfo.Name() must return path.Base(name), not the full path.
	f := newNullFile("dir/subdir/file.txt")
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Name() != "file.txt" {
		t.Errorf("Stat().Name() = %q, want %q (base name only)", info.Name(), "file.txt")
	}
}

func TestNullDirStatName(t *testing.T) {
	// Root dir FileInfo.Name() must return "." per fs.FS convention.
	if nullDirStat.Name() != "." {
		t.Errorf("nullDirStat.Name() = %q, want %q", nullDirStat.Name(), ".")
	}
}

func TestNullReadDirFileReadReturnsPathError(t *testing.T) {
	// Reading from a directory must return *fs.PathError, not a plain error.
	f := &nullReadDirFile{}
	n, err := f.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("Read() on directory returned nil error, want *fs.PathError")
	}
	if n != 0 {
		t.Errorf("Read() = %d bytes, want 0", n)
	}
	var pe *fs.PathError
	if !errors.As(err, &pe) {
		t.Errorf("Read() error type = %T, want *fs.PathError; err = %v", err, err)
	}
}
