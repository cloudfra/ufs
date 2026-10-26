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
	"bytes"
	"errors"
	"io"
	"io/fs"
	"runtime"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// ---------------------------------------------------------------------------
// Polyfill test stubs
//
// Each stub implements a different subset of the types.File interface so tests can
// verify that ToBufferedFile delegates or fills in exactly
// the right methods.
// ---------------------------------------------------------------------------

// testBareFile implements only fs.File (Stat, Read, Close).
// No Seek, ReadAt, Write, or WriteString.
type testBareFile struct {
	r    *bytes.Reader
	name string
}

func newTestBareFile(name, content string) *testBareFile {
	return &testBareFile{r: bytes.NewReader([]byte(content)), name: name}
}

func (f *testBareFile) Stat() (fs.FileInfo, error) {
	return fakeInfo{name: f.name, size: f.r.Size()}, nil
}
func (f *testBareFile) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *testBareFile) Close() error               { return nil }

// testCloseCountingFile records how many times Close ran, so a test can check
// that a failure while releasing a buffer still closes the underlying file.
type testCloseCountingFile struct {
	*testBareFile
	closeCalls int
}

func newTestCloseCountingFile(name, content string) *testCloseCountingFile {
	return &testCloseCountingFile{testBareFile: newTestBareFile(name, content)}
}

func (f *testCloseCountingFile) Close() error {
	f.closeCalls++
	return nil
}

// testWriterFile adds io.Writer to testBareFile. No Seek, ReadAt, or WriteString.
type testWriterFile struct {
	*testBareFile
	written []byte
}

func newTestWriterFile(name, content string) *testWriterFile {
	return &testWriterFile{testBareFile: newTestBareFile(name, content)}
}

func (f *testWriterFile) Write(p []byte) (int, error) {
	f.written = append(f.written, p...)
	return len(p), nil
}

// testStringWriterFile adds io.StringWriter to testWriterFile.
type testStringWriterFile struct {
	*testWriterFile
	stringWriterCalled bool
}

func newTestStringWriterFile(name, content string) *testStringWriterFile {
	return &testStringWriterFile{testWriterFile: newTestWriterFile(name, content)}
}

func (f *testStringWriterFile) WriteString(s string) (int, error) {
	f.stringWriterCalled = true
	return f.Write([]byte(s))
}

// testSeekerFile implements fs.File + io.Seeker + io.ReaderAt (all via bytes.Reader).
// No Write or WriteString.
type testSeekerFile struct {
	r    *bytes.Reader
	name string
}

func newTestSeekerFile(name, content string) *testSeekerFile {
	return &testSeekerFile{r: bytes.NewReader([]byte(content)), name: name}
}

func (f *testSeekerFile) Stat() (fs.FileInfo, error) {
	return fakeInfo{name: f.name, size: f.r.Size()}, nil
}
func (f *testSeekerFile) Read(p []byte) (int, error)                { return f.r.Read(p) }
func (f *testSeekerFile) Close() error                              { return nil }
func (f *testSeekerFile) Seek(off int64, whence int) (int64, error) { return f.r.Seek(off, whence) }
func (f *testSeekerFile) ReadAt(p []byte, off int64) (int, error)   { return f.r.ReadAt(p, off) }

// testWritableSeekerFile implements fs.File + io.Writer + io.Seeker. It has no
// ReadAt, so the wrapper keeps the native Seek and must refuse ReadAt rather
// than buffer a file that is open for writing.
type testWritableSeekerFile struct {
	r       *bytes.Reader
	written []byte
	name    string
}

func newTestWritableSeekerFile(name, content string) *testWritableSeekerFile {
	return &testWritableSeekerFile{r: bytes.NewReader([]byte(content)), name: name}
}

func (f *testWritableSeekerFile) Stat() (fs.FileInfo, error) {
	return fakeInfo{name: f.name, size: f.r.Size()}, nil
}
func (f *testWritableSeekerFile) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *testWritableSeekerFile) Close() error               { return nil }
func (f *testWritableSeekerFile) Seek(off int64, whence int) (int64, error) {
	return f.r.Seek(off, whence)
}

