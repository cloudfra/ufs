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
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
)

// failCreateFS is a [createFS] whose Create always fails.
type failCreateFS struct {
	createFS
}

func (failCreateFS) Create(name string) (types.File, error) {
	return nil, &fs.PathError{Op: "create", Path: name, Err: errNoSpace}
}

// readErrFile is a stream whose reads fail.
type readErrFile struct {
	streamHandle
}

func (readErrFile) Read([]byte) (int, error) {
	return 0, errNoSpace
}

// statErrStream is a stream whose Stat fails.
type statErrStream struct {
	streamHandle
}

func (statErrStream) Stat() (fs.FileInfo, error) {
	return nil, errNoSpace
}

func TestKeepInMemory(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		mode    BufferMode
		size    int64
		want    bool
		wantErr error
	}{
		{mode: BufferAuto, size: 0, want: true},
		{mode: BufferAuto, size: MemoryBufferLimit, want: true},
		{mode: BufferAuto, size: MemoryBufferLimit + 1, want: false},
		{mode: BufferMemory, size: MemoryBufferLimit + 1, want: true},
		{mode: BufferTempFile, size: 0, want: false},
		{mode: BufferMode(99), size: 0, wantErr: fs.ErrInvalid},
	}
	for _, tc := range testCases {
		got, err := keepInMemory(tc.mode, tc.size)
		if got != tc.want || !errors.Is(err, tc.wantErr) {
			t.Errorf("keepInMemory(%d, %d) = %t, %v, want %t, %v", tc.mode, tc.size, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestLoadBufferedFileErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		file    func(*store) fs.File
		mode    BufferMode
		wantErr error
	}{
		{
			name:    "a directory",
			file:    openDir,
			wantErr: fs.ErrInvalid,
		},
		{
			name:    "stat fails",
			file:    openStatErr,
			wantErr: errNoSpace,
		},
		{
			name:    "read into memory fails",
			file:    openReadErr,
			mode:    BufferMemory,
			wantErr: errNoSpace,
		},
		{
			name:    "read into a temporary file fails",
			file:    openReadErr,
			mode:    BufferTempFile,
			wantErr: errNoSpace,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newStore()
			s.seedFile("a", "alpha")
			if _, err := loadBufferedFile("a", tc.file(s), tc.mode); !errors.Is(err, tc.wantErr) {
				t.Errorf("loadBufferedFile() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestLoadBufferedFileAutoUsesTempFileForLargeFiles(t *testing.T) {
	t.Parallel()
	s := newStore()
	s.seedFile("big", string(make([]byte, MemoryBufferLimit+1)))
	f, err := loadBufferedFile("big", mustOpen(s, "big"), BufferAuto)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	f.readable = true
	if info, err := f.Stat(); err != nil || info.Size() != MemoryBufferLimit+1 {
		t.Errorf("Stat() = %v, %v", info, err)
	}
	p := make([]byte, 2)
	if n, err := f.ReadAt(p, MemoryBufferLimit-1); n != 2 || err != nil && !errors.Is(err, io.EOF) {
		t.Errorf("ReadAt() of the last bytes = %d, %v", n, err)
	}
}

func TestBufferedFileClosed(t *testing.T) {
	t.Parallel()
	for _, bm := range bufferModes {
		t.Run(bm.name, func(t *testing.T) {
			t.Parallel()
			s := newStore()
			s.seedFile("a", "alpha")
			f, err := OpenFileBuffered(createFS{s}, "a", os.O_RDWR, 0, bm.mode)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			p := make([]byte, 1)
			for name, op := range map[string]func() error{
				"Stat": func() error {
					_, err := f.Stat()
					return err
				},
				"Read": func() error {
					_, err := f.Read(p)
					return err
				},
				"ReadAt": func() error {
					_, err := f.ReadAt(p, 0)
					return err
				},
				"Write": func() error {
					_, err := f.Write(p)
					return err
				},
				"WriteString": func() error {
					_, err := f.WriteString("x")
					return err
				},
				"Seek": func() error {
					_, err := f.Seek(0, io.SeekStart)
					return err
				},
				"Close": f.Close,
			} {
				if err := op(); !errors.Is(err, fs.ErrClosed) {
					t.Errorf("%s() after Close = %v, want %v", name, err, fs.ErrClosed)
				}
			}
		})
	}
}

func TestBufferedFileWrite(t *testing.T) {
	t.Parallel()
	for _, bm := range bufferModes {
		t.Run(bm.name, func(t *testing.T) {
			t.Parallel()
			s := newStore()
			s.seedFile("a", "alpha")
			f, err := OpenFileBuffered(createFS{s}, "a", os.O_RDWR|os.O_APPEND, 0, bm.mode)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := f.Write([]byte("-beta")); n != 5 || err != nil {
				t.Errorf("Write() = %d, %v", n, err)
			}
			if pos, err := f.Seek(0, io.SeekCurrent); err != nil || pos != 10 {
				t.Errorf("position after an append = %d, %v, want 10", pos, err)
			}
			if _, err := f.Seek(-1, io.SeekStart); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Seek(-1) = %v, want %v", err, fs.ErrInvalid)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if got := string(s.files["a"]); got != "alpha-beta" {
				t.Errorf("content = %q, want %q", got, "alpha-beta")
			}
		})
	}
}

func TestBufferedFileWriteBackCreateFails(t *testing.T) {
	t.Parallel()
	for _, bm := range bufferModes {
		t.Run(bm.name, func(t *testing.T) {
			t.Parallel()
			s := newStore()
			s.seedFile("a", "alpha")
			f, err := OpenFileBuffered(failCreateFS{createFS{s}}, "a", os.O_WRONLY, 0, bm.mode)
			if err != nil {
				t.Fatal(err)
			}
			mustWriteString(t, f, "A")
			if err := f.Close(); !errors.Is(err, errNoSpace) {
				t.Errorf("Close() = %v, want %v", err, errNoSpace)
			}
			if got := string(s.files["a"]); got != "alpha" {
				t.Errorf("content = %q, want it unchanged", got)
			}
		})
	}
}

func openDir(*store) fs.File {
	return &fakeDir{name: "d"}
}

func openStatErr(s *store) fs.File {
	return statErrStream{streamHandle{mustOpen(s, "a")}}
}

func openReadErr(s *store) fs.File {
	return readErrFile{streamHandle{mustOpen(s, "a")}}
}

// mustOpen opens name in s as a *fakeHandle, for a test table that builds
// files before it has a *testing.T.
func mustOpen(s *store, name string) *fakeHandle {
	f, err := s.open(name)
	if err != nil {
		panic(err)
	}
	return f.(*fakeHandle)
}
