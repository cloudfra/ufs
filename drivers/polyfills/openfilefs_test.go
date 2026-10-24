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

package polyfills

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"reflect"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
)

// streamFS implements fs.FS with files that can only be read in order, the way
// an object store or a compressed archive entry is.
type streamFS struct {
	*store
}

func (f streamFS) Open(name string) (fs.File, error) {
	file, err := f.open(name)
	if h, ok := file.(*fakeHandle); ok {
		return streamHandle{h}, nil
	}
	return file, err
}

// streamCreateFS is a [streamFS] that also implements [types.CreateFileFS].
type streamCreateFS struct {
	streamFS
}

func (f streamCreateFS) Create(name string) (types.File, error) {
	return f.create(name)
}

func newPlainFS(s *store) fs.FS {
	return plainFS{s}
}

func newCreateFS(s *store) fs.FS {
	return createFS{s}
}

func newOpenFileFS(s *store) fs.FS {
	return openFileFS{s}
}

func newStreamFS(s *store) fs.FS {
	return streamFS{s}
}

func newStreamCreateFS(s *store) fs.FS {
	return streamCreateFS{streamFS{s}}
}

var bufferModes = []struct {
	name string
	mode BufferMode
}{
	{"auto", BufferAuto},
	{"memory", BufferMemory},
	{"tempfile", BufferTempFile},
}

