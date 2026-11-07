// Copyright 2026 Cloudfra
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
	"errors"
	"io"
	"io/fs"
	"os"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// openFilePerm is the permission an OpenFile test asks for. A file system may
// ignore it, so no test here reads it back.
const openFilePerm = fs.FileMode(0o644)

// openFileAccessFlags lists the three access modes an open flag carries.
var openFileAccessFlags = []struct {
	name string
	flag int
}{
	{"readOnly", os.O_RDONLY},
	{"writeOnly", os.O_WRONLY},
	{"readWrite", os.O_RDWR},
}

// OpenFile runs the OpenFile conformance suite against file systems returned by
// createFSFunc. The suite describes a file system that implements
// [types.OpenFileFS] itself and carries its own flags: the keep-existing, append
// and bare-write groups need a handle that edits content in place, which the
// create-based emulation refuses on purpose, and the directory group needs
// [types.MkdirAllFS] to create the directory it opens.
func OpenFile(t *testing.T, args Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	openFileMissing(t, args, createFSFunc)
	openFileCreate(t, args, createFSFunc)
	openFileExclusive(t, args, createFSFunc)
	openFileTruncate(t, args, createFSFunc)
	openFileKeepExisting(t, args, createFSFunc)
	openFileAppend(t, args, createFSFunc)
	openFileAccess(t, args, createFSFunc)
	openFileDirectory(t, args, createFSFunc)
	openFileInvalidPaths(t, args, createFSFunc)
	openFileReopen(t, args, createFSFunc)
}

func openFileMissing(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	t.Run("OpenFileMissing", func(t *testing.T) {
		t.Parallel()
		for _, access := range openFileAccessFlags {
			t.Run(access.name, func(t *testing.T) {
				t.Parallel()
				fsys := createFSFunc(t)
				t.Cleanup(closeOpenFileFS(t, fsys))

				for _, flag := range []int{access.flag, access.flag | os.O_EXCL} {
					f, err := fsys.OpenFile("missing.txt", flag, openFilePerm)
					if !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("OpenFile(missing.txt, %s, flag %d) = %v, want %v", access.name, flag, err, fs.ErrNotExist)
					}
					if f != nil {
						if err := f.Close(); err != nil {
							t.Errorf("Close after a refused open = %v, want nil", err)
						}
					}
				}
				if _, err := fs.Stat(fsys, "missing.txt"); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("Stat after a refused open = %v, want %v", err, fs.ErrNotExist)
				}
			})
		}
	})
}

func openFileCreate(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	t.Run("OpenFileCreate", func(t *testing.T) {
		t.Parallel()
		for _, access := range openFileAccessFlags {
			t.Run(access.name, func(t *testing.T) {
				t.Parallel()
				fsys := createFSFunc(t)
				t.Cleanup(closeOpenFileFS(t, fsys))

				f, err := fsys.OpenFile("created.txt", access.flag|os.O_CREATE, openFilePerm)
				if err != nil {
					t.Fatalf("OpenFile(created.txt, %s|O_CREATE) = %v, want nil", access.name, err)
				}
				if access.flag == os.O_RDONLY {
					// The handle is not written here because whether a read-only
					// handle accepts writes is platform specific: localFS refuses
					// it on Linux and allows it on Windows. openFileAccess covers
					// the refusals that are portable.
					if err := f.Close(); err != nil {
						t.Fatalf("Close = %v, want nil", err)
					}
					info, err := fs.Stat(fsys, "created.txt")
					if err != nil {
						t.Fatalf("Stat(created.txt) = %v, want nil", err)
					}
					if info.Size() != 0 {
						t.Errorf("an unwritten O_RDONLY create left %d bytes, want none", info.Size())
					}
					return
				}
				if _, err := f.WriteString("hello"); err != nil {
					t.Fatalf("WriteString = %v, want nil", err)
				}
				if err := f.Close(); err != nil {
					t.Fatalf("Close = %v, want nil", err)
				}
				if got := readOpenFile(t, fsys, "created.txt"); got != "hello" {
					t.Errorf("created file = %q, want %q", got, "hello")
				}
			})
		}
	})
}

