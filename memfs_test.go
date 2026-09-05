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
)

func TestIsMemFSUri(t *testing.T) {
	testCases := []struct {
		name string
		want bool
	}{
		{
			name: "memory:",
			want: true,
		},
		{
			name: "memory://",
			want: true,
		},
		{
			name: "mem:",
			want: false,
		},
		{
			name: "mem://",
			want: false,
		},
		{
			name: "memfs://",
			want: false,
		},
		{
			name: cwdPath,
			want: false,
		},
		{
			name: "null://",
			want: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isMemFSUri(tc.name)
			if got != tc.want {
				t.Errorf("got: %t, want: %t", got, tc.want)
			}
		})
	}
}

func TestNewMemFS(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	if fsys == nil {
		t.Fatal("fsys is nil")
	}
}

func TestMemFS(t *testing.T) {
	testFileSystem(t, newFSFuncWithoutContext(newMemFS), "memory://test")
}

func TestMemFSCreate(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}

	// Valid Create
	f, err := fsys.Create("created.txt")
	if err != nil {
		t.Fatalf("Create(\"created.txt\") failed: %v", err)
	}
	defer validateClose(t, f)()

	if f == nil {
		t.Fatal("Created file is nil")
	}
}

func TestMemFileOperations(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}

	// Create and write
	f, err := fsys.Create("testops.txt")
	if err != nil {
		t.Fatal(err)
	}

	// Write
	n, err := f.Write([]byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Errorf("Write() = %d, want 11", n)
	}

	// Seek to beginning
	pos, err := f.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatal(err)
	}
	if pos != 0 {
		t.Errorf("Seek(0, Start) = %d, want 0", pos)
	}

	// Read
	buf := make([]byte, 11)
	n, err = f.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Errorf("Read() = %d, want 11", n)
	}
	if string(buf) != "hello world" {
		t.Errorf("Read() = %q, want %q", string(buf), "hello world")
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
	if info.Size() != 11 {
		t.Errorf("Size() = %d, want 11", info.Size())
	}
	if info.IsDir() {
		t.Error("IsDir() = true, want false")
	}
	if info.Mode() != fs.ModePerm {
		t.Errorf("Mode() = %v, want %v", info.Mode(), fs.ModePerm)
	}

	// Read after EOF
	n, err = f.Read(buf)
	if n != 0 {
		t.Errorf("Read() after EOF = %d, want 0", n)
	}
	if err != io.EOF {
		t.Errorf("Read() error after EOF = %v, want io.EOF", err)
	}
}

func TestMemFileSeek(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}

	f, err := fsys.Create("seek.txt")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := f.WriteString("abcdefghijklm"); err != nil {
		t.Fatal(err)
	} else if n != len("abcdefghijklm") {
		t.Fatalf("WriteString() = %d, want %d", n, len("abcdefghijklm"))
	}

	// Seek to middle
	pos, err := f.Seek(5, io.SeekStart)
	if err != nil {
		t.Fatal(err)
	}
	if pos != 5 {
		t.Errorf("Seek(5, Start) = %d, want 5", pos)
	}

	buf := make([]byte, 3)
	n, err := f.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("Read() = %d, want 3", n)
	}
	if string(buf) != "fgh" {
		t.Errorf("Read() = %q, want %q", string(buf), "fgh")
	}

	// SeekCurrent
	pos, err = f.Seek(2, io.SeekCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if pos != 10 {
		t.Errorf("Seek(2, Current) = %d, want 10", pos)
	}

	// SeekEnd
	pos, err = f.Seek(0, io.SeekEnd)
	if err != nil {
		t.Fatal(err)
	}
	if pos != 13 {
		t.Errorf("Seek(0, End) = %d, want 13", pos)
	}

	// Invalid whence
	_, err = f.Seek(0, 99)
	if err == nil {
		t.Error("Seek(0, 99) succeeded, want error")
	}
}