func (f *testWritableSeekerFile) Write(p []byte) (int, error) {
	f.written = append(f.written, p...)
	return len(p), nil
}

// testReadWriteFile implements only Stat, Read, Write and Close, the method set
// reported in https://github.com/cloudfra/ufs/issues/451. Reads and writes both
// reach the same byte slice, so a buffer inside the wrapper would disagree with
// the content this file reports.
type testReadWriteFile struct {
	name string
	b    []byte
	pos  int
}

func newTestReadWriteFile(name, content string) *testReadWriteFile {
	return &testReadWriteFile{name: name, b: []byte(content)}
}

func (f *testReadWriteFile) Stat() (fs.FileInfo, error) {
	return fakeInfo{name: f.name, size: int64(len(f.b))}, nil
}

func (f *testReadWriteFile) Read(p []byte) (int, error) {
	if f.pos >= len(f.b) {
		return 0, io.EOF
	}
	n := copy(p, f.b[f.pos:])
	f.pos += n
	return n, nil
}

func (f *testReadWriteFile) Write(p []byte) (int, error) {
	f.b = append(f.b, p...)
	return len(p), nil
}

func (f *testReadWriteFile) Close() error { return nil }

// ---------------------------------------------------------------------------
// Tests for ToBufferedFile with readOnly=true
// ---------------------------------------------------------------------------