func openFileExclusive(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	t.Run("OpenFileExclusive", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(closeOpenFileFS(t, fsys))

		writeOpenFile(t, fsys, "taken.txt", "there")
		for _, access := range openFileAccessFlags {
			f, err := fsys.OpenFile("taken.txt", access.flag|os.O_CREATE|os.O_EXCL, openFilePerm)
			if !errors.Is(err, fs.ErrExist) {
				t.Errorf("OpenFile(taken.txt, %s|O_CREATE|O_EXCL) = %v, want %v", access.name, err, fs.ErrExist)
			}
			if f != nil {
				if err := f.Close(); err != nil {
					t.Errorf("Close after a refused open = %v, want nil", err)
				}
			}
		}
		if got := readOpenFile(t, fsys, "taken.txt"); got != "there" {
			t.Errorf("a refused exclusive open left %q, want %q", got, "there")
		}

		f, err := fsys.OpenFile("fresh.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL, openFilePerm)
		if err != nil {
			t.Fatalf("OpenFile(fresh.txt, O_CREATE|O_EXCL) = %v, want nil", err)
		}
		if _, err := f.WriteString("new"); err != nil {
			t.Fatalf("WriteString = %v, want nil", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
		if got := readOpenFile(t, fsys, "fresh.txt"); got != "new" {
			t.Errorf("exclusive create = %q, want %q", got, "new")
		}
	})
}

func openFileTruncate(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	t.Run("OpenFileTruncate", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(closeOpenFileFS(t, fsys))

		writeOpenFile(t, fsys, "trunc.txt", "long content")
		f, err := fsys.OpenFile("trunc.txt", os.O_WRONLY|os.O_TRUNC, openFilePerm)
		if err != nil {
			t.Fatalf("OpenFile(trunc.txt, O_TRUNC) = %v, want nil", err)
		}
		if _, err := f.WriteString("short"); err != nil {
			t.Fatalf("WriteString = %v, want nil", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
		if got := readOpenFile(t, fsys, "trunc.txt"); got != "short" {
			t.Errorf("truncated file = %q, want %q", got, "short")
		}
		// O_TRUNC on a read-only open is deliberately untested: POSIX leaves it
		// unspecified and localFS disagrees across platforms, refusing the open
		// on Windows with "Access is denied" and truncating on Linux.
	})
}

// openFileKeepExisting wants a write that keeps the content already in the
// file, which only a file system carrying its own flags can do.
func openFileKeepExisting(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	t.Run("OpenFileKeepExisting", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(closeOpenFileFS(t, fsys))

		writeOpenFile(t, fsys, "keep.txt", "abcdef")
		f, err := fsys.OpenFile("keep.txt", os.O_WRONLY, openFilePerm)
		if err != nil {
			t.Fatalf("OpenFile(keep.txt) = %v, want nil", err)
		}
		if _, err := f.WriteString("XY"); err != nil {
			t.Fatalf("WriteString = %v, want nil", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
		if got := readOpenFile(t, fsys, "keep.txt"); got != "XYcdef" {
			t.Errorf("a write at the start of a file left %q, want %q", got, "XYcdef")
		}
	})
}

// openFileAppend pins an append over an existing name: the handle starts from
// the content already there, and every write lands at the end of the file.
func openFileAppend(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	t.Run("OpenFileAppend", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(closeOpenFileFS(t, fsys))

		writeOpenFile(t, fsys, "app.txt", "first")
		f, err := fsys.OpenFile("app.txt", os.O_WRONLY|os.O_APPEND, openFilePerm)
		if err != nil {
			t.Fatalf("OpenFile(app.txt, O_APPEND) = %v, want nil", err)
		}
		if _, err := f.WriteString(" second"); err != nil {
			t.Fatalf("WriteString = %v, want nil", err)
		}
		// O_APPEND puts every write at the end, wherever the handle is seeked.
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			t.Fatalf("Seek = %v, want nil", err)
		}
		if _, err := f.WriteString(" third"); err != nil {
			t.Fatalf("WriteString after Seek = %v, want nil", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
		if got := readOpenFile(t, fsys, "app.txt"); got != "first second third" {
			t.Errorf("appended file = %q, want %q", got, "first second third")
		}
	})
}

// openFileAccess wants a bare write over existing content to reach only that
// content's tail, so it needs a file system carrying its own flags too.
func openFileAccess(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	t.Run("OpenFileAccess", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(closeOpenFileFS(t, fsys))

		writeOpenFile(t, fsys, "access.txt", "abcdef")

		f, err := fsys.OpenFile("access.txt", os.O_RDONLY, openFilePerm)
		if err != nil {
			t.Fatalf("OpenFile(access.txt, O_RDONLY) = %v, want nil", err)
		}
		if _, err := f.WriteString("x"); err == nil {
			t.Error("WriteString on an O_RDONLY handle = nil, want an error")
		}
		if _, err := f.Write([]byte("x")); err == nil {
			t.Error("Write on an O_RDONLY handle = nil, want an error")
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}

		f, err = fsys.OpenFile("access.txt", os.O_WRONLY, openFilePerm)
		if err != nil {
			t.Fatalf("OpenFile(access.txt, O_WRONLY) = %v, want nil", err)
		}
		if _, err := f.Read(make([]byte, 4)); err == nil {
			t.Error("Read on an O_WRONLY handle = nil, want an error")
		}
		if _, err := f.ReadAt(make([]byte, 4), 1); err == nil {
			t.Error("ReadAt on an O_WRONLY handle = nil, want an error")
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
		if got := readOpenFile(t, fsys, "access.txt"); got != "abcdef" {
			t.Errorf("a refused write left %q, want %q", got, "abcdef")
		}

		f, err = fsys.OpenFile("access.txt", os.O_RDWR, openFilePerm)
		if err != nil {
			t.Fatalf("OpenFile(access.txt, O_RDWR) = %v, want nil", err)
		}
		if _, err := f.Seek(2, io.SeekStart); err != nil {
			t.Fatalf("Seek = %v, want nil", err)
		}
		if _, err := f.WriteString("XY"); err != nil {
			t.Fatalf("WriteString = %v, want nil", err)
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			t.Fatalf("Seek = %v, want nil", err)
		}
		buf := make([]byte, 6)
		if _, err := io.ReadFull(f, buf); err != nil {
			t.Fatalf("Read on an O_RDWR handle = %v, want nil", err)
		}
		if string(buf) != "abXYef" {
			t.Errorf("an O_RDWR handle read %q, want %q", buf, "abXYef")
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
	})
}

func openFileDirectory(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	t.Run("OpenFileDirectory", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(closeOpenFileFS(t, fsys))

		mkr, ok := fsys.(types.MkdirAllFS)
		if !ok {
			t.Fatalf("%T does not implement types.MkdirAllFS, which this suite needs to create the directory it opens", fsys)
		}
		if err := mkr.MkdirAll("dir", 0o755); err != nil {
			t.Fatalf("MkdirAll(dir) = %v, want nil", err)
		}
		if _, err := fsys.OpenFile("dir/inner.txt", os.O_WRONLY|os.O_CREATE, openFilePerm); err != nil {
			t.Fatalf("OpenFile(dir/inner.txt) = %v, want nil", err)
		}

		f, err := fsys.OpenFile("dir", os.O_RDONLY, openFilePerm)
		if err != nil {
			t.Fatalf("OpenFile(dir, O_RDONLY) = %v, want nil", err)
		}
		info, err := f.Stat()
		if err != nil {
			t.Fatalf("Stat = %v, want nil", err)
		}
		if !info.IsDir() {
			t.Errorf("the handle for dir is not a directory: %v", info.Mode())
		}
		rdf, ok := f.(fs.ReadDirFile)
		if !ok {
			t.Fatalf("OpenFile(dir) = %T, want an fs.ReadDirFile", f)
		}
		entries, err := rdf.ReadDir(-1)
		if err != nil {
			t.Fatalf("ReadDir = %v, want nil", err)
		}
		if len(entries) != 1 || entries[0].Name() != "inner.txt" {
			t.Errorf("ReadDir(dir) = %v, want [inner.txt]", entries)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}

		// Opening a directory to write has no standard error, so the refusal is
		// checked as a path error naming the entry. Which sentinel it carries is
		// left to issue #548: localFS reports the bare EISDIR from openat, which
		// matches no io/fs sentinel.
		for _, flag := range []int{os.O_WRONLY, os.O_WRONLY | os.O_TRUNC, os.O_RDWR | os.O_TRUNC} {
			g, err := fsys.OpenFile("dir", flag, openFilePerm)
			if err == nil {
				t.Errorf("OpenFile(dir, flag %d) = %v, want an error", flag, g)
				if g != nil {
					if err := g.Close(); err != nil {
						t.Errorf("Close on the directory handle = %v, want nil", err)
					}
				}
				continue
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("OpenFile(dir, flag %d) = %T, want an *fs.PathError", flag, err)
			} else if pathErr.Path != "dir" {
				t.Errorf("OpenFile(dir, flag %d) named %q, want dir", flag, pathErr.Path)
			}
		}
	})
}

func openFileInvalidPaths(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	t.Run("OpenFileInvalidPaths", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(closeOpenFileFS(t, fsys))

		for _, name := range append(invalidPaths, "", "dir/", "dir//inner.txt", "./dir") {
			for _, flag := range []int{os.O_RDONLY, os.O_WRONLY | os.O_CREATE} {
				f, err := fsys.OpenFile(name, flag, openFilePerm)
				if !errors.Is(err, fs.ErrInvalid) {
					t.Errorf("OpenFile(%q, flag %d) = %v, want %v", name, flag, err, fs.ErrInvalid)
				}
				var pathErr *fs.PathError
				if !errors.As(err, &pathErr) {
					t.Errorf("OpenFile(%q, flag %d) = %T, want an *fs.PathError", name, flag, err)
				} else if pathErr.Op != "open" {
					t.Errorf("OpenFile(%q, flag %d) Op = %q, want open", name, flag, pathErr.Op)
				}
				if f != nil {
					if err := f.Close(); err != nil {
						t.Errorf("Close after a refused open of %q = %v, want nil", name, err)
					}
				}
			}
		}
	})
}

func openFileReopen(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	t.Run("OpenFileReopen", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(closeOpenFileFS(t, fsys))

		for _, name := range []string{"reopened.txt", "reopened.txt"} {
			f, err := fsys.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, openFilePerm)
			if err != nil {
				t.Fatalf("OpenFile(%s) = %v, want nil", name, err)
			}
			if _, err := f.WriteString("content"); err != nil {
				t.Fatalf("WriteString = %v, want nil", err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("Close = %v, want nil", err)
			}
			if got := readOpenFile(t, fsys, name); got != "content" {
				t.Errorf("reopened file = %q, want %q", got, "content")
			}
		}
	})
}

// writeOpenFile writes content to name through OpenFile and closes the handle,
// stopping the test on any failure.
func writeOpenFile(tb testing.TB, fsys types.OpenFileFS, name, content string) {
	tb.Helper()
	f, err := fsys.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, openFilePerm)
	if err != nil {
		tb.Fatalf("OpenFile(%q) = %v, want nil", name, err)
	}
	if _, err := f.WriteString(content); err != nil {
		tb.Fatalf("WriteString(%q) = %v, want nil", name, err)
	}
	if err := f.Close(); err != nil {
		tb.Fatalf("Close(%q) = %v, want nil", name, err)
	}
}

// readOpenFile returns the content a file system holds for name, read the way
// every file system allows it.
func readOpenFile(tb testing.TB, fsys types.OpenFileFS, name string) string {
	tb.Helper()
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		tb.Fatalf("ReadFile(%q) = %v, want nil", name, err)
	}
	return string(data)
}

// closeOpenFileFS closes fsys when it can be closed, checking that it closes
// cleanly and exactly once.
func closeOpenFileFS(tb testing.TB, fsys types.OpenFileFS) func() {
	closer, ok := fsys.(io.Closer)
	if !ok {
		return func() {}
	}
	return ufsTesting.ValidateClose(tb, closer)
}