func TestMemFileReadAt(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}

	f, err := fsys.Create("readat.txt")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := f.WriteString("hello world"); err != nil {
		t.Fatal(err)
	} else if n != len("hello world") {
		t.Fatalf("WriteString() = %d, want %d", n, len("hello world"))
	}

	// Read from middle (should not hit EOF)
	buf := make([]byte, 5)
	n, err := f.ReadAt(buf, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("ReadAt() = %d, want 5", n)
	}
	if string(buf) != "ello " {
		t.Errorf("ReadAt() = %q, want %q", string(buf), "ello ")
	}

	// Read to end (should return EOF)
	buf2 := make([]byte, 5)
	n, err = f.ReadAt(buf2, 6)
	if err != io.EOF {
		t.Errorf("ReadAt() at end err = %v, want io.EOF", err)
	}
	if n != 5 {
		t.Errorf("ReadAt() at end = %d, want 5", n)
	}
	if string(buf2) != "world" {
		t.Errorf("ReadAt() at end = %q, want %q", string(buf2), "world")
	}

	// Read beyond end
	buf3 := make([]byte, 5)
	n, err = f.ReadAt(buf3, 11)
	if err != io.EOF {
		t.Errorf("ReadAt() beyond end err = %v, want io.EOF", err)
	}
	if n != 0 {
		t.Errorf("ReadAt() beyond end = %d, want 0", n)
	}
}

func TestMemFSDirectory(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}

	// Create directory
	err = fsys.MkdirAll("subdir", fs.ModePerm)
	if err != nil {
		t.Fatal(err)
	}

	// Open directory
	dir, err := fsys.Open("subdir")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dir.Close(); err != nil {
			t.Errorf("failed to close directory: %v", err)
		}
	}()

	// Stat directory
	info, err := dir.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("IsDir() = false, want true")
	}

	// Regular files must not implement fs.ReadDirFile.
	f, err := fsys.Create("subdir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, f)()

	if _, ok := f.(fs.ReadDirFile); ok {
		t.Error("regular file implements fs.ReadDirFile, want it not to")
	}
}

func TestMemFSFilePersistence(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}

	// Create and write a file
	f, err := fsys.Create("persist.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("persistent data")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen and read
	f2, err := fsys.Open("persist.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f2.Close(); err != nil {
			t.Errorf("failed to close file: %v", err)
		}
	}()

	data, err := io.ReadAll(f2)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "persistent data" {
		t.Errorf("ReadAll() = %q, want %q", string(data), "persistent data")
	}
}

func TestMemFSReadFile(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}

	f, err := fsys.Create("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("hello world"); err != nil {
		t.Fatalf("failed to write to file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close file: %v", err)
	}

	rfs := fsys.(fs.ReadFileFS)

	t.Run("valid", func(t *testing.T) {
		got, err := rfs.ReadFile("hello.txt")
		if err != nil {
			t.Fatalf("ReadFile() = %v, want nil", err)
		}
		if string(got) != "hello world" {
			t.Errorf("ReadFile() = %q, want %q", got, "hello world")
		}
	})

	t.Run("not_found", func(t *testing.T) {
		if _, err := rfs.ReadFile("missing.txt"); err == nil {
			t.Error("ReadFile(missing) succeeded, want error")
		}
	})

	t.Run("invalid_path", func(t *testing.T) {
		if _, err := rfs.ReadFile("../escape.txt"); err == nil {
			t.Error("ReadFile(../escape.txt) succeeded, want error")
		}
	})
}

