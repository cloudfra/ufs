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
	"path/filepath"
	"testing"
)

// testBoltFSURI returns a bolt: URI backed by a fresh temp file, unique per
// test (bolt databases are real files, unlike memory:).
func testBoltFSURI(t *testing.T) string {
	t.Helper()
	return boltFSPrefix + filepath.Join(t.TempDir(), "test.db")
}

func newTestBoltFS(t *testing.T) FS {
	t.Helper()
	fsys, err := makeBoltFS(testBoltFSURI(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("failed to close boltFS: %v", err)
		}
	})
	return fsys
}

func TestIsBoltFSUri(t *testing.T) {
	testCases := []struct {
		name string
		want bool
	}{
		{name: "bolt:", want: true},
		{name: "bolt:/tmp/x.db", want: true},
		{name: "memory:", want: false},
		{name: "bolts:/tmp/x.db", want: false},
		{name: cwdPath, want: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBoltFSUri(tc.name); got != tc.want {
				t.Errorf("got: %t, want: %t", got, tc.want)
			}
		})
	}
}

func TestNewBoltFS(t *testing.T) {
	fsys, err := newBoltFS(testBoltFSURI(t))
	if err != nil {
		t.Fatal(err)
	}
	if fsys == nil {
		t.Fatal("fsys is nil")
	}
	defer validateClose(t, fsys)()
}

func TestBoltFS(t *testing.T) {
	testFileSystem(t, newFSFuncWithoutContext(newBoltFS), testBoltFSURI(t))
}

func TestBoltFSCreate(t *testing.T) {
	fsys := newTestBoltFS(t)

	f, err := fsys.Create("created.txt")
	if err != nil {
		t.Fatalf("Create(\"created.txt\") failed: %v", err)
	}
	defer validateClose(t, f)()

	if f == nil {
		t.Fatal("Created file is nil")
	}
}

func TestBoltFileOperations(t *testing.T) {
	fsys := newTestBoltFS(t)

	f, err := fsys.Create("testops.txt")
	if err != nil {
		t.Fatal(err)
	}

	n, err := f.Write([]byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Errorf("Write() = %d, want 11", n)
	}

	pos, err := f.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatal(err)
	}
	if pos != 0 {
		t.Errorf("Seek(0, Start) = %d, want 0", pos)
	}

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

	if err := f.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}

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

	n, err = f.Read(buf)
	if n != 0 {
		t.Errorf("Read() after EOF = %d, want 0", n)
	}
	if err != io.EOF {
		t.Errorf("Read() error after EOF = %v, want io.EOF", err)
	}
}