func TestToBufferedFileReadOnly(t *testing.T) {
	t.Run("fast_path_when_already_satisfies_File", func(t *testing.T) {
		base := &fakeHandle{store: newStore(), name: "t.txt"}
		got, err := ToBufferedFile(base, true, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		if got != base {
			t.Error("expected same value; file already satisfies types.File so no wrapper should be created")
		}
	})

	t.Run("read_through_buffer", func(t *testing.T) {
		const content = "hello world"
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		got, err := io.ReadAll(wrapped)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != content {
			t.Errorf("Read = %q, want %q", got, content)
		}
	})

	t.Run("seek_to_start_after_partial_read", func(t *testing.T) {
		const content = "hello world"
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		// Consume first 5 bytes.
		if _, err := io.ReadFull(wrapped, make([]byte, 5)); err != nil {
			t.Fatal(err)
		}
		pos, err := wrapped.Seek(0, io.SeekStart)
		if err != nil {
			t.Fatalf("Seek(0, SeekStart) = %v", err)
		}
		if pos != 0 {
			t.Errorf("Seek returned %d, want 0", pos)
		}
		got, err := io.ReadAll(wrapped)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != content {
			t.Errorf("Read after Seek = %q, want %q", got, content)
		}
	})

	t.Run("seek_current_and_end", func(t *testing.T) {
		const content = "abcdefghij" // 10 bytes
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		if _, err := wrapped.Seek(3, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		pos, err := wrapped.Seek(2, io.SeekCurrent)
		if err != nil {
			t.Fatalf("Seek(2, SeekCurrent) = %v", err)
		}
		if pos != 5 {
			t.Errorf("Seek(+2 from 3) = %d, want 5", pos)
		}
		pos, err = wrapped.Seek(-3, io.SeekEnd)
		if err != nil {
			t.Fatalf("Seek(-3, SeekEnd) = %v", err)
		}
		if pos != 7 {
			t.Errorf("Seek(-3 from end) = %d, want 7", pos)
		}
		buf := make([]byte, 3)
		if _, err := io.ReadFull(wrapped, buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != "hij" {
			t.Errorf("Read after Seek(-3, SeekEnd) = %q, want %q", buf, "hij")
		}
	})

	t.Run("readat_does_not_affect_read_position", func(t *testing.T) {
		const content = "hello world"
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		buf := make([]byte, 5)
		n, err := wrapped.ReadAt(buf, 6)
		if err != nil || n != 5 || string(buf) != "world" {
			t.Fatalf("ReadAt(5, 6) = %q %v; want %q nil", buf[:n], err, "world")
		}
		// Read position is still at 0.
		first5 := make([]byte, 5)
		if _, err := io.ReadFull(wrapped, first5); err != nil {
			t.Fatal(err)
		}
		if string(first5) != "hello" {
			t.Errorf("Read after ReadAt = %q, want %q", first5, "hello")
		}
	})

	t.Run("write_always_errors", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "content"), true, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		if _, err := wrapped.Write([]byte("x")); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Write() = %v, want fs.ErrInvalid", err)
		}
		if _, err := wrapped.WriteString("x"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("WriteString() = %v, want fs.ErrInvalid", err)
		}
	})

	t.Run("write_always_errors_even_when_underlying_supports_write", func(t *testing.T) {
		f := newTestWriterFile("t.txt", "content")
		wrapped, err := ToBufferedFile(f, true, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		if _, err := wrapped.Write([]byte("x")); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Write() = %v, want fs.ErrInvalid", err)
		}
		if _, err := wrapped.WriteString("x"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("WriteString() = %v, want fs.ErrInvalid", err)
		}
		if len(f.written) > 0 {
			t.Errorf("underlying writer received %d bytes, want 0", len(f.written))
		}
	})

	t.Run("stat_delegates_to_underlying", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestBareFile("myfile.txt", "hello"), true, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		info, err := wrapped.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if info.Name() != "myfile.txt" {
			t.Errorf("Stat().Name() = %q, want %q", info.Name(), "myfile.txt")
		}
	})

	t.Run("close_releases_buffer", func(t *testing.T) {
		got, err := ToBufferedFile(newTestBareFile("t.txt", "content"), true, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		nf := got.(*bufferedFile)
		if nf.buf == nil {
			t.Fatal("expected non-nil buffer before close")
		}
		if err := got.Close(); err != nil {
			t.Fatal(err)
		}
		if nf.buf != nil {
			t.Error("expected nil buffer after close")
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for ToBufferedFile with readOnly=false
// ---------------------------------------------------------------------------

func TestToBufferedFileWritable(t *testing.T) {
	t.Run("fast_path_when_already_satisfies_File", func(t *testing.T) {
		base := &fakeHandle{store: newStore(), name: "t.txt"}
		got, err := ToBufferedFile(base, false, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		if got != base {
			t.Error("expected same value; file already satisfies types.File so no wrapper should be created")
		}
	})

	t.Run("no_buffer_when_seek_and_readat_present", func(t *testing.T) {
		f := newTestSeekerFile("t.txt", "hello world")
		wrapped, err := ToBufferedFile(f, false, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		nf := wrapped.(*bufferedFile)
		if nf.buf != nil {
			t.Error("expected nil buffer when underlying provides both Seek and ReadAt")
		}
		// Verify native Seek and ReadAt still work through delegation.
		if _, err := wrapped.Seek(6, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 5)
		if _, err := io.ReadFull(wrapped, buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != "world" {
			t.Errorf("Read after Seek(6) = %q, want %q", buf, "world")
		}
	})

	t.Run("write_errors_without_underlying_writer", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "content"), false, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		if _, err := wrapped.Write([]byte("x")); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Write() = %v, want fs.ErrInvalid", err)
		}
		if _, err := wrapped.WriteString("x"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("WriteString() = %v, want fs.ErrInvalid", err)
		}
	})

	t.Run("write_delegates_to_underlying_writer", func(t *testing.T) {
		f := newTestWriterFile("t.txt", "")
		wrapped, err := ToBufferedFile(f, false, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		if _, err := wrapped.Write([]byte("hello")); err != nil {
			t.Errorf("Write() = %v, want nil", err)
		}
		if string(f.written) != "hello" {
			t.Errorf("underlying received %q, want %q", f.written, "hello")
		}
	})

	t.Run("writestring_derived_from_write_when_no_stringwriter", func(t *testing.T) {
		f := newTestWriterFile("t.txt", "")
		wrapped, err := ToBufferedFile(f, false, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		if _, err := wrapped.WriteString("world"); err != nil {
			t.Errorf("WriteString() = %v, want nil", err)
		}
		if string(f.written) != "world" {
			t.Errorf("underlying received %q, want %q", f.written, "world")
		}
	})

	t.Run("writestring_delegates_to_underlying_stringwriter", func(t *testing.T) {
		f := newTestStringWriterFile("t.txt", "")
		wrapped, err := ToBufferedFile(f, false, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		if _, err := wrapped.WriteString("world"); err != nil {
			t.Errorf("WriteString() = %v, want nil", err)
		}
		if !f.stringWriterCalled {
			t.Error("expected underlying WriteString to be called directly, not derived from Write")
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for ToBufferedFile
// ---------------------------------------------------------------------------

func TestToBufferedFile(t *testing.T) {
	t.Run("fast_path_when_already_satisfies_File", func(t *testing.T) {
		base := &fakeHandle{store: newStore(), name: "t.txt"}
		for _, readOnly := range []bool{true, false} {
			got, err := ToBufferedFile(base, readOnly, BufferMemory)
			if err != nil {
				t.Fatal(err)
			}
			if got != base {
				t.Errorf("readOnly=%t: expected same value; file already satisfies types.File", readOnly)
			}
		}
	})

	t.Run("readonly_write_errors", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "content"), true, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		if _, err := wrapped.Write([]byte("x")); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Write() = %v, want fs.ErrInvalid", err)
		}
		if _, err := wrapped.WriteString("x"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("WriteString() = %v, want fs.ErrInvalid", err)
		}
	})

	t.Run("readwrite_delegates_write", func(t *testing.T) {
		f := newTestWriterFile("t.txt", "")
		wrapped, err := ToBufferedFile(f, false, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		if _, err := wrapped.Write([]byte("hello")); err != nil {
			t.Errorf("Write() = %v, want nil", err)
		}
		if string(f.written) != "hello" {
			t.Errorf("underlying received %q, want %q", f.written, "hello")
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for a file open for writing that lacks Seek or ReadAt
//
// Buffering such a file gives the wrapper a second copy of the content that
// never sees the writes made through it, so the missing method is refused
// instead. See https://github.com/cloudfra/ufs/issues/451.
// ---------------------------------------------------------------------------

func TestToBufferedFileWritableNoBuffer(t *testing.T) {
	t.Run("read_and_write_agree_without_a_buffer", func(t *testing.T) {
		f := newTestReadWriteFile("t.txt", "old")
		wrapped, err := ToBufferedFile(f, false, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		nf := wrapped.(*bufferedFile)
		if nf.buf != nil {
			t.Error("expected nil buf for a file open for writing")
		}
		if nf.tmpFile != nil {
			t.Error("expected nil tmpFile for a file open for writing")
		}
		if _, err := wrapped.Write([]byte("NEW")); err != nil {
			t.Fatalf("Write() = %v, want nil", err)
		}
		got, err := io.ReadAll(wrapped)
		if err != nil {
			t.Fatalf("ReadAll() = %v", err)
		}
		if string(got) != "oldNEW" {
			t.Errorf("Read after Write = %q, want %q", got, "oldNEW")
		}
		info, err := wrapped.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() != int64(len("oldNEW")) {
			t.Errorf("Stat().Size() = %d, want %d", info.Size(), len("oldNEW"))
		}
	})

	t.Run("seek_and_readat_refused_without_underlying_support", func(t *testing.T) {
		f := newTestReadWriteFile("t.txt", "old")
		wrapped, err := ToBufferedFile(f, false, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		if _, err := wrapped.Seek(0, io.SeekStart); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Seek(0, SeekStart) = %v, want fs.ErrInvalid", err)
		}
		if _, err := wrapped.ReadAt(make([]byte, 3), 0); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("ReadAt() = %v, want fs.ErrInvalid", err)
		}
	})

	t.Run("disk_mode_does_not_buffer_a_writable_file", func(t *testing.T) {
		f := newTestReadWriteFile("t.txt", "old")
		wrapped, err := ToBufferedFile(f, false, BufferDisk)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		nf := wrapped.(*bufferedFile)
		if nf.tmpFile != nil {
			t.Error("expected nil tmpFile in disk mode for a file open for writing")
		}
		if nf.buf != nil {
			t.Error("expected nil buf in disk mode for a file open for writing")
		}
		if _, err := wrapped.ReadAt(make([]byte, 3), 0); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("ReadAt() = %v, want fs.ErrInvalid", err)
		}
	})

	t.Run("native_seek_kept_when_readat_missing", func(t *testing.T) {
		f := newTestWritableSeekerFile("t.txt", "hello world")
		wrapped, err := ToBufferedFile(f, false, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		nf := wrapped.(*bufferedFile)
		if nf.buf != nil {
			t.Error("expected no buffer when the underlying file provides Seek")
		}
		if _, err := wrapped.Seek(6, io.SeekStart); err != nil {
			t.Fatalf("Seek(6, SeekStart) = %v, want the underlying Seek to be used", err)
		}
		buf := make([]byte, 5)
		if _, err := io.ReadFull(wrapped, buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != "world" {
			t.Errorf("Read after Seek(6) = %q, want %q", buf, "world")
		}
		if _, err := wrapped.ReadAt(buf, 0); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("ReadAt() = %v, want fs.ErrInvalid", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for using a bufferedFile after Close
// ---------------------------------------------------------------------------

// assertClosedMethods checks that every method reports fs.ErrClosed once Close
// has run, including the methods the wrapper polyfilled onto the file.
func assertClosedMethods(tb testing.TB, f types.File) {
	tb.Helper()

	if _, err := f.Read(make([]byte, 4)); !errors.Is(err, fs.ErrClosed) {
		tb.Errorf("Read() = %v, want fs.ErrClosed", err)
	}
	if _, err := f.ReadAt(make([]byte, 4), 0); !errors.Is(err, fs.ErrClosed) {
		tb.Errorf("ReadAt() = %v, want fs.ErrClosed", err)
	}
	if _, err := f.Seek(0, io.SeekStart); !errors.Is(err, fs.ErrClosed) {
		tb.Errorf("Seek() = %v, want fs.ErrClosed", err)
	}
	if _, err := f.Write([]byte("x")); !errors.Is(err, fs.ErrClosed) {
		tb.Errorf("Write() = %v, want fs.ErrClosed", err)
	}
	if _, err := f.WriteString("x"); !errors.Is(err, fs.ErrClosed) {
		tb.Errorf("WriteString() = %v, want fs.ErrClosed", err)
	}
	if _, err := f.Stat(); !errors.Is(err, fs.ErrClosed) {
		tb.Errorf("Stat() = %v, want fs.ErrClosed", err)
	}
}

func TestBufferedFileAfterClose(t *testing.T) {
	t.Run("memory_buffer_unreachable_after_close", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello"), true, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		nf := wrapped.(*bufferedFile)
		if err := wrapped.Close(); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		if nf.buf != nil {
			t.Error("expected nil buffer after Close")
		}
		assertClosedMethods(t, wrapped)

		if err := wrapped.Close(); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("second Close() = %v, want fs.ErrClosed", err)
		}
	})

	t.Run("disk_buffer_unreachable_after_close", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello"), true, BufferDisk)
		if err != nil {
			t.Fatal(err)
		}
		nf := wrapped.(*bufferedFile)
		tmpName := nf.tmpFile.Name()
		if err := wrapped.Close(); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		if nf.tmpFile != nil {
			t.Error("expected nil tmpFile after Close")
		}
		if _, err := osutil.Stat(tmpName); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("temp file %q still exists after Close", tmpName)
		}
		assertClosedMethods(t, wrapped)
	})

	t.Run("native_seek_and_readat_unreachable_after_close", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestSeekerFile("t.txt", "hello world"), true, BufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		nf := wrapped.(*bufferedFile)
		if nf.buf != nil {
			t.Fatal("expected nil buffer when the underlying file provides Seek and ReadAt")
		}
		if err := wrapped.Close(); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		assertClosedMethods(t, wrapped)
	})
}

// ---------------------------------------------------------------------------
// Tests for the buffer polyfill behavior (Seek / ReadAt / Read consistency)
// ---------------------------------------------------------------------------

// testPolyfillBuffering exercises the Seek / ReadAt / Read buffer. Only a file
// that is open for reading is buffered, so every case wraps with readOnly=true.
func testPolyfillBuffering(t *testing.T, mode BufferMode) {
	t.Helper()

	t.Run("multiple_seeks_are_consistent", func(t *testing.T) {
		const content = "abcdefghij"
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, mode)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		for _, tc := range []struct {
			offset int64
			want   string
		}{
			{0, "abcde"},
			{5, "fghij"},
			{3, "defgh"},
			{0, "abcde"},
		} {
			if _, err := wrapped.Seek(tc.offset, io.SeekStart); err != nil {
				t.Fatalf("Seek(%d) = %v", tc.offset, err)
			}
			buf := make([]byte, 5)
			if _, err := io.ReadFull(wrapped, buf); err != nil {
				t.Fatalf("ReadFull after Seek(%d) = %v", tc.offset, err)
			}
			if string(buf) != tc.want {
				t.Errorf("Seek(%d) then Read = %q, want %q", tc.offset, buf, tc.want)
			}
		}
	})

	t.Run("readat_at_various_offsets", func(t *testing.T) {
		const content = "abcdefghij"
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, mode)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		for _, tc := range []struct {
			off  int64
			n    int
			want string
		}{
			{0, 3, "abc"},
			{7, 3, "hij"},
			{4, 4, "efgh"},
		} {
			buf := make([]byte, tc.n)
			n, err := wrapped.ReadAt(buf, tc.off)
			if err != nil || n != tc.n || string(buf) != tc.want {
				t.Errorf("ReadAt(%d, %d) = %q %v; want %q nil", tc.n, tc.off, buf[:n], err, tc.want)
			}
		}
	})

	t.Run("readat_does_not_affect_read_position", func(t *testing.T) {
		const content = "hello world"
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, mode)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		buf := make([]byte, 5)
		n, err := wrapped.ReadAt(buf, 6)
		if err != nil || n != 5 || string(buf) != "world" {
			t.Fatalf("ReadAt(5, 6) = %q %v; want %q nil", buf[:n], err, "world")
		}
		first5 := make([]byte, 5)
		if _, err := io.ReadFull(wrapped, first5); err != nil {
			t.Fatal(err)
		}
		if string(first5) != "hello" {
			t.Errorf("Read after ReadAt = %q, want %q", first5, "hello")
		}
	})

	t.Run("seek_current_and_end", func(t *testing.T) {
		const content = "abcdefghij" // 10 bytes
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, mode)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		if _, err := wrapped.Seek(3, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		pos, err := wrapped.Seek(2, io.SeekCurrent)
		if err != nil {
			t.Fatalf("Seek(2, SeekCurrent) = %v", err)
		}
		if pos != 5 {
			t.Errorf("Seek(+2 from 3) = %d, want 5", pos)
		}
		pos, err = wrapped.Seek(-3, io.SeekEnd)
		if err != nil {
			t.Fatalf("Seek(-3, SeekEnd) = %v", err)
		}
		if pos != 7 {
			t.Errorf("Seek(-3 from end) = %d, want 7", pos)
		}
		buf := make([]byte, 3)
		if _, err := io.ReadFull(wrapped, buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != "hij" {
			t.Errorf("Read after Seek(-3, SeekEnd) = %q, want %q", buf, "hij")
		}
	})
}

func TestBufferedFilePolyfillBuffering(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		t.Run("buffer_created_when_seek_missing", func(t *testing.T) {
			wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello"), true, BufferMemory)
			if err != nil {
				t.Fatal(err)
			}
			defer ufsTesting.ValidateClose(t, wrapped)()

			nf := wrapped.(*bufferedFile)
			if nf.buf == nil {
				t.Error("expected non-nil buf when underlying lacks Seek")
			}
			if nf.tmpFile != nil {
				t.Error("expected nil tmpFile in memory mode")
			}
		})

		testPolyfillBuffering(t, BufferMemory)
	})

	t.Run("disk", func(t *testing.T) {
		t.Run("tmpfile_created_when_seek_missing", func(t *testing.T) {
			wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello"), true, BufferDisk)
			if err != nil {
				t.Fatal(err)
			}
			defer ufsTesting.ValidateClose(t, wrapped)()

			nf := wrapped.(*bufferedFile)
			if nf.tmpFile == nil {
				t.Error("expected non-nil tmpFile when underlying lacks Seek")
			}
			if nf.buf != nil {
				t.Error("expected nil buf in disk mode")
			}
		})

		t.Run("tmpfile_cleaned_up_on_close", func(t *testing.T) {
			wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello"), true, BufferDisk)
			if err != nil {
				t.Fatal(err)
			}
			nf := wrapped.(*bufferedFile)
			tmpName := nf.tmpFile.Name()
			if err := wrapped.Close(); err != nil {
				t.Fatal(err)
			}
			if nf.tmpFile != nil {
				t.Error("expected nil tmpFile after close")
			}
			if _, err := osutil.Stat(tmpName); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("temp file %q still exists after close", tmpName)
			}
		})

		t.Run("close_reports_temp_handle_close_failure", func(t *testing.T) {
			wrapped, err := ToBufferedFile(newTestCloseCountingFile("t.txt", "hello"), true, BufferDisk)
			if err != nil {
				t.Fatal(err)
			}
			nf := wrapped.(*bufferedFile)
			base := nf.File.(*testCloseCountingFile)
			tmpName := nf.tmpFile.Name()
			if err := nf.tmpFile.Close(); err != nil {
				t.Fatal(err)
			}
			// Close stops at the failed handle and leaves the temp file on disk,
			// so the test removes it.
			t.Cleanup(func() {
				if err := osutil.Remove(tmpName); err != nil && !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("remove temp file %q: %v", tmpName, err)
				}
			})

			err = wrapped.Close()
			if !errors.Is(err, fs.ErrClosed) {
				t.Errorf("Close() = %v, want fs.ErrClosed", err)
			}
			if nf.tmpFile != nil {
				t.Error("expected nil tmpFile after a failed close")
			}
			if base.closeCalls != 1 {
				t.Errorf("underlying Close calls = %d, want 1", base.closeCalls)
			}
		})

		t.Run("close_reports_temp_remove_failure", func(t *testing.T) {
			wrapped, err := ToBufferedFile(newTestCloseCountingFile("t.txt", "hello"), true, BufferDisk)
			if err != nil {
				t.Fatal(err)
			}
			nf := wrapped.(*bufferedFile)
			base := nf.File.(*testCloseCountingFile)
			tmpName := nf.tmpFile.Name()
			// Make the removal inside Close fail the way the host OS reports it.
			// Unix unlinks through the open handle, leaving no name to remove,
			// while Windows refuses to delete a name whose file is still open, so
			// a second handle keeps it busy.
			var busy io.Closer
			if runtime.GOOS == "windows" {
				extra, err := osutil.Open(tmpName)
				if err != nil {
					t.Fatal(err)
				}
				busy = extra
			} else {
				if err := osutil.Remove(tmpName); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				if busy != nil {
					if err := busy.Close(); err != nil {
						t.Errorf("close extra handle on %q: %v", tmpName, err)
					}
				}
				if err := osutil.Remove(tmpName); err != nil && !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("remove temp file %q: %v", tmpName, err)
				}
			})

			err = wrapped.Close()
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Fatalf("Close() = %v, want a *fs.PathError from removing %q", err, tmpName)
			}
			if pathErr.Op != "remove" || pathErr.Path != tmpName {
				t.Errorf("Close() reported %s %q, want remove %q", pathErr.Op, pathErr.Path, tmpName)
			}
			if nf.tmpFile != nil {
				t.Error("expected nil tmpFile after a failed close")
			}
			if base.closeCalls != 1 {
				t.Errorf("underlying Close calls = %d, want 1", base.closeCalls)
			}
		})

		testPolyfillBuffering(t, BufferDisk)
	})

	t.Run("no_buffer_when_seek_and_readat_present", func(t *testing.T) {
		f := newTestSeekerFile("t.txt", "hello world")
		wrapped, err := ToBufferedFile(f, false, BufferDisk)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		nf := wrapped.(*bufferedFile)
		if nf.buf != nil {
			t.Error("expected nil buf when underlying provides both Seek and ReadAt")
		}
		if nf.tmpFile != nil {
			t.Error("expected nil tmpFile when underlying provides both Seek and ReadAt")
		}
	})
}
