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
	"strings"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
)

// streamHandle hides every method of fakeHandle beyond fs.File, the way a file
// from an object store or a compressed archive entry can only be read in
// order.
type streamHandle struct {
	h *fakeHandle
}

func (s streamHandle) Stat() (fs.FileInfo, error) {
	return s.h.Stat()
}

func (s streamHandle) Read(p []byte) (int, error) {
	return s.h.Read(p)
}

func (s streamHandle) Close() error {
	return s.h.Close()
}

// seekOnly is a file that can seek but not read at an offset.
type seekOnly struct {
	h *fakeHandle
}

func (s seekOnly) Stat() (fs.FileInfo, error) {
	return s.h.Stat()
}

func (s seekOnly) Read(p []byte) (int, error) {
	return s.h.Read(p)
}

func (s seekOnly) Seek(offset int64, whence int) (int64, error) {
	return s.h.Seek(offset, whence)
}

func (s seekOnly) Close() error {
	return s.h.Close()
}

// streamWriter is a stream that can also be written, but has no WriteString.
type streamWriter struct {
	streamHandle
}

func (s streamWriter) Write(p []byte) (int, error) {
	return s.h.Write(p)
}

// streamStringWriter is a [streamWriter] with its own WriteString.
type streamStringWriter struct {
	streamWriter
}

func (s streamStringWriter) WriteString(str string) (int, error) {
	return s.h.WriteString(str)
}

// listDir is a directory that can be listed.
type listDir struct {
	*fakeDir
	entries []fs.DirEntry
}

func (d *listDir) ReadDir(int) ([]fs.DirEntry, error) {
	return d.entries, nil
}

// statErrFile is a stream whose Stat fails, so it has no name to report.
type statErrFile struct {
	streamHandle
}

func (statErrFile) Stat() (fs.FileInfo, error) {
	return nil, errors.New("stat failed")
}

// seekRecorder records the seeks made on a [types.File].
type seekRecorder struct {
	types.File
	whences []int
}

func (s *seekRecorder) Seek(offset int64, whence int) (int64, error) {
	s.whences = append(s.whences, whence)
	return s.File.Seek(offset, whence)
}

// openHandle opens name in s as a *fakeHandle.
func openHandle(t *testing.T, s *store, name string) *fakeHandle {
	t.Helper()
	f, err := s.open(name)
	if err != nil {
		t.Fatal(err)
	}
	return f.(*fakeHandle)
}

func TestToFileReturnsATypesFile(t *testing.T) {
	t.Parallel()
	s := newStore()
	s.seedFile("a", "alpha")
	native := openHandle(t, s, "a")
	if got := ToFile(native); got != types.File(native) {
		t.Errorf("ToFile(types.File) = %T, want the file itself", got)
	}
}

func TestToFileStream(t *testing.T) {
	t.Parallel()
	s := newStore()
	s.seedFile("d/a", "alpha")
	f := ToFile(streamHandle{openHandle(t, s, "d/a")})

	if info, err := f.Stat(); err != nil || info.Name() != "a" || info.Size() != 5 {
		t.Errorf("Stat() = %v, %v", info, err)
	}
	wantContent(t, f, "alpha")
	for name, write := range map[string]func() (int, error){
		"Write": func() (int, error) {
			return f.Write([]byte("x"))
		},
		"WriteString": func() (int, error) {
			return f.WriteString("x")
		},
	} {
		if _, err := write(); !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), "a") {
			t.Errorf("%s() = %v, want %v naming the file", name, err, fs.ErrPermission)
		}
	}
	if _, err := f.Seek(0, io.SeekStart); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Seek() = %v, want %v", err, errors.ErrUnsupported)
	}
	if _, err := f.ReadAt(make([]byte, 1), 0); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("ReadAt() = %v, want %v", err, errors.ErrUnsupported)
	}
	if err := f.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
	if s.closeCalls != 1 {
		t.Errorf("closes = %d, want 1", s.closeCalls)
	}
}