func TestOpenFile(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		wrap      func(*store) fs.FS
		files     map[string]string
		dirs      []string
		file      string
		flag      int
		use       func(*testing.T, types.File)
		wantErr   error
		wantText  string
		wantFiles map[string]string
		wantOpens int
	}{
		{
			name:      "native OpenFileFS wins over the emulation",
			wrap:      newOpenFileFS,
			files:     map[string]string{"a": "alpha"},
			file:      "a",
			flag:      os.O_RDWR,
			wantFiles: map[string]string{"a": "alpha"},
			wantOpens: 1,
		},
		{
			name:    "invalid path",
			wrap:    newCreateFS,
			file:    "../a",
			flag:    os.O_RDONLY,
			wantErr: fs.ErrInvalid,
		},
		{
			name:    "missing file without O_CREATE",
			wrap:    newCreateFS,
			file:    "a",
			flag:    os.O_RDWR,
			wantErr: fs.ErrNotExist,
		},
		{
			name:      "O_EXCL on an existing file",
			wrap:      newCreateFS,
			files:     map[string]string{"a": "alpha"},
			file:      "a",
			flag:      os.O_WRONLY | os.O_CREATE | os.O_EXCL,
			wantErr:   fs.ErrExist,
			wantFiles: map[string]string{"a": "alpha"},
		},
		{
			name:    "writing a directory",
			wrap:    newCreateFS,
			dirs:    []string{"d"},
			file:    "d",
			flag:    os.O_WRONLY,
			wantErr: fs.ErrInvalid,
		},
		{
			name:      "writing without CreateFileFS",
			wrap:      newPlainFS,
			files:     map[string]string{"a": "alpha"},
			file:      "a",
			flag:      os.O_WRONLY | os.O_TRUNC,
			wantText:  `does not support "open"`,
			wantFiles: map[string]string{"a": "alpha"},
		},
		{
			name:  "read-only open of a plain FS cannot write",
			wrap:  newPlainFS,
			files: map[string]string{"a": "alpha"},
			file:  "a",
			flag:  os.O_RDONLY,
			use: func(t *testing.T, f types.File) {
				wantContent(t, f, "alpha")
				if _, err := f.Write([]byte("x")); !errors.Is(err, fs.ErrPermission) {
					t.Errorf("Write() = %v, want %v", err, fs.ErrPermission)
				}
			},
			wantFiles: map[string]string{"a": "alpha"},
		},
		{
			name: "read-only O_CREATE creates an empty file",
			wrap: newCreateFS,
			file: "a",
			flag: os.O_RDONLY | os.O_CREATE,
			use: func(t *testing.T, f types.File) {
				wantContent(t, f, "")
			},
			wantFiles: map[string]string{"a": ""},
			wantOpens: 1,
		},
		{
			name:  "O_TRUNC replaces the content",
			wrap:  newCreateFS,
			files: map[string]string{"a": "alpha"},
			file:  "a",
			flag:  os.O_WRONLY | os.O_TRUNC,
			use: func(t *testing.T, f types.File) {
				mustWriteString(t, f, "new")
				if _, err := f.Read(make([]byte, 1)); !errors.Is(err, fs.ErrPermission) {
					t.Errorf("Read() of a write-only file = %v, want %v", err, fs.ErrPermission)
				}
			},
			wantFiles: map[string]string{"a": "new"},
			wantOpens: 1,
		},
		{
			name: "O_CREATE of a new file",
			wrap: newCreateFS,
			file: "a",
			flag: os.O_RDWR | os.O_CREATE,
			use: func(t *testing.T, f types.File) {
				mustWriteString(t, f, "new")
			},
			wantFiles: map[string]string{"a": "new"},
			wantOpens: 1,
		},
		{
			name:  "O_RDWR edits in place on Close",
			wrap:  newCreateFS,
			files: map[string]string{"a": "alpha"},
			file:  "a",
			flag:  os.O_RDWR,
			use: func(t *testing.T, f types.File) {
				wantContent(t, f, "alpha")
				if _, err := f.Seek(1, io.SeekStart); err != nil {
					t.Fatalf("Seek() = %v", err)
				}
				mustWriteString(t, f, "LP")
				if info, err := f.Stat(); err != nil || info.Size() != 5 {
					t.Errorf("Stat() = %v, %v, want size 5", info, err)
				}
			},
			wantFiles: map[string]string{"a": "aLPha"},
			wantOpens: 1,
		},
		{
			name:  "O_WRONLY keeps the content and cannot read",
			wrap:  newCreateFS,
			files: map[string]string{"a": "alpha"},
			file:  "a",
			flag:  os.O_WRONLY,
			use: func(t *testing.T, f types.File) {
				mustWriteString(t, f, "AL")
				if _, err := f.ReadAt(make([]byte, 1), 0); !errors.Is(err, fs.ErrPermission) {
					t.Errorf("ReadAt() of a write-only file = %v, want %v", err, fs.ErrPermission)
				}
			},
			wantFiles: map[string]string{"a": "ALpha"},
			wantOpens: 1,
		},
		{
			name:  "O_APPEND writes at the end after a seek",
			wrap:  newCreateFS,
			files: map[string]string{"a": "alpha"},
			file:  "a",
			flag:  os.O_WRONLY | os.O_APPEND,
			use: func(t *testing.T, f types.File) {
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					t.Fatalf("Seek() = %v", err)
				}
				mustWriteString(t, f, "-beta")
			},
			wantFiles: map[string]string{"a": "alpha-beta"},
			wantOpens: 1,
		},
		{
			name:  "a write past the end leaves a zero gap",
			wrap:  newCreateFS,
			files: map[string]string{"a": "ab"},
			file:  "a",
			flag:  os.O_RDWR,
			use: func(t *testing.T, f types.File) {
				if _, err := f.Seek(4, io.SeekStart); err != nil {
					t.Fatalf("Seek() = %v", err)
				}
				mustWriteString(t, f, "c")
			},
			wantFiles: map[string]string{"a": "ab\x00\x00c"},
			wantOpens: 1,
		},
		{
			name:  "an update without writes is not written back",
			wrap:  newCreateFS,
			files: map[string]string{"a": "alpha"},
			file:  "a",
			flag:  os.O_RDWR,
			use: func(t *testing.T, f types.File) {
				wantContent(t, f, "alpha")
			},
			wantFiles: map[string]string{"a": "alpha"},
		},
		{
			name:  "an update of a stream FS",
			wrap:  newStreamCreateFS,
			files: map[string]string{"a": "alpha"},
			file:  "a",
			flag:  os.O_RDWR | os.O_APPEND,
			use: func(t *testing.T, f types.File) {
				mustWriteString(t, f, "!")
				wantReadAt(t, f, 3, "ha!")
			},
			wantFiles: map[string]string{"a": "alpha!"},
			wantOpens: 1,
		},
		{
			name:  "a read-only stream is buffered on its first seek",
			wrap:  newStreamFS,
			files: map[string]string{"a": "alpha"},
			file:  "a",
			flag:  os.O_RDONLY,
			use: func(t *testing.T, f types.File) {
				p := make([]byte, 2)
				if _, err := io.ReadFull(f, p); err != nil || string(p) != "al" {
					t.Fatalf("Read() = %q, %v, want %q", p, err, "al")
				}
				if pos, err := f.Seek(0, io.SeekCurrent); err != nil || pos != 2 {
					t.Errorf("Seek(0, SeekCurrent) = %d, %v, want 2", pos, err)
				}
				wantReadAt(t, f, 1, "lph")
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					t.Fatalf("Seek() = %v", err)
				}
				wantContent(t, f, "alpha")
				if _, err := f.Write([]byte("x")); !errors.Is(err, fs.ErrPermission) {
					t.Errorf("Write() = %v, want %v", err, fs.ErrPermission)
				}
			},
			wantFiles: map[string]string{"a": "alpha"},
		},
	}

	for _, tc := range testCases {
		for _, bm := range bufferModes {
			t.Run(tc.name+"/"+bm.name, func(t *testing.T) {
				t.Parallel()
				s := newStore()
				for name, data := range tc.files {
					s.seedFile(name, data)
				}
				for _, dir := range tc.dirs {
					s.seedDir(dir)
				}
				f, err := OpenFileBuffered(tc.wrap(s), tc.file, tc.flag, filePerm, bm.mode)
				switch {
				case tc.wantText != "":
					checkWantErr(t, "OpenFileBuffered", err, tc.wantText)
				case tc.wantErr != nil:
					if !errors.Is(err, tc.wantErr) {
						t.Fatalf("OpenFileBuffered() = %v, want %v", err, tc.wantErr)
					}
				default:
					if err != nil {
						t.Fatalf("OpenFileBuffered() = %v", err)
					}
					if tc.use != nil {
						tc.use(t, f)
					}
					if err := f.Close(); err != nil {
						t.Fatalf("Close() = %v", err)
					}
					if err := f.Close(); err == nil {
						t.Error("second Close() = nil, want an error")
					}
				}
				if got := stringFiles(s); !reflect.DeepEqual(got, tc.wantFiles) {
					t.Errorf("files = %q, want %q", got, tc.wantFiles)
				}
				if s.openCalls != tc.wantOpens {
					t.Errorf("writable opens = %d, want %d", s.openCalls, tc.wantOpens)
				}
			})
		}
	}
}

