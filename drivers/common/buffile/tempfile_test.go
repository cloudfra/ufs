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

package buffile

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

var testModTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// storages builds a File with the given content in each kind of storage, so
// that a test can check that a temporary file behaves exactly like memory.
var storages = []struct {
	name string
	new  func(t *testing.T, content string) *File
}{
	{"memory", func(_ *testing.T, content string) *File {
		return newTestFile("dir/f.txt", content)
	}},
	{"tempfile", func(t *testing.T, content string) *File {
		t.Helper()
		f, err := NewTempFile("dir/f.txt", strings.NewReader(content), 0o644, testModTime)
		if err != nil {
			t.Fatalf("NewTempFile() = %v", err)
		}
		t.Cleanup(func() {
			if err := f.Release(); err != nil {
				t.Errorf("Release() = %v", err)
			}
		})
		return &f
	}},
}

func TestStorageReadWrite(t *testing.T) {
	t.Parallel()
	for _, st := range storages {
		t.Run(st.name, func(t *testing.T) {
			t.Parallel()
			f := st.new(t, "hello")

			info, err := f.Stat()
			if err != nil || info.Name() != "f.txt" || info.Size() != 5 || info.Mode() != 0o644 || !info.ModTime().Equal(testModTime) || info.IsDir() || info.Sys() != nil {
				t.Errorf("Stat() = %+v, %v", info, err)
			}

			p := make([]byte, 3)
			if n, err := f.Read(p); n != 3 || err != nil || string(p) != "hel" {
				t.Errorf("Read() = %d, %v, %q, want 3, nil, %q", n, err, p, "hel")
			}
			if n, err := f.Read(p); n != 2 || err != nil || string(p[:n]) != "lo" {
				t.Errorf("Read() at the tail = %d, %v, %q, want 2, nil, %q", n, err, p[:n], "lo")
			}
			if n, err := f.Read(p); n != 0 || !errors.Is(err, io.EOF) {
				t.Errorf("Read() at the end = %d, %v, want 0, EOF", n, err)
			}

			if n, err := f.ReadAt(p, 1); n != 3 || err != nil || string(p) != "ell" {
				t.Errorf("ReadAt(1) = %d, %v, %q", n, err, p)
			}
			if n, err := f.ReadAt(p, 2); n != 3 || !errors.Is(err, io.EOF) || string(p) != "llo" {
				t.Errorf("ReadAt(2) up to the end = %d, %v, %q, want EOF with the last bytes", n, err, p)
			}
			if n, err := f.ReadAt(p, 9); n != 0 || !errors.Is(err, io.EOF) {
				t.Errorf("ReadAt(9) = %d, %v, want 0, EOF", n, err)
			}
			if _, err := f.ReadAt(p, -1); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("ReadAt(-1) = %v, want %v", err, fs.ErrInvalid)
			}

			if f.Dirty() {
				t.Error("Dirty() before a write = true")
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			if n, err := f.Write([]byte("J")); n != 1 || err != nil {
				t.Fatalf("Write() = %d, %v", n, err)
			}
			if _, err := f.Seek(2, io.SeekEnd); err != nil {
				t.Fatal(err)
			}
			if n, err := f.WriteString("!"); n != 1 || err != nil {
				t.Fatalf("WriteString() = %d, %v", n, err)
			}
			if !f.Dirty() {
				t.Error("Dirty() after a write = false")
			}
			if got, want := readAllAt(t, f), "Jello\x00\x00!"; got != want {
				t.Errorf("content = %q, want %q", got, want)
			}
			if end, err := f.Seek(0, io.SeekEnd); err != nil || end != 8 {
				t.Errorf("Seek(0, SeekEnd) = %d, %v, want 8", end, err)
			}
		})
	}
}

