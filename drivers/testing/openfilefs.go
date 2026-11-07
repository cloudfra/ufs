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
	openFileReadAt(t, args, createFSFunc)
	openFileSeek(t, args, createFSFunc)
	openFileClosed(t, args, createFSFunc)
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

		// O_TRUNC also empties what the handle that truncated the file reads
		// back, not only what a later open sees.
		writeOpenFile(t, fsys, "truncrw.txt", "long content")
		g, err := fsys.OpenFile("truncrw.txt", os.O_RDWR|os.O_TRUNC, openFilePerm)
		if err != nil {
			t.Fatalf("OpenFile(truncrw.txt, O_RDWR|O_TRUNC) = %v, want nil", err)
		}
		if n, err := g.Read(make([]byte, 4)); n != 0 || !errors.Is(err, io.EOF) {
			t.Errorf("Read on a freshly truncated O_RDWR handle = %d, %v, want 0, %v", n, err, io.EOF)
		}
		if _, err := g.WriteString("short"); err != nil {
			t.Fatalf("WriteString = %v, want nil", err)
		}
		if err := g.Close(); err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
		if got := readOpenFile(t, fsys, "truncrw.txt"); got != "short" {
			t.Errorf("truncated file read through its own handle = %q, want %q", got, "short")
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

		// O_APPEND with O_CREATE starts from an empty file when the name is new.
		g, err := fsys.OpenFile("new.txt", os.O_WRONLY|os.O_CREATE|os.O_APPEND, openFilePerm)
		if err != nil {
			t.Fatalf("OpenFile(new.txt, O_CREATE|O_APPEND) = %v, want nil", err)
		}
		if _, err := g.WriteString("first"); err != nil {
			t.Fatalf("WriteString = %v, want nil", err)
		}
		if err := g.Close(); err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
		if got := readOpenFile(t, fsys, "new.txt"); got != "first" {
			t.Errorf("appended to a created file = %q, want %q", got, "first")
		}

		// O_TRUNC empties the name when the file is opened, so an append flag
		// carried along with it starts from nothing.
		writeOpenFile(t, fsys, "truncapp.txt", "before")
		h, err := fsys.OpenFile("truncapp.txt", os.O_WRONLY|os.O_TRUNC|os.O_APPEND, openFilePerm)
		if err != nil {
			t.Fatalf("OpenFile(truncapp.txt, O_TRUNC|O_APPEND) = %v, want nil", err)
		}
		if _, err := h.WriteString("after"); err != nil {
			t.Fatalf("WriteString = %v, want nil", err)
		}
		if err := h.Close(); err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
		if got := readOpenFile(t, fsys, "truncapp.txt"); got != "after" {
			t.Errorf("appending to a truncated file = %q, want %q", got, "after")
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

// openFileReadAt pins the offset-addressed read: it must return the bytes held
// at the offset it is handed, and leave the handle's own position alone.
func openFileReadAt(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	t.Run("OpenFileReadAt", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(closeOpenFileFS(t, fsys))

		writeOpenFile(t, fsys, "readat.txt", "0123456789")

		for _, tc := range []struct {
			name    string
			offset  int64
			size    int
			want    string
			wantEOF bool
		}{
			{name: "start", offset: 0, size: 4, want: "0123"},
			{name: "middle", offset: 4, size: 4, want: "4567"},
			{name: "across_the_end", offset: 8, size: 4, want: "89", wantEOF: true},
			{name: "at_the_end", offset: 10, size: 4, want: "", wantEOF: true},
			{name: "past_the_end", offset: 14, size: 4, want: "", wantEOF: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				f, err := fsys.OpenFile("readat.txt", os.O_RDONLY, openFilePerm)
				if err != nil {
					t.Fatalf("OpenFile(readat.txt, O_RDONLY) = %v, want nil", err)
				}
				buf := make([]byte, tc.size)
				n, err := f.ReadAt(buf, tc.offset)
				if n != len(tc.want) {
					t.Errorf("ReadAt at %d read %d bytes, want %d", tc.offset, n, len(tc.want))
				}
				if got := string(buf[:n]); got != tc.want {
					t.Errorf("ReadAt at %d = %q, want %q", tc.offset, got, tc.want)
				}
				if tc.wantEOF {
					if !errors.Is(err, io.EOF) {
						t.Errorf("ReadAt at %d = %v, want %v", tc.offset, err, io.EOF)
					}
				} else if err != nil {
					t.Errorf("ReadAt at %d = %v, want nil", tc.offset, err)
				}
				if err := f.Close(); err != nil {
					t.Errorf("Close = %v, want nil", err)
				}
			})
		}

		t.Run("negative_offset", func(t *testing.T) {
			t.Parallel()
			f, err := fsys.OpenFile("readat.txt", os.O_RDONLY, openFilePerm)
			if err != nil {
				t.Fatalf("OpenFile(readat.txt, O_RDONLY) = %v, want nil", err)
			}
			// Only the refusal is pinned. localFS reports a bare message from the
			// os package while an in-memory file system reports fs.ErrInvalid.
			buf := make([]byte, 4)
			if n, err := f.ReadAt(buf, -1); n != 0 || err == nil {
				t.Errorf("ReadAt at -1 = %d, %v, want 0 and an error", n, err)
			}
			if err := f.Close(); err != nil {
				t.Errorf("Close = %v, want nil", err)
			}
		})

		t.Run("position_unchanged", func(t *testing.T) {
			t.Parallel()
			f, err := fsys.OpenFile("readat.txt", os.O_RDONLY, openFilePerm)
			if err != nil {
				t.Fatalf("OpenFile(readat.txt, O_RDONLY) = %v, want nil", err)
			}
			buf := make([]byte, 3)
			if n, err := f.Read(buf); n != 3 || err != nil || string(buf) != "012" {
				t.Fatalf("Read = %d, %v, %q, want 3, nil, %q", n, err, string(buf), "012")
			}
			// The ReadAt is deliberately aimed away from the position the handle
			// has reached, so a ReadAt that moved it would show up next.
			if n, err := f.ReadAt(buf, 6); n != 3 || err != nil || string(buf) != "678" {
				t.Fatalf("ReadAt at 6 = %d, %v, %q, want 3, nil, %q", n, err, string(buf), "678")
			}
			if n, err := f.Read(buf); n != 3 || err != nil || string(buf) != "345" {
				t.Errorf("Read after a ReadAt = %d, %v, %q, want 3, nil, %q", n, err, string(buf), "345")
			}
			if err := f.Close(); err != nil {
				t.Errorf("Close = %v, want nil", err)
			}
		})
	})
}