func TestOpenFileUpdateNotVisibleBeforeClose(t *testing.T) {
	t.Parallel()
	for _, bm := range bufferModes {
		t.Run(bm.name, func(t *testing.T) {
			t.Parallel()
			s := newStore()
			s.seedFile("a", "alpha")
			f, err := OpenFileBuffered(createFS{s}, "a", os.O_WRONLY, 0, bm.mode)
			if err != nil {
				t.Fatalf("OpenFileBuffered() = %v", err)
			}
			mustWriteString(t, f, "ALPHA")
			if got := string(s.files["a"]); got != "alpha" {
				t.Errorf("content before Close = %q, want %q", got, "alpha")
			}
			if err := f.Close(); err != nil {
				t.Fatalf("Close() = %v", err)
			}
			if got := string(s.files["a"]); got != "ALPHA" {
				t.Errorf("content after Close = %q, want %q", got, "ALPHA")
			}
		})
	}
}

func TestOpenFileWriteBackError(t *testing.T) {
	t.Parallel()
	s := newStore()
	s.seedFile("a", "alpha")
	s.writeErrOn = "a"
	f, err := OpenFile(createFS{s}, "a", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile() = %v", err)
	}
	mustWriteString(t, f, "x")
	if err := f.Close(); !errors.Is(err, errNoSpace) {
		t.Errorf("Close() = %v, want %v", err, errNoSpace)
	}
	// The stat, the copy and the failed write-back each closed their handle.
	if s.closeCalls != 3 {
		t.Errorf("closes = %d, want 3: a handle was left open", s.closeCalls)
	}
}

func TestBufferedFileStat(t *testing.T) {
	t.Parallel()
	for _, bm := range bufferModes {
		t.Run(bm.name, func(t *testing.T) {
			t.Parallel()
			s := newStore()
			s.seedFile("d/a", "alpha")
			f, err := OpenFileBuffered(createFS{s}, "d/a", os.O_RDWR|os.O_APPEND, 0, bm.mode)
			if err != nil {
				t.Fatalf("OpenFileBuffered() = %v", err)
			}
			mustWriteString(t, f, "-beta")
			info, err := f.Stat()
			if err != nil {
				t.Fatalf("Stat() = %v", err)
			}
			if info.Name() != "a" || info.Size() != int64(len("alpha-beta")) || info.Mode() != filePerm {
				t.Errorf("Stat() = %q size %d mode %v", info.Name(), info.Size(), info.Mode())
			}
			if err := f.Close(); err != nil {
				t.Fatalf("Close() = %v", err)
			}
			if _, err := f.Stat(); !errors.Is(err, fs.ErrClosed) {
				t.Errorf("Stat() after Close = %v, want %v", err, fs.ErrClosed)
			}
		})
	}
}

func stringFiles(s *store) map[string]string {
	if len(s.files) == 0 {
		return nil
	}
	files := make(map[string]string, len(s.files))
	for name, data := range s.files {
		files[name] = string(data)
	}
	return files
}