func TestMemFSReadLink(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("file.txt")
	if err != nil {
		t.Fatalf("Create(\"file.txt\") = %v, want nil", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close file: %v", err)
	}

	lfs := fsys.(fs.ReadLinkFS)

	t.Run("existing_file_not_a_symlink", func(t *testing.T) {
		if _, err := lfs.ReadLink("file.txt"); err == nil {
			t.Error("ReadLink on regular file succeeded, want error")
		}
	})

	t.Run("not_found", func(t *testing.T) {
		if _, err := lfs.ReadLink("missing.txt"); err == nil {
			t.Error("ReadLink(missing) succeeded, want error")
		}
	})

	t.Run("invalid_path", func(t *testing.T) {
		if _, err := lfs.ReadLink("../escape.txt"); err == nil {
			t.Error("ReadLink(../escape) succeeded, want error")
		}
	})
}

func TestMemFSLstat(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.MkdirAll("mydir", fs.ModePerm); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	f, err := fsys.Create("mydir/file.txt")
	if err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	if n, err := f.WriteString("data"); err != nil {
		t.Fatalf("failed to write file: %v", err)
	} else if n != len("data") {
		t.Fatalf("WriteString() = %d, want %d", n, len("data"))
	}
	if err := f.Close(); err != nil {
		t.Errorf("failed to close file: %v", err)
	}

	lfs := fsys.(fs.ReadLinkFS)

	t.Run("regular_file", func(t *testing.T) {
		info, err := lfs.Lstat("mydir/file.txt")
		if err != nil {
			t.Fatalf("Lstat() = %v, want nil", err)
		}
		if info.Name() != "file.txt" {
			t.Errorf("Name() = %q, want %q", info.Name(), "file.txt")
		}
		if info.Size() != 4 {
			t.Errorf("Size() = %d, want 4", info.Size())
		}
		if info.IsDir() {
			t.Error("IsDir() = true, want false")
		}
	})

	t.Run("directory", func(t *testing.T) {
		info, err := lfs.Lstat("mydir")
		if err != nil {
			t.Fatalf("Lstat() = %v, want nil", err)
		}
		if !info.IsDir() {
			t.Error("IsDir() = false, want true for directory")
		}
		if info.Mode()&fs.ModeDir == 0 {
			t.Errorf("Mode() missing ModeDir: %v", info.Mode())
		}
	})

	t.Run("root", func(t *testing.T) {
		info, err := lfs.Lstat(cwdPath)
		if err != nil {
			t.Fatalf("Lstat(.) = %v, want nil", err)
		}
		if !info.IsDir() {
			t.Error("IsDir() = false, want true for root")
		}
	})

	t.Run("not_found", func(t *testing.T) {
		if _, err := lfs.Lstat("missing.txt"); err == nil {
			t.Error("Lstat(missing) succeeded, want error")
		}
	})

	t.Run("invalid_path", func(t *testing.T) {
		if _, err := lfs.Lstat("../escape"); err == nil {
			t.Error("Lstat(../escape) succeeded, want error")
		}
	})
}

func TestMemFSReadDir(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.MkdirAll("docs", fs.ModePerm); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		f, err := fsys.Create("docs/" + name)
		if err != nil {
			t.Fatalf("Create(%q) = %v, want nil", "docs/"+name, err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("failed to close file: %v", err)
		}
	}

	dfs := fsys.(fs.ReadDirFS)

	t.Run("populated_dir", func(t *testing.T) {
		entries, err := dfs.ReadDir("docs")
		if err != nil {
			t.Fatalf("ReadDir() = %v, want nil", err)
		}
		if len(entries) != 2 {
			t.Errorf("ReadDir() = %d entries, want 2", len(entries))
		}
	})

	t.Run("root", func(t *testing.T) {
		entries, err := dfs.ReadDir(cwdPath)
		if err != nil {
			t.Fatalf("ReadDir(.) = %v, want nil", err)
		}
		if len(entries) == 0 {
			t.Error("ReadDir(.) returned 0 entries, want at least 1")
		}
	})

	t.Run("on_file", func(t *testing.T) {
		f, err := fsys.Create("plain.txt")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := dfs.ReadDir("plain.txt"); err == nil {
			t.Error("ReadDir on a file succeeded, want error")
		}
	})

	t.Run("not_found", func(t *testing.T) {
		if _, err := dfs.ReadDir("missing"); err == nil {
			t.Error("ReadDir(missing) succeeded, want error")
		}
	})

	t.Run("invalid_path", func(t *testing.T) {
		if _, err := dfs.ReadDir("../escape"); err == nil {
			t.Error("ReadDir(../escape) succeeded, want error")
		}
	})
}

func TestMemFSGlob(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.MkdirAll("src", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"src/foo.go", "src/bar.go", "src/README.md"} {
		f, err := fsys.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	gfs := fsys.(fs.GlobFS)

	t.Run("match_go_files", func(t *testing.T) {
		matches, err := gfs.Glob("src/*.go")
		if err != nil {
			t.Fatalf("Glob() = %v, want nil", err)
		}
		if len(matches) != 2 {
			t.Errorf("Glob(src/*.go) = %v, want 2 matches", matches)
		}
	})

	t.Run("match_all_in_src", func(t *testing.T) {
		matches, err := gfs.Glob("src/*")
		if err != nil {
			t.Fatalf("Glob() = %v, want nil", err)
		}
		if len(matches) != 3 {
			t.Errorf("Glob(src/*) = %v, want 3 matches", matches)
		}
	})

	t.Run("no_match", func(t *testing.T) {
		matches, err := gfs.Glob("src/*.xyz")
		if err != nil {
			t.Fatalf("Glob() = %v, want nil", err)
		}
		if len(matches) != 0 {
			t.Errorf("Glob(src/*.xyz) = %v, want 0 matches", matches)
		}
	})

	t.Run("invalid_pattern", func(t *testing.T) {
		if _, err := gfs.Glob("[invalid"); err == nil {
			t.Error("Glob([invalid) succeeded, want error")
		}
	})
}

func TestMemFSReaddirAll(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.MkdirAll("parent", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		f, err := fsys.Create("parent/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	dir, err := fsys.Open("parent")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dir.Close(); err != nil {
			t.Errorf("failed to close directory: %v", err)
		}
	}()

	rdf, ok := dir.(fs.ReadDirFile)
	if !ok {
		t.Fatal("Open(dir) did not return a fs.ReadDirFile")
	}
	entries, err := rdf.ReadDir(-1)
	if err != nil {
		t.Fatalf("ReadDir(-1) = %v, want nil", err)
	}
	if len(entries) != 2 {
		t.Errorf("ReadDir(-1) = %d entries, want 2", len(entries))
	}
}

func TestMemFSReaddirPaginated(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.MkdirAll("paged", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		f, err := fsys.Create("paged/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	dir, err := fsys.Open("paged")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dir.Close(); err != nil {
			t.Errorf("failed to close directory: %v", err)
		}
	}()

	rdf, ok := dir.(fs.ReadDirFile)
	if !ok {
		t.Fatal("Open(dir) did not return a fs.ReadDirFile")
	}
	for i := range 3 {
		e, err := rdf.ReadDir(1)
		if err != nil || len(e) != 1 {
			t.Fatalf("ReadDir(1) call %d: got %d entries, err=%v", i+1, len(e), err)
		}
	}
	// Exhausted: next call with n>0 must return io.EOF
	if _, err := rdf.ReadDir(1); err != io.EOF {
		t.Errorf("ReadDir(1) after exhaustion = %v, want io.EOF", err)
	}
}

func TestMemFileReadDirOnFile(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("regular.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, f)()

	// Regular files must not expose ReadDir; ReadDirFS.ReadDir should error.
	if _, ok := f.(fs.ReadDirFile); ok {
		t.Error("regular file implements fs.ReadDirFile, want it not to")
	}
	if _, err := fsys.ReadDir("regular.txt"); err == nil {
		t.Error("ReadDir on a regular file succeeded, want error")
	}
}

func TestMemFileSeekNegative(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("neg.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, f)()

	if _, err := f.Seek(-1, io.SeekStart); err == nil {
		t.Error("Seek(-1, SeekStart) succeeded, want error")
	}
}

func TestMemFSMkdirAllInvalid(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.MkdirAll("invalid/../path", fs.ModePerm); err == nil {
		t.Error("MkdirAll(invalid/../path) succeeded, want error")
	}
}

func TestMemFileDirRead(t *testing.T) {
	// Read() on a directory must return a non-EOF error.
	// Returning io.EOF is wrong: io.ReadAll would silently succeed with empty
	// content instead of propagating an error to the caller.
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.MkdirAll("emptydir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}

	for _, dirPath := range []string{".", "emptydir"} {
		t.Run(dirPath, func(t *testing.T) {
			f, err := fsys.Open(dirPath)
			if err != nil {
				t.Fatalf("Open(%q) = %v", dirPath, err)
			}
			defer validateClose(t, f)()

			n, err := f.Read(make([]byte, 1))
			if err == nil || err == io.EOF {
				t.Errorf("Read() on directory %q = (%d, %v), want a non-EOF error", dirPath, n, err)
			}
			if n != 0 {
				t.Errorf("Read() on directory %q returned %d bytes, want 0", dirPath, n)
			}
		})
	}
}

func TestMemFSClosedOperations(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}

	t.Run("Stat", func(t *testing.T) {
		if _, err := fsys.Stat("file.txt"); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("Stat() on closed memFS = %v, want fs.ErrClosed", err)
		}
	})
	t.Run("ReadFile", func(t *testing.T) {
		if _, err := fsys.ReadFile("file.txt"); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("ReadFile() on closed memFS = %v, want fs.ErrClosed", err)
		}
	})
	t.Run("ReadLink", func(t *testing.T) {
		if _, err := fsys.ReadLink("file.txt"); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("ReadLink() on closed memFS = %v, want fs.ErrClosed", err)
		}
	})
	t.Run("Lstat", func(t *testing.T) {
		if _, err := fsys.Lstat("file.txt"); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("Lstat() on closed memFS = %v, want fs.ErrClosed", err)
		}
	})
}