// openFileSeek pins where a handle positions itself for the next read or write,
// including the hole that a write past the end of a file leaves behind.
func openFileSeek(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	t.Run("OpenFileSeek", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(closeOpenFileFS(t, fsys))

		writeOpenFile(t, fsys, "seek.txt", "abcdef")

		for _, tc := range []struct {
			name     string
			from     int64
			offset   int64
			whence   int
			wantPos  int64
			wantRead string
			wantEOF  bool
		}{
			{name: "start", from: -1, offset: 2, whence: io.SeekStart, wantPos: 2, wantRead: "cd"},
			{name: "current", from: 1, offset: 2, whence: io.SeekCurrent, wantPos: 3, wantRead: "de"},
			{name: "end", from: -1, offset: -2, whence: io.SeekEnd, wantPos: 4, wantRead: "ef"},
			{name: "past_the_end", from: -1, offset: 20, whence: io.SeekStart, wantPos: 20, wantEOF: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				f, err := fsys.OpenFile("seek.txt", os.O_RDONLY, openFilePerm)
				if err != nil {
					t.Fatalf("OpenFile(seek.txt, O_RDONLY) = %v, want nil", err)
				}
				if tc.from >= 0 {
					if pos, err := f.Seek(tc.from, io.SeekStart); pos != tc.from || err != nil {
						t.Fatalf("Seek(%d, io.SeekStart) = %d, %v, want %d, nil", tc.from, pos, err, tc.from)
					}
				}
				pos, err := f.Seek(tc.offset, tc.whence)
				if pos != tc.wantPos || err != nil {
					t.Fatalf("Seek(%d, whence %d) = %d, %v, want %d, nil", tc.offset, tc.whence, pos, err, tc.wantPos)
				}
				buf := make([]byte, 2)
				n, err := f.Read(buf)
				if n != len(tc.wantRead) {
					t.Fatalf("Read after Seek(%d, whence %d) read %d bytes, want %d", tc.offset, tc.whence, n, len(tc.wantRead))
				}
				if tc.wantEOF {
					if !errors.Is(err, io.EOF) {
						t.Fatalf("Read after Seek(%d, whence %d) = %v, want %v", tc.offset, tc.whence, err, io.EOF)
					}
				} else if err != nil {
					t.Fatalf("Read after Seek(%d, whence %d) = %v, want nil", tc.offset, tc.whence, err)
				}
				if got := string(buf[:n]); got != tc.wantRead {
					t.Errorf("Read after Seek(%d, whence %d) = %q, want %q", tc.offset, tc.whence, got, tc.wantRead)
				}
				if err := f.Close(); err != nil {
					t.Errorf("Close = %v, want nil", err)
				}
			})
		}

		t.Run("hole_before_a_write", func(t *testing.T) {
			t.Parallel()
			f, err := fsys.OpenFile("hole.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC, openFilePerm)
			if err != nil {
				t.Fatalf("OpenFile(hole.txt) = %v, want nil", err)
			}
			if _, err := f.WriteString("ab"); err != nil {
				t.Fatalf("WriteString = %v, want nil", err)
			}
			if pos, err := f.Seek(4, io.SeekStart); pos != 4 || err != nil {
				t.Fatalf("Seek(4, io.SeekStart) = %d, %v, want 4, nil", pos, err)
			}
			if _, err := f.WriteString("X"); err != nil {
				t.Fatalf("WriteString past the end = %v, want nil", err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("Close = %v, want nil", err)
			}
			if got := readOpenFile(t, fsys, "hole.txt"); got != "ab\x00\x00X" {
				t.Errorf("a write past the end left %q, want %q", got, "ab\\0\\0X")
			}
		})

		t.Run("refused_seeks", func(t *testing.T) {
			t.Parallel()
			f, err := fsys.OpenFile("seek.txt", os.O_RDONLY, openFilePerm)
			if err != nil {
				t.Fatalf("OpenFile(seek.txt, O_RDONLY) = %v, want nil", err)
			}
			// Only the refusal and the untouched position are pinned: localFS
			// carries the bare EINVAL from seek(2), which matches no io/fs
			// sentinel, and issue #548 decides what this refusal wraps. An
			// unknown whence is deliberately not pinned here, because Windows
			// keeps the move method at FILE_BEGIN for a whence it does not
			// recognize, so the same call succeeds there.
			pos, err := f.Seek(-1, io.SeekStart)
			if pos != 0 || err == nil {
				t.Errorf("Seek(-1, io.SeekStart) = %d, %v, want 0 and an error", pos, err)
			}
			if err := f.Close(); err != nil {
				t.Errorf("Close = %v, want nil", err)
			}
		})
	})
}