func TestStorageIOTestReader(t *testing.T) {
	t.Parallel()
	content := strings.Repeat("0123456789", 100)
	for _, st := range storages {
		t.Run(st.name, func(t *testing.T) {
			t.Parallel()
			if err := iotest.TestReader(st.new(t, content), []byte(content)); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestStorageTakeDirty(t *testing.T) {
	t.Parallel()
	for _, st := range storages {
		t.Run(st.name, func(t *testing.T) {
			t.Parallel()
			f := st.new(t, "abc")
			if _, _, ok := f.TakeDirty(time.Now()); ok {
				t.Error("TakeDirty() of a clean file = ok")
			}
			if _, err := f.WriteString("X"); err != nil {
				t.Fatal(err)
			}
			stamp := testModTime.Add(time.Hour)
			content, mode, ok := f.TakeDirty(stamp)
			if !ok || string(content) != "Xbc" || mode != 0o644 {
				t.Errorf("TakeDirty() = %q, %v, %t, want %q, 0644, true", content, mode, ok, "Xbc")
			}
			if f.Dirty() {
				t.Error("Dirty() after TakeDirty = true")
			}
			if info := statOf(t, f); !info.ModTime().Equal(stamp) {
				t.Errorf("ModTime() = %v, want %v", info.ModTime(), stamp)
			}
		})
	}
}

func TestStorageFlushTo(t *testing.T) {
	t.Parallel()
	for _, st := range storages {
		t.Run(st.name, func(t *testing.T) {
			t.Parallel()
			f := st.new(t, "abc")
			var dst bytes.Buffer
			if ok, err := f.FlushTo(&dst, time.Now()); ok || err != nil || dst.Len() != 0 {
				t.Errorf("FlushTo() of a clean file = %t, %v, wrote %q", ok, err, dst.String())
			}

			if _, err := f.WriteString("X"); err != nil {
				t.Fatal(err)
			}
			if ok, err := f.FlushTo(failingWriter{}, time.Now()); !ok || !errors.Is(err, errFailingWrite) {
				t.Errorf("FlushTo() into a failing writer = %t, %v, want true, %v", ok, err, errFailingWrite)
			}
			if !f.Dirty() {
				t.Error("Dirty() after a failed FlushTo = false, want the write kept for a retry")
			}

			stamp := testModTime.Add(time.Hour)
			if ok, err := f.FlushTo(&dst, stamp); !ok || err != nil || dst.String() != "Xbc" {
				t.Errorf("FlushTo() = %t, %v, wrote %q, want true, nil, %q", ok, err, dst.String(), "Xbc")
			}
			if f.Dirty() {
				t.Error("Dirty() after FlushTo = true")
			}
			if info := statOf(t, f); !info.ModTime().Equal(stamp) {
				t.Errorf("ModTime() = %v, want %v", info.ModTime(), stamp)
			}
		})
	}
}

func TestFlushToShortWrite(t *testing.T) {
	t.Parallel()
	f := newTestFile("f.txt", "")
	if _, err := f.WriteString("abcd"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.FlushTo(shortWriter{}, time.Now()); !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("FlushTo() = %v, want %v", err, io.ErrShortWrite)
	}
}

func TestNewTempFileReadError(t *testing.T) {
	t.Parallel()
	errRead := errors.New("read failed")
	if _, err := NewTempFile("f.txt", iotest.ErrReader(errRead), 0o644, testModTime); !errors.Is(err, errRead) {
		t.Errorf("NewTempFile() = %v, want %v", err, errRead)
	}
}

func TestRelease(t *testing.T) {
	t.Parallel()

	f, err := NewTempFile("f.txt", strings.NewReader("abc"), 0o644, testModTime)
	if err != nil {
		t.Fatal(err)
	}
	name := f.tmp.Name()
	if err := f.Release(); err != nil {
		t.Fatalf("Release() = %v", err)
	}
	if _, err := os.Stat(name); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("temporary file after Release: Stat() = %v, want %v", err, fs.ErrNotExist)
	}
	if info := statOf(t, &f); info.Size() != 0 {
		t.Errorf("Size() after Release = %d, want 0", info.Size())
	}

	mem := newTestFile("f.txt", "abc")
	if err := mem.Release(); err != nil {
		t.Errorf("Release() of a memory file = %v", err)
	}
	if info := statOf(t, mem); info.Size() != 0 {
		t.Errorf("Size() after Release = %d, want 0", info.Size())
	}
}

func statOf(t *testing.T, f *File) fs.FileInfo {
	t.Helper()
	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() = %v", err)
	}
	return info
}

var errFailingWrite = errors.New("write failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errFailingWrite
}

// shortWriter reports fewer bytes than it was given without an error.
type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	return len(p) / 2, nil
}