func TestMemFSRemove(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("failed to close memFS: %v", err)
		}
	}()

	f, err := fsys.Create("hello.txt")
	if err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("failed to close file: %v", err)
	}

	t.Run("file_exists", func(t *testing.T) {
		if err := fsys.Remove("hello.txt"); err != nil {
			t.Fatalf("Remove('hello.txt') = %v, want nil", err)
		}
		if _, err := fsys.Stat("hello.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("after Remove, Stat returned %v, want ErrNotExist", err)
		}
	})

	t.Run("not_exist", func(t *testing.T) {
		if err := fsys.Remove("ghost.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Remove(nonexistent) = %v, want ErrNotExist", err)
		}
	})

	t.Run("root_denied", func(t *testing.T) {
		if err := fsys.Remove(cwdPath); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("Remove('.') = %v, want ErrPermission", err)
		}
	})

	t.Run("empty_dir_ok", func(t *testing.T) {
		if err := fsys.MkdirAll("emptydir", fs.ModePerm); err != nil {
			t.Errorf("failed to create directory: %v", err)
		}
		if err := fsys.Remove("emptydir"); err != nil {
			t.Errorf("Remove(empty dir) = %v, want nil", err)
		}
	})

	t.Run("non_empty_dir_fails", func(t *testing.T) {
		if err := fsys.MkdirAll("nonempty", fs.ModePerm); err != nil {
			t.Errorf("failed to create directory: %v", err)
		}
		g, err := fsys.Create("nonempty/child.txt")
		if err != nil {
			t.Errorf("failed to create file in non-empty dir: %v", err)
		}
		if err := g.Close(); err != nil {
			t.Errorf("failed to close file: %v", err)
		}
		if err := fsys.Remove("nonempty"); err == nil {
			t.Error("Remove(non-empty dir) succeeded, want error")
		}
	})
}