// openFileClosed wants a closed handle to refuse every read and write, which no
// flag combination can show on its own.
func openFileClosed(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.OpenFileFS) {
	t.Run("OpenFileClosedHandle", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(closeOpenFileFS(t, fsys))

		f, err := fsys.OpenFile("closed.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC, openFilePerm)
		if err != nil {
			t.Fatalf("OpenFile(closed.txt) = %v, want nil", err)
		}
		if _, err := f.WriteString("kept"); err != nil {
			t.Fatalf("WriteString = %v, want nil", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
		if _, err := f.Read(make([]byte, 4)); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("Read after Close = %v, want %v", err, fs.ErrClosed)
		}
		if _, err := f.ReadAt(make([]byte, 4), 0); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("ReadAt after Close = %v, want %v", err, fs.ErrClosed)
		}
		if _, err := f.Write([]byte("x")); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("Write after Close = %v, want %v", err, fs.ErrClosed)
		}
		if _, err := f.WriteString("x"); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("WriteString after Close = %v, want %v", err, fs.ErrClosed)
		}
		// What a closed handle does to Seek, Stat and a second Close is left
		// unasserted: a plain os.File refuses all three while a handle that keeps
		// its content in memory answers them, and no file system may rely on
		// either.
		if got := readOpenFile(t, fsys, "closed.txt"); got != "kept" {
			t.Errorf("a closed handle left %q, want %q", got, "kept")
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