func TestBoltFileSeek(t *testing.T) {
	fsys := newTestBoltFS(t)

	f, err := fsys.Create("seek.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, f)()
	if n, err := f.WriteString("abcdefghijklm"); err != nil {
		t.Fatal(err)
	} else if n != len("abcdefghijklm") {
		t.Fatalf("WriteString() = %d, want %d", n, len("abcdefghijklm"))
	}

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

	pos, err = f.Seek(2, io.SeekCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if pos != 10 {
		t.Errorf("Seek(2, Current) = %d, want 10", pos)
	}

	pos, err = f.Seek(0, io.SeekEnd)
	if err != nil {
		t.Fatal(err)
	}
	if pos != 13 {
		t.Errorf("Seek(0, End) = %d, want 13", pos)
	}

	if _, err := f.Seek(0, 99); err == nil {
		t.Error("Seek(0, 99) succeeded, want error")
	}
}

func TestBoltFileReadAt(t *testing.T) {
	fsys := newTestBoltFS(t)

	f, err := fsys.Create("readat.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, f)()
	if n, err := f.WriteString("hello world"); err != nil {
		t.Fatal(err)
	} else if n != len("hello world") {
		t.Fatalf("WriteString() = %d, want %d", n, len("hello world"))
	}

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

	buf3 := make([]byte, 5)
	n, err = f.ReadAt(buf3, 11)
	if err != io.EOF {
		t.Errorf("ReadAt() beyond end err = %v, want io.EOF", err)
	}
	if n != 0 {
		t.Errorf("ReadAt() beyond end = %d, want 0", n)
	}
}

func TestBoltFSDirectory(t *testing.T) {
	fsys := newTestBoltFS(t)

	if err := fsys.MkdirAll("subdir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}

	dir, err := fsys.Open("subdir")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dir.Close(); err != nil {
			t.Errorf("failed to close directory: %v", err)
		}
	}()

	info, err := dir.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("IsDir() = false, want true")
	}

	f, err := fsys.Create("subdir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, f)()

	if _, ok := f.(fs.ReadDirFile); ok {
		t.Error("regular file implements fs.ReadDirFile, want it not to")
	}
}

func TestBoltFSFilePersistence(t *testing.T) {
	fsys := newTestBoltFS(t)

	f, err := fsys.Create("persist.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("persistent data"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

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

// TestBoltFSWriteDeferredUntilClose verifies that content written to a
// boltFile is not visible to a fresh Open of the same path until the writer
// is Closed: writes are buffered in memory and only committed to the bolt
// database on Close.
func TestBoltFSWriteDeferredUntilClose(t *testing.T) {
	fsys := newTestBoltFS(t)

	f, err := fsys.Create("deferred.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not yet on disk"); err != nil {
		t.Fatal(err)
	}

	// Before Close, a separate Open should see the file with no content: the
	// write has not been committed to the bolt database yet.
	other, err := fsys.Open("deferred.txt")
	if err != nil {
		t.Fatalf("Open() before writer Close() failed: %v", err)
	}
	data, err := io.ReadAll(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("Open() before writer Close() saw content %q, want empty (write should be deferred)", data)
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	after, err := fsys.Open("deferred.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := after.Close(); err != nil {
			t.Errorf("failed to close file: %v", err)
		}
	}()
	data, err = io.ReadAll(after)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "not yet on disk" {
		t.Errorf("after writer Close(), ReadAll() = %q, want %q", data, "not yet on disk")
	}
}

func TestBoltFSReadFile(t *testing.T) {
	fsys := newTestBoltFS(t)

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

func TestBoltFSReadLink(t *testing.T) {
	fsys := newTestBoltFS(t)
	f, _ := fsys.Create("file.txt")
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

func TestBoltFSLstat(t *testing.T) {
	fsys := newTestBoltFS(t)
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

func TestBoltFSReadDir(t *testing.T) {
	fsys := newTestBoltFS(t)
	if err := fsys.MkdirAll("docs", fs.ModePerm); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		f, _ := fsys.Create("docs/" + name)
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

func TestBoltFSGlob(t *testing.T) {
	fsys := newTestBoltFS(t)
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

func TestBoltFSReaddirAll(t *testing.T) {
	fsys := newTestBoltFS(t)
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

func TestBoltFSReaddirPaginated(t *testing.T) {
	fsys := newTestBoltFS(t)
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
	if _, err := rdf.ReadDir(1); err != io.EOF {
		t.Errorf("ReadDir(1) after exhaustion = %v, want io.EOF", err)
	}
}

func TestBoltFileReadDirOnFile(t *testing.T) {
	fsys := newTestBoltFS(t)
	f, err := fsys.Create("regular.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, f)()

	if _, ok := f.(fs.ReadDirFile); ok {
		t.Error("regular file implements fs.ReadDirFile, want it not to")
	}
	if _, err := fsys.ReadDir("regular.txt"); err == nil {
		t.Error("ReadDir on a regular file succeeded, want error")
	}
}

func TestBoltFileSeekNegative(t *testing.T) {
	fsys := newTestBoltFS(t)
	f, err := fsys.Create("neg.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, f)()

	if _, err := f.Seek(-1, io.SeekStart); err == nil {
		t.Error("Seek(-1, SeekStart) succeeded, want error")
	}
}

func TestBoltFSMkdirAllInvalid(t *testing.T) {
	fsys := newTestBoltFS(t)
	if err := fsys.MkdirAll("invalid/../path", fs.ModePerm); err == nil {
		t.Error("MkdirAll(invalid/../path) succeeded, want error")
	}
}

func TestBoltFileDirRead(t *testing.T) {
	fsys := newTestBoltFS(t)
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

func TestBoltFSClosedOperations(t *testing.T) {
	fsys, err := newBoltFS(testBoltFSURI(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}

	t.Run("Stat", func(t *testing.T) {
		if _, err := fsys.Stat("file.txt"); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("Stat() on closed boltFS = %v, want fs.ErrClosed", err)
		}
	})
	t.Run("ReadFile", func(t *testing.T) {
		if _, err := fsys.ReadFile("file.txt"); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("ReadFile() on closed boltFS = %v, want fs.ErrClosed", err)
		}
	})
	t.Run("ReadLink", func(t *testing.T) {
		if _, err := fsys.ReadLink("file.txt"); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("ReadLink() on closed boltFS = %v, want fs.ErrClosed", err)
		}
	})
	t.Run("Lstat", func(t *testing.T) {
		if _, err := fsys.Lstat("file.txt"); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("Lstat() on closed boltFS = %v, want fs.ErrClosed", err)
		}
	})
}

func TestBoltFSRemove(t *testing.T) {
	fsys := newTestBoltFS(t)

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

func TestBoltFSRemoveAll(t *testing.T) {
	fsys := newTestBoltFS(t)

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
		fsys2 := newTestBoltFS(t)

		if err := fsys2.MkdirAll("dir", fs.ModePerm); err != nil {
			t.Fatalf("failed to create directory: %v", err)
		}
		h, _ := fsys2.Create("file.txt")
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

func TestBoltFSRemoveClosedFS(t *testing.T) {
	fsys, _ := newBoltFS(testBoltFSURI(t))
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}

	if err := fsys.Remove("file.txt"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Remove on closed boltFS = %v, want fs.ErrClosed", err)
	}
	if err := fsys.RemoveAll("dir"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("RemoveAll on closed boltFS = %v, want fs.ErrClosed", err)
	}
}

func TestBoltFSStatOpName(t *testing.T) {
	// Stat() for an invalid path must report Op = "stat", not "lstat".
	fsys := newTestBoltFS(t)
	_, err := fsys.Stat("/absolute")
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