func TestMemFSRemoveAll(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	if err := fsys.MkdirAll("a/b", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a/b/x.txt", "a/y.txt", "z.txt"} {
		g, err := fsys.Create(name)
		if err != nil {
			t.Fatalf("failed to create file %q: %v", name, err)
		}
		if err := g.Close(); err != nil {
			t.Errorf("failed to close file: %v", err)
		}
	}

	t.Run("subtree", func(t *testing.T) {
		if err := fsys.RemoveAll("a"); err != nil {
			t.Fatalf("RemoveAll('a') = %v, want nil", err)
		}
		if _, err := fsys.Stat("a"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("after RemoveAll('a'), Stat returned %v, want ErrNotExist", err)
		}
		if _, err := fsys.Stat("z.txt"); err != nil {
			t.Errorf("RemoveAll('a') unexpectedly removed z.txt: %v", err)
		}
	})

	t.Run("not_exist_is_noop", func(t *testing.T) {
		if err := fsys.RemoveAll("ghost"); err != nil {
			t.Errorf("RemoveAll(nonexistent) = %v, want nil", err)
		}
	})

	t.Run("root_clears_content", func(t *testing.T) {
		fsys2 := makeMemFS(memFSPrefix)
		defer func() {
			if err := fsys2.Close(); err != nil {
				t.Errorf("failed to close memFS: %v", err)
			}
		}()

		if err := fsys2.MkdirAll("dir", fs.ModePerm); err != nil {
			t.Fatalf("failed to create directory: %v", err)
		}
		h, err := fsys2.Create("file.txt")
		if err != nil {
			t.Fatalf("Create(\"file.txt\") = %v, want nil", err)
		}
		if err := h.Close(); err != nil {
			t.Errorf("failed to close file: %v", err)
		}

		if err := fsys2.RemoveAll(cwdPath); err != nil {
			t.Fatalf("RemoveAll('.') = %v, want nil", err)
		}
		entries, err := fsys2.ReadDir(cwdPath)
		if err != nil {
			t.Errorf("failed to read directory: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("after RemoveAll('.'), FS still has %d entries", len(entries))
		}
	})
}

func TestMemFSRemoveClosedFS(t *testing.T) {
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatalf("newMemFS() = %v, want nil", err)
	}
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}

	if err := fsys.Remove("file.txt"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Remove on closed memFS = %v, want fs.ErrClosed", err)
	}
	if err := fsys.RemoveAll("dir"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("RemoveAll on closed memFS = %v, want fs.ErrClosed", err)
	}
}

func TestMemFSStatOpName(t *testing.T) {
	// Stat() for an invalid path must report Op = "stat", not "lstat".
	fsys, err := newMemFS("memory://test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fsys.Stat("/absolute")
	if err == nil {
		t.Fatal("Stat(/absolute) succeeded, want error")
	}
	var pe *fs.PathError
	if !errors.As(err, &pe) {
		t.Fatalf("Stat() error type = %T, want *fs.PathError", err)
	}
	if pe.Op != "stat" {
		t.Errorf("Stat() PathError.Op = %q, want %q", pe.Op, "stat")
	}
}