func TestToFileErrorNameWithoutStat(t *testing.T) {
	t.Parallel()
	s := newStore()
	s.seedFile("a", "alpha")
	f := ToFile(statErrFile{streamHandle{openHandle(t, s, "a")}})
	if _, err := f.Write([]byte("x")); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Write() = %v, want %v", err, fs.ErrPermission)
	}
}

func TestToFileWriter(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		file func(h *fakeHandle) fs.File
	}{
		{
			name: "Write only",
			file: func(h *fakeHandle) fs.File {
				return streamWriter{streamHandle{h}}
			},
		},
		{
			name: "Write and WriteString",
			file: func(h *fakeHandle) fs.File {
				return streamStringWriter{streamWriter{streamHandle{h}}}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newStore()
			s.seedFile("a", "")
			h := openHandle(t, s, "a")
			h.writable = true
			f := ToFile(tc.file(h))
			if n, err := f.Write([]byte("ab")); n != 2 || err != nil {
				t.Errorf("Write() = %d, %v", n, err)
			}
			mustWriteString(t, f, "cd")
			if got := string(s.files["a"]); got != "abcd" {
				t.Errorf("content = %q, want %q", got, "abcd")
			}
		})
	}
}

func TestToFileSeekReadAt(t *testing.T) {
	t.Parallel()
	s := newStore()
	s.seedFile("a", "alpha")
	h := openHandle(t, s, "a")
	f := ToFile(seekOnly{h})

	if pos, err := f.Seek(1, io.SeekStart); err != nil || pos != 1 {
		t.Fatalf("Seek() = %d, %v", pos, err)
	}
	wantReadAt(t, f, 3, "ha")
	p := make([]byte, 4)
	if n, err := f.ReadAt(p, 3); n != 2 || !errors.Is(err, io.EOF) {
		t.Errorf("ReadAt() past the end = %d, %v, want 2, EOF", n, err)
	}
	if pos, err := f.Seek(0, io.SeekCurrent); err != nil || pos != 1 {
		t.Errorf("position after ReadAt = %d, %v, want 1", pos, err)
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ReadAt(p, 0); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("ReadAt() of a closed file = %v, want %v", err, fs.ErrClosed)
	}
}

func TestToFileDirectory(t *testing.T) {
	t.Parallel()
	entries := []fs.DirEntry{fs.FileInfoToDirEntry(fakeInfo{name: "a"})}
	f := ToFile(&listDir{fakeDir: &fakeDir{name: "d"}, entries: entries})
	dir, ok := f.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("ToFile(directory) = %T, want an fs.ReadDirFile", f)
	}
	got, err := dir.ReadDir(-1)
	if err != nil || !reflect.DeepEqual(got, entries) {
		t.Errorf("ReadDir() = %v, %v, want %v", got, err, entries)
	}
	if _, ok := ToFile(&fakeDir{name: "d"}).(fs.ReadDirFile); ok {
		t.Error("ToFile() of a directory that cannot be listed is an fs.ReadDirFile")
	}
}

func TestReadOnlyFileMaterialize(t *testing.T) {
	t.Parallel()
	for _, bm := range bufferModes {
		t.Run(bm.name, func(t *testing.T) {
			t.Parallel()
			s := newStore()
			s.seedFile("a", "alpha")
			fsys := streamFS{s}
			f := toReadOnlyFile(fsys, "a", streamHandle{openHandle(t, s, "a")}, bm.mode)

			wantReadAt(t, f, 1, "lph")
			if info, err := f.Stat(); err != nil || info.Size() != 5 {
				t.Errorf("Stat() after buffering = %v, %v", info, err)
			}
			wantContent(t, f, "alpha")
			if _, err := f.WriteString("x"); !errors.Is(err, fs.ErrPermission) {
				t.Errorf("WriteString() = %v, want %v", err, fs.ErrPermission)
			}
			if err := f.Close(); err != nil {
				t.Errorf("Close() = %v", err)
			}
			// The first handle, and the second one opened to buffer it.
			if s.closeCalls != 2 {
				t.Errorf("closes = %d, want 2", s.closeCalls)
			}
		})
	}
}

func TestReadOnlyFileMaterializeErrors(t *testing.T) {
	t.Parallel()

	t.Run("the file is gone", func(t *testing.T) {
		t.Parallel()
		s := newStore()
		s.seedFile("a", "alpha")
		f := toReadOnlyFile(streamFS{s}, "a", streamHandle{openHandle(t, s, "a")}, BufferAuto)
		delete(s.files, "a")
		if _, err := f.Seek(0, io.SeekEnd); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Seek() = %v, want %v", err, fs.ErrNotExist)
		}
	})

	t.Run("unknown buffer mode", func(t *testing.T) {
		t.Parallel()
		s := newStore()
		s.seedFile("a", "alpha")
		f := toReadOnlyFile(streamFS{s}, "a", streamHandle{openHandle(t, s, "a")}, BufferMode(-1))
		if _, err := f.ReadAt(make([]byte, 1), 0); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("ReadAt() = %v, want %v", err, fs.ErrInvalid)
		}
		// The stream still works, and the failed copy closed its handle.
		wantContent(t, f, "alpha")
		if s.closeCalls != 1 {
			t.Errorf("closes = %d, want 1", s.closeCalls)
		}
	})
}

func TestAccessFile(t *testing.T) {
	t.Parallel()

	t.Run("write only", func(t *testing.T) {
		t.Parallel()
		s := newStore()
		f, err := s.create("a")
		if err != nil {
			t.Fatal(err)
		}
		af := &accessFile{File: f, name: "a"}
		if _, err := af.Read(make([]byte, 1)); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("Read() = %v, want %v", err, fs.ErrPermission)
		}
		if _, err := af.ReadAt(make([]byte, 1), 0); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("ReadAt() = %v, want %v", err, fs.ErrPermission)
		}
		if n, err := af.Write([]byte("ab")); n != 2 || err != nil {
			t.Errorf("Write() = %d, %v", n, err)
		}
	})

	t.Run("read write append", func(t *testing.T) {
		t.Parallel()
		s := newStore()
		f, err := s.openFile("a", os.O_RDWR|os.O_CREATE, 0)
		if err != nil {
			t.Fatal(err)
		}
		rec := &seekRecorder{File: f}
		af := &accessFile{File: rec, name: "a", readable: true, append: true}
		if _, err := af.Write([]byte("ab")); err != nil {
			t.Fatal(err)
		}
		mustWriteString(t, af, "cd")
		if want := []int{io.SeekEnd, io.SeekEnd}; !reflect.DeepEqual(rec.whences, want) {
			t.Errorf("seeks = %v, want %v: each append write must move to the end", rec.whences, want)
		}
		wantReadAt(t, af, 1, "bcd")
	})
}

func wantContent(t *testing.T, f io.Reader, want string) {
	t.Helper()
	got, err := io.ReadAll(f)
	if err != nil || string(got) != want {
		t.Errorf("ReadAll() = %q, %v, want %q", got, err, want)
	}
}

func wantReadAt(t *testing.T, f io.ReaderAt, off int64, want string) {
	t.Helper()
	p := make([]byte, len(want))
	n, err := f.ReadAt(p, off)
	if err != nil && (!errors.Is(err, io.EOF) || n != len(want)) {
		t.Errorf("ReadAt(%d) = %v", off, err)
	}
	if string(p[:n]) != want {
		t.Errorf("ReadAt(%d) = %q, want %q", off, p[:n], want)
	}
}

func mustWriteString(t *testing.T, f io.StringWriter, s string) {
	t.Helper()
	if n, err := f.WriteString(s); err != nil || n != len(s) {
		t.Fatalf("WriteString(%q) = %d, %v", s, n, err)
	}
}
