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
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"

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

// testWritableSeekerFile implements fs.File + io.Writer + io.Seeker with no
// ReadAt.
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

// testSeekerOnlyFile implements fs.File + io.Seeker but no io.ReaderAt.
type testSeekerOnlyFile struct {
	r    *bytes.Reader
	name string
}

func newTestSeekerOnlyFile(name, content string) *testSeekerOnlyFile {
	return &testSeekerOnlyFile{r: bytes.NewReader([]byte(content)), name: name}
}

func (f *testSeekerOnlyFile) Stat() (fs.FileInfo, error) {
	return fakeInfo{name: f.name, size: f.r.Size()}, nil
}
func (f *testSeekerOnlyFile) Read(p []byte) (int, error)                { return f.r.Read(p) }
func (f *testSeekerOnlyFile) Close() error                              { return nil }
func (f *testSeekerOnlyFile) Seek(off int64, whence int) (int64, error) { return f.r.Seek(off, whence) }

// testReaderAtFile implements fs.File + io.ReaderAt but no io.Seeker.
type testReaderAtFile struct {
	r    *bytes.Reader
	name string
}

func newTestReaderAtFile(name, content string) *testReaderAtFile {
	return &testReaderAtFile{r: bytes.NewReader([]byte(content)), name: name}
}

func (f *testReaderAtFile) Stat() (fs.FileInfo, error) {
	return fakeInfo{name: f.name, size: f.r.Size()}, nil
}
func (f *testReaderAtFile) Read(p []byte) (int, error)              { return f.r.Read(p) }
func (f *testReaderAtFile) Close() error                            { return nil }
func (f *testReaderAtFile) ReadAt(p []byte, off int64) (int, error) { return f.r.ReadAt(p, off) }

// testReaderAtNoStatFile has a ReadAt but no length to track a position against,
// so the wrapper must fall back to copying its content.
type testReaderAtNoStatFile struct {
	*testReaderAtFile
}

func newTestReaderAtNoStatFile(name, content string) *testReaderAtNoStatFile {
	return &testReaderAtNoStatFile{testReaderAtFile: newTestReaderAtFile(name, content)}
}

func (f *testReaderAtNoStatFile) Stat() (fs.FileInfo, error) {
	return nil, fs.ErrInvalid
}

// ---------------------------------------------------------------------------
// Assertions about the store a wrapped file reads through
// ---------------------------------------------------------------------------

// wantNoContent checks that the wrapper kept using the underlying file rather
// than installing a buffer.
func wantNoContent(tb testing.TB, nf *bufferedFile) {
	tb.Helper()
	if nf.content != nil {
		tb.Errorf("content = %T, want none", nf.content)
	}
}

// wantMemoryContent checks that the content was copied into the in-memory
// buffer.
func wantMemoryContent(tb testing.TB, nf *bufferedFile) {
	tb.Helper()
	if _, ok := nf.content.(*memoryBuffer); !ok {
		tb.Errorf("content = %T, want the in-memory buffer", nf.content)
	}
}

// wantDiskContent checks that the content was copied into a temporary file,
// returning the buffer so a test can reach the file's name.
func wantDiskContent(tb testing.TB, nf *bufferedFile) *diskBuffer {
	tb.Helper()
	buf, ok := nf.content.(*diskBuffer)
	if !ok {
		tb.Fatalf("content = %T, want the temporary file buffer", nf.content)
	}
	return buf
}

// wantSeekViewContent checks that the wrapper reads through the underlying
// file's ReadAt without copying anything.
func wantSeekViewContent(tb testing.TB, nf *bufferedFile) {
	tb.Helper()
	if _, ok := nf.content.(*sectionBuffer); !ok {
		tb.Errorf("content = %T, want a view onto the underlying ReadAt", nf.content)
	}
}

// assertReadAt reads len(want) bytes at off and checks they equal want. A read
// ending at the end of the content may report nil or io.EOF, and the two stores
// disagree, so atEnd cases accept either.
func assertReadAt(tb testing.TB, f types.File, off int64, want string, atEnd bool) {
	tb.Helper()

	buf := make([]byte, len(want))
	n, err := f.ReadAt(buf, off)
	if n != len(buf) || string(buf) != want {
		tb.Errorf("ReadAt(%d, %d) = %q %v; want %q", len(buf), off, buf[:n], err, want)
		return
	}
	if err != nil && (!atEnd || !errors.Is(err, io.EOF)) {
		tb.Errorf("ReadAt(%d, %d) err = %v, want nil or io.EOF", len(buf), off, err)
	}
}

// ---------------------------------------------------------------------------
// Tests for ToBufferedFile with readOnly=true
// ---------------------------------------------------------------------------

func TestToBufferedFileReadOnly(t *testing.T) {
	t.Run("fast_path_when_already_satisfies_File", func(t *testing.T) {
		base := &fakeHandle{store: newStore(), name: "t.txt"}
		got, err := ToBufferedFile(base, true, BufferArgs{Mode: BufferMemory})
		if err != nil {
			t.Fatal(err)
		}
		if got != base {
			t.Error("expected same value; file already satisfies types.File so no wrapper should be created")
		}
	})

	t.Run("read_through_buffer", func(t *testing.T) {
		const content = "hello world"
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, BufferArgs{Mode: BufferMemory})
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
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, BufferArgs{Mode: BufferMemory})
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
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, BufferArgs{Mode: BufferMemory})
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
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, BufferArgs{Mode: BufferMemory})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		assertReadAt(t, wrapped, 6, "world", true)
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
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "content"), true, BufferArgs{Mode: BufferMemory})
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
		wrapped, err := ToBufferedFile(f, true, BufferArgs{Mode: BufferMemory})
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
		wrapped, err := ToBufferedFile(newTestBareFile("myfile.txt", "hello"), true, BufferArgs{Mode: BufferMemory})
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
		got, err := ToBufferedFile(newTestBareFile("t.txt", "content"), true, BufferArgs{Mode: BufferMemory})
		if err != nil {
			t.Fatal(err)
		}
		nf := got.(*bufferedFile)
		wantMemoryContent(t, nf)
		if err := got.Close(); err != nil {
			t.Fatal(err)
		}
		if nf.content != nil {
			t.Errorf("content = %T, want none after Close", nf.content)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for ToBufferedFile with readOnly=false
// ---------------------------------------------------------------------------

func TestToBufferedFileWritable(t *testing.T) {
	t.Run("fast_path_when_already_satisfies_File", func(t *testing.T) {
		base := &fakeHandle{store: newStore(), name: "t.txt"}
		got, err := ToBufferedFile(base, false, BufferArgs{Mode: BufferMemory})
		if err != nil {
			t.Fatal(err)
		}
		if got != base {
			t.Error("expected same value; file already satisfies types.File so no wrapper should be created")
		}
	})

	t.Run("no_buffer_when_seek_and_readat_present", func(t *testing.T) {
		f := newTestSeekerFile("t.txt", "hello world")
		wrapped, err := ToBufferedFile(f, false, BufferArgs{Mode: BufferMemory})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		wantNoContent(t, wrapped.(*bufferedFile))
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
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "content"), false, BufferArgs{Mode: BufferMemory})
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
		wrapped, err := ToBufferedFile(f, false, BufferArgs{Mode: BufferMemory})
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
		wrapped, err := ToBufferedFile(f, false, BufferArgs{Mode: BufferMemory})
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
		wrapped, err := ToBufferedFile(f, false, BufferArgs{Mode: BufferMemory})
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
			got, err := ToBufferedFile(base, readOnly, BufferArgs{Mode: BufferMemory})
			if err != nil {
				t.Fatal(err)
			}
			if got != base {
				t.Errorf("readOnly=%t: expected same value; file already satisfies types.File", readOnly)
			}
		}
	})

	t.Run("readonly_write_errors", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "content"), true, BufferArgs{Mode: BufferMemory})
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
		wrapped, err := ToBufferedFile(f, false, BufferArgs{Mode: BufferMemory})
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
// Such a file must not get a second copy of its content, so a missing method is
// either derived or refused. See https://github.com/cloudfra/ufs/issues/451.
// ---------------------------------------------------------------------------

func TestToBufferedFileWritableNoBuffer(t *testing.T) {
	t.Run("read_and_write_agree_without_a_buffer", func(t *testing.T) {
		f := newTestReadWriteFile("t.txt", "old")
		wrapped, err := ToBufferedFile(f, false, BufferArgs{Mode: BufferMemory})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		wantNoContent(t, wrapped.(*bufferedFile))
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
		wrapped, err := ToBufferedFile(f, false, BufferArgs{Mode: BufferMemory})
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
		wrapped, err := ToBufferedFile(f, false, BufferArgs{Mode: BufferDisk})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		wantNoContent(t, wrapped.(*bufferedFile))
		if _, err := wrapped.ReadAt(make([]byte, 3), 0); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("ReadAt() = %v, want fs.ErrInvalid", err)
		}
	})

	t.Run("readat_derived_from_a_writable_file_seek", func(t *testing.T) {
		f := newTestWritableSeekerFile("t.txt", "hello world")
		wrapped, err := ToBufferedFile(f, false, BufferArgs{Mode: BufferMemory})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		wantNoContent(t, wrapped.(*bufferedFile))
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
		// Deriving ReadAt from the file's own Seek reads the live file, so it
		// cannot report content that predates the writes made through the
		// wrapper, which is what a copy of the content would do.
		assertReadAt(t, wrapped, 0, "hello", false)
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
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello"), true, BufferArgs{Mode: BufferMemory})
		if err != nil {
			t.Fatal(err)
		}
		nf := wrapped.(*bufferedFile)
		if err := wrapped.Close(); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		if nf.content != nil {
			t.Error("expected nil buffer after Close")
		}
		assertClosedMethods(t, wrapped)

		if err := wrapped.Close(); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("second Close() = %v, want fs.ErrClosed", err)
		}
	})

	t.Run("disk_buffer_unreachable_after_close", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello"), true, BufferArgs{Mode: BufferDisk})
		if err != nil {
			t.Fatal(err)
		}
		nf := wrapped.(*bufferedFile)
		tmpName := wantDiskContent(t, nf).f.Name()
		if err := wrapped.Close(); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		if nf.content != nil {
			t.Error("expected nil buffer after Close")
		}
		if _, err := osutil.Stat(tmpName); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("temp file %q still exists after Close", tmpName)
		}
		assertClosedMethods(t, wrapped)
	})

	t.Run("native_seek_and_readat_unreachable_after_close", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestSeekerFile("t.txt", "hello world"), true, BufferArgs{Mode: BufferMemory})
		if err != nil {
			t.Fatal(err)
		}
		wantNoContent(t, wrapped.(*bufferedFile))
		if err := wrapped.Close(); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		assertClosedMethods(t, wrapped)
	})
}

// testReadErrorFile cannot be read at all, the way a driver refuses a file its
// credentials do not allow reading.
type testReadErrorFile struct {
	*testCloseCountingFile
}

func newTestReadErrorFile(name string) *testReadErrorFile {
	return &testReadErrorFile{testCloseCountingFile: newTestCloseCountingFile(name, "")}
}

func (f *testReadErrorFile) Read([]byte) (int, error) {
	return 0, fs.ErrPermission
}

// ---------------------------------------------------------------------------
// Tests for deriving a missing method from the one the file provides
//
// A file that already has Seek or ReadAt gets the other derived without a copy.
// ---------------------------------------------------------------------------

func TestBufferedFileDerivedMethods(t *testing.T) {
	t.Run("readat_derived_from_a_read_only_seek", func(t *testing.T) {
		const content = "hello world"
		f := newTestSeekerOnlyFile("t.txt", content)
		wrapped, err := ToBufferedFile(f, true, BufferArgs{Mode: BufferDisk})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		wantNoContent(t, wrapped.(*bufferedFile))
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
		assertReadAt(t, wrapped, 0, "hello", false)
		// The derived ReadAt restores the position the file had before it.
		pos, err := wrapped.Seek(0, io.SeekCurrent)
		if err != nil {
			t.Fatal(err)
		}
		if pos != int64(len(content)) {
			t.Errorf("position after ReadAt = %d, want %d", pos, len(content))
		}
	})

	t.Run("seek_derived_from_readat_without_a_copy", func(t *testing.T) {
		const content = "hello world"
		f := newTestReaderAtFile("t.txt", content)
		wrapped, err := ToBufferedFile(f, true, BufferArgs{Mode: BufferDisk})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		wantSeekViewContent(t, wrapped.(*bufferedFile))
		for _, tc := range []struct {
			offset int64
			want   string
		}{
			{6, "world"},
			{0, "hello"},
			{3, "lo wo"},
		} {
			if _, err := wrapped.Seek(tc.offset, io.SeekStart); err != nil {
				t.Fatalf("Seek(%d, SeekStart) = %v", tc.offset, err)
			}
			buf := make([]byte, 5)
			if _, err := io.ReadFull(wrapped, buf); err != nil {
				t.Fatalf("ReadFull after Seek(%d) = %v", tc.offset, err)
			}
			if string(buf) != tc.want {
				t.Errorf("Seek(%d) then Read = %q, want %q", tc.offset, buf, tc.want)
			}
		}
		assertReadAt(t, wrapped, 6, "world", true)
		// ReadAt leaves the position the reads reached on their own: the view is
		// at 3+5 from the last loop case, and a read at an offset does not move it.
		pos, err := wrapped.Seek(0, io.SeekCurrent)
		if err != nil {
			t.Fatal(err)
		}
		if pos != 8 {
			t.Errorf("position after ReadAt = %d, want 8", pos)
		}
	})

	t.Run("readat_only_with_a_failing_stat_falls_back_to_a_copy", func(t *testing.T) {
		// Without a length there is nothing to track a position against, so the
		// only way to give this file a Seek is a copy of its content.
		wrapped, err := ToBufferedFile(newTestReaderAtNoStatFile("t.txt", "hello"), true,
			BufferArgs{Mode: BufferMemory})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		wantMemoryContent(t, wrapped.(*bufferedFile))
		if _, err := wrapped.Seek(1, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(wrapped, buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != "ello" {
			t.Errorf("Read after Seek(1) = %q, want %q", buf, "ello")
		}
	})

	for _, mode := range []BufferMode{BufferMemory, BufferDisk, BufferAuto} {
		// Deriving a method must not copy content even in the modes that would
		// otherwise put it on disk, which is what keeps a large stream-only file
		// from being read twice.
		t.Run("derived_methods_ignore_the_buffer_store", func(t *testing.T) {
			for _, f := range []fs.File{
				newTestSeekerOnlyFile("t.txt", "hello world"),
				newTestReaderAtFile("t.txt", "hello world"),
			} {
				wrapped, err := ToBufferedFile(f, true, BufferArgs{Mode: mode, AutoThreshold: 1})
				if err != nil {
					t.Fatal(err)
				}
				buf := make([]byte, 5)
				if _, err := wrapped.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
					t.Errorf("ReadAt() = %v, want nil or io.EOF", err)
				}
				if _, err := wrapped.Seek(0, io.SeekStart); err != nil {
					t.Errorf("Seek(0, SeekStart) = %v", err)
				}
				ufsTesting.ValidateClose(t, wrapped)()
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests for a buffer that cannot be filled
//
// The call fails and the file plus any partial temporary file are released.
// ---------------------------------------------------------------------------

func TestBufferedFileBufferFailure(t *testing.T) {
	for _, mode := range []BufferMode{BufferMemory, BufferDisk, BufferAuto} {
		t.Run(bufferModeName(mode)+"_read_failure_closes_the_file", func(t *testing.T) {
			f := newTestReadErrorFile("t.txt")
			wrapped, err := ToBufferedFile(f, true, BufferArgs{Mode: mode, AutoThreshold: 1 << 20})
			if !errors.Is(err, fs.ErrPermission) {
				t.Fatalf("ToBufferedFile() = %v, want fs.ErrPermission", err)
			}
			if wrapped != nil {
				t.Errorf("ToBufferedFile() returned %v, want nil", wrapped)
			}
			if f.closeCalls != 1 {
				t.Errorf("underlying Close calls = %d, want 1", f.closeCalls)
			}
		})
	}

	t.Run("auto_without_a_length_chooses_the_temporary_file", func(t *testing.T) {
		f := newTestReadErrorNoStatFile("t.txt")
		wrapped, err := ToBufferedFile(f, true, BufferArgs{Mode: BufferAuto, AutoThreshold: 1 << 20})
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("ToBufferedFile() = %v, want fs.ErrPermission", err)
		}
		if wrapped != nil {
			t.Errorf("ToBufferedFile() returned %v, want nil", wrapped)
		}
		if f.closeCalls != 1 {
			t.Errorf("underlying Close calls = %d, want 1", f.closeCalls)
		}
	})
}

// testReadErrorNoStatFile fails to read and reports no length, so auto mode has
// to pick the temporary file rather than memory.
type testReadErrorNoStatFile struct {
	*testReadErrorFile
}

func newTestReadErrorNoStatFile(name string) *testReadErrorNoStatFile {
	return &testReadErrorNoStatFile{testReadErrorFile: newTestReadErrorFile(name)}
}

func (f *testReadErrorNoStatFile) Stat() (fs.FileInfo, error) {
	return nil, fs.ErrInvalid
}

// TestDiskBufferTempFileFailure checks that the store never leaves a temporary
// file behind. The directory is passed in so the test can inspect it.
func TestDiskBufferTempFileFailure(t *testing.T) {
	t.Run("missing_directory_creates_nothing", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		buf, err := newDiskBuffer(dir, newTestBareFile("t.txt", "hello"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("newDiskBuffer() = %v, want fs.ErrNotExist", err)
		}
		if buf != nil {
			t.Errorf("newDiskBuffer() returned %v, want nil", buf)
		}
	})

	t.Run("failed_copy_removes_the_temporary_file", func(t *testing.T) {
		dir := t.TempDir()
		buf, err := newDiskBuffer(dir, newTestReadErrorFile("t.txt"))
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("newDiskBuffer() = %v, want fs.ErrPermission", err)
		}
		if buf != nil {
			t.Errorf("newDiskBuffer() returned %v, want nil", buf)
		}
		entries, err := osutil.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("temporary files left in %s = %v, want none", dir, entries)
		}
	})

	t.Run("close_removes_the_temporary_file", func(t *testing.T) {
		dir := t.TempDir()
		buf, err := newDiskBuffer(dir, newTestBareFile("t.txt", "hello"))
		if err != nil {
			t.Fatal(err)
		}
		if err := buf.Close(); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		entries, err := osutil.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("temporary files left in %s = %v, want none", dir, entries)
		}
	})
}

// bufferModeName names a mode for a subtest.
func bufferModeName(mode BufferMode) string {
	switch mode {
	case BufferDisk:
		return "disk"
	case BufferAuto:
		return "auto"
	default:
		return "memory"
	}
}

// ---------------------------------------------------------------------------
// Tests for the buffer polyfill behavior (Seek / ReadAt / Read consistency)
// ---------------------------------------------------------------------------

// testPolyfillBuffering exercises the Seek / ReadAt / Read buffer. Only a file
// that is open for reading is buffered, so every case wraps with readOnly=true.
func testPolyfillBuffering(t *testing.T, args BufferArgs) {
	t.Helper()

	t.Run("multiple_seeks_are_consistent", func(t *testing.T) {
		const content = "abcdefghij"
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, args)
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
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, args)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		for _, tc := range []struct {
			off   int64
			want  string
			atEnd bool
		}{
			{0, "abc", false},
			{7, "hij", true},
			{4, "efgh", false},
		} {
			assertReadAt(t, wrapped, tc.off, tc.want, tc.atEnd)
		}
	})

	t.Run("readat_does_not_affect_read_position", func(t *testing.T) {
		const content = "hello world"
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, args)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		assertReadAt(t, wrapped, 6, "world", true)
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
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", content), true, args)
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
		t.Run("memory_buffer_created_when_seek_missing", func(t *testing.T) {
			wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello"), true, BufferArgs{Mode: BufferMemory})
			if err != nil {
				t.Fatal(err)
			}
			defer ufsTesting.ValidateClose(t, wrapped)()

			wantMemoryContent(t, wrapped.(*bufferedFile))
		})

		testPolyfillBuffering(t, BufferArgs{Mode: BufferMemory})
	})

	t.Run("disk", func(t *testing.T) {
		t.Run("temp_file_created_when_seek_missing", func(t *testing.T) {
			wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello"), true, BufferArgs{Mode: BufferDisk})
			if err != nil {
				t.Fatal(err)
			}
			defer ufsTesting.ValidateClose(t, wrapped)()

			wantDiskContent(t, wrapped.(*bufferedFile))
		})

		t.Run("temp_file_cleaned_up_on_close", func(t *testing.T) {
			wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello"), true, BufferArgs{Mode: BufferDisk})
			if err != nil {
				t.Fatal(err)
			}
			nf := wrapped.(*bufferedFile)
			tmpName := wantDiskContent(t, nf).f.Name()
			if err := wrapped.Close(); err != nil {
				t.Fatal(err)
			}
			if nf.content != nil {
				t.Error("expected nil buffer after close")
			}
			if _, err := osutil.Stat(tmpName); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("temp file %q still exists after close", tmpName)
			}
		})

		t.Run("close_reports_temp_handle_close_failure", func(t *testing.T) {
			wrapped, err := ToBufferedFile(newTestCloseCountingFile("t.txt", "hello"), true, BufferArgs{Mode: BufferDisk})
			if err != nil {
				t.Fatal(err)
			}
			nf := wrapped.(*bufferedFile)
			base := nf.File.(*testCloseCountingFile)
			tmpName := wantDiskContent(t, nf).f.Name()
			if err := nf.content.(*diskBuffer).f.Close(); err != nil {
				t.Fatal(err)
			}
			// The removal is still attempted when the handle close fails, so the
			// cleanup here only matters if that removal also fails.
			t.Cleanup(func() {
				if err := osutil.Remove(tmpName); err != nil && !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("remove temp file %q: %v", tmpName, err)
				}
			})

			err = wrapped.Close()
			if !errors.Is(err, fs.ErrClosed) {
				t.Errorf("Close() = %v, want fs.ErrClosed", err)
			}
			if _, err := osutil.Stat(tmpName); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("temp file %q still exists after a failed handle close", tmpName)
			}
			if nf.content != nil {
				t.Error("expected nil buffer after a failed close")
			}
			if base.closeCalls != 1 {
				t.Errorf("underlying Close calls = %d, want 1", base.closeCalls)
			}
		})

		t.Run("close_reports_temp_remove_failure", func(t *testing.T) {
			wrapped, err := ToBufferedFile(newTestCloseCountingFile("t.txt", "hello"), true, BufferArgs{Mode: BufferDisk})
			if err != nil {
				t.Fatal(err)
			}
			nf := wrapped.(*bufferedFile)
			base := nf.File.(*testCloseCountingFile)
			tmpName := wantDiskContent(t, nf).f.Name()
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
			if nf.content != nil {
				t.Error("expected nil buffer after a failed close")
			}
			if base.closeCalls != 1 {
				t.Errorf("underlying Close calls = %d, want 1", base.closeCalls)
			}
		})

		testPolyfillBuffering(t, BufferArgs{Mode: BufferDisk})
	})

	t.Run("no_buffer_when_seek_and_readat_present", func(t *testing.T) {
		f := newTestSeekerFile("t.txt", "hello world")
		wrapped, err := ToBufferedFile(f, false, BufferArgs{Mode: BufferDisk})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		wantNoContent(t, wrapped.(*bufferedFile))
	})
}

// ---------------------------------------------------------------------------
// Tests for BufferAuto
//
// Auto mode picks memory or a temporary file from the reported size, so a large
// file cannot exhaust memory. See https://github.com/cloudfra/ufs/issues/547.
// ---------------------------------------------------------------------------

// testStatErrorFile has no size to report, so auto mode must not assume the
// content is small.
type testStatErrorFile struct {
	*testBareFile
}

func newTestStatErrorFile(name, content string) *testStatErrorFile {
	return &testStatErrorFile{testBareFile: newTestBareFile(name, content)}
}

func (f *testStatErrorFile) Stat() (fs.FileInfo, error) {
	return nil, fs.ErrInvalid
}

// testIrregularSizeFile reports a negative size the way a stream of unknown
// length does.
type testIrregularSizeFile struct {
	*testBareFile
}

func newTestIrregularSizeFile(name, content string) *testIrregularSizeFile {
	return &testIrregularSizeFile{testBareFile: newTestBareFile(name, content)}
}

func (f *testIrregularSizeFile) Stat() (fs.FileInfo, error) {
	return fakeInfo{name: f.name, size: -1}, nil
}

func TestBufferedFileAutoMode(t *testing.T) {
	t.Run("default_threshold_is_one_megabyte", func(t *testing.T) {
		if AutoBufferThreshold != 1<<20 {
			t.Errorf("AutoBufferThreshold = %d, want %d", AutoBufferThreshold, 1<<20)
		}
	})

	t.Run("below_threshold_buffers_in_memory", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello world"), true,
			BufferArgs{Mode: BufferAuto, AutoThreshold: 16})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		wantMemoryContent(t, wrapped.(*bufferedFile))
	})

	t.Run("at_or_above_threshold_buffers_on_disk", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello world 1234"), true,
			BufferArgs{Mode: BufferAuto, AutoThreshold: 16})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		wantDiskContent(t, wrapped.(*bufferedFile))
	})

	// The default threshold is what a caller gets without asking for one, so
	// both sides of it are checked with content of real sizes.
	t.Run("default_threshold_splits_content", func(t *testing.T) {
		small := strings.Repeat("x", int(AutoBufferThreshold)-1)
		wrappedSmall, err := ToBufferedFile(newTestBareFile("small.txt", small), true,
			BufferArgs{Mode: BufferAuto})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrappedSmall)()
		wantMemoryContent(t, wrappedSmall.(*bufferedFile))

		large := strings.Repeat("x", int(AutoBufferThreshold))
		wrappedLarge, err := ToBufferedFile(newTestBareFile("large.txt", large), true,
			BufferArgs{Mode: BufferAuto})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrappedLarge)()
		wantDiskContent(t, wrappedLarge.(*bufferedFile))
		assertReadAt(t, wrappedLarge, AutoBufferThreshold-4, "xxxx", true)
	})

	t.Run("unset_or_negative_threshold_uses_the_default", func(t *testing.T) {
		for _, threshold := range []int64{0, -1} {
			wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello world"), true,
				BufferArgs{Mode: BufferAuto, AutoThreshold: threshold})
			if err != nil {
				t.Fatalf("AutoThreshold=%d: %v", threshold, err)
			}
			defer ufsTesting.ValidateClose(t, wrapped)()

			nf := wrapped.(*bufferedFile)
			if _, ok := nf.content.(*memoryBuffer); !ok {
				t.Errorf("AutoThreshold=%d: expected the default threshold, which puts this content in memory", threshold)
			}
		}
	})

	t.Run("stat_failure_buffers_on_disk", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestStatErrorFile("t.txt", "hello"), true,
			BufferArgs{Mode: BufferAuto, AutoThreshold: 1 << 20})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		wantDiskContent(t, wrapped.(*bufferedFile))
		assertReadAt(t, wrapped, 0, "hello", false)
	})

	t.Run("unknown_size_buffers_on_disk", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestIrregularSizeFile("t.txt", "hello"), true,
			BufferArgs{Mode: BufferAuto, AutoThreshold: 1 << 20})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		wantDiskContent(t, wrapped.(*bufferedFile))
	})

	t.Run("auto_disk_buffer_removed_on_close", func(t *testing.T) {
		wrapped, err := ToBufferedFile(newTestBareFile("t.txt", "hello world 1234"), true,
			BufferArgs{Mode: BufferAuto, AutoThreshold: 1})
		if err != nil {
			t.Fatal(err)
		}
		nf := wrapped.(*bufferedFile)
		tmpName := wantDiskContent(t, nf).f.Name()
		if err := wrapped.Close(); err != nil {
			t.Fatalf("Close() = %v", err)
		}
		if _, err := osutil.Stat(tmpName); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("temp file %q still exists after Close", tmpName)
		}
	})

	t.Run("auto_buffers_neither_a_writable_file_nor_a_seekable_one", func(t *testing.T) {
		wrappedWritable, err := ToBufferedFile(newTestReadWriteFile("t.txt", "old"), false,
			BufferArgs{Mode: BufferAuto, AutoThreshold: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrappedWritable)()

		wantNoContent(t, wrappedWritable.(*bufferedFile))
		if _, err := wrappedWritable.ReadAt(make([]byte, 3), 0); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("ReadAt() = %v, want fs.ErrInvalid", err)
		}

		wrappedSeekable, err := ToBufferedFile(newTestSeekerFile("t.txt", "hello world"), true,
			BufferArgs{Mode: BufferAuto, AutoThreshold: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrappedSeekable)()

		wantNoContent(t, wrappedSeekable.(*bufferedFile))
	})

	// Both auto branches must give the same Seek and ReadAt contract as the
	// explicit modes: a tiny threshold forces the disk branch, while the default
	// keeps the suite's short content in memory.
	t.Run("conformance_through_the_memory_branch", func(t *testing.T) {
		testPolyfillBuffering(t, BufferArgs{Mode: BufferAuto})
	})

	t.Run("conformance_through_the_disk_branch", func(t *testing.T) {
		testPolyfillBuffering(t, BufferArgs{Mode: BufferAuto, AutoThreshold: 1})
	})
}

// TestToBufferedFileIOTestReader runs the standard library's reader suite over
// every kind of file ToBufferedFile returns, the acceptance criterion in
// https://github.com/cloudfra/ufs/issues/547.
func TestToBufferedFileIOTestReader(t *testing.T) {
	content := "the quick brown fox jumps over the lazy dog 0123456789"

	tests := []struct {
		name string
		base func() fs.File
		args BufferArgs
	}{
		{"memory", func() fs.File { return newTestBareFile("t.txt", content) }, BufferArgs{Mode: BufferMemory}},
		{"disk", func() fs.File { return newTestBareFile("t.txt", content) }, BufferArgs{Mode: BufferDisk}},
		{"auto_memory", func() fs.File { return newTestBareFile("t.txt", content) }, BufferArgs{Mode: BufferAuto}},
		{"auto_disk", func() fs.File { return newTestBareFile("t.txt", content) }, BufferArgs{Mode: BufferAuto, AutoThreshold: 1}},
		// A file with no length to buffer cannot be sized against the Auto
		// threshold, so it is copied and the suite still sees random access.
		{"no_stat_copies_to_memory", func() fs.File { return newTestReaderAtNoStatFile("t.txt", content) }, BufferArgs{Mode: BufferMemory}},
		// These two keep reading the underlying file, so the mode must not matter:
		// the missing method is derived from the one the file already has.
		{"seek_only_derives_readat", func() fs.File { return newTestSeekerOnlyFile("t.txt", content) }, BufferArgs{Mode: BufferMemory}},
		{"readat_only_derives_seek", func() fs.File { return newTestReaderAtFile("t.txt", content) }, BufferArgs{Mode: BufferDisk}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped, err := ToBufferedFile(tt.base(), true, tt.args)
			if err != nil {
				t.Fatal(err)
			}
			defer ufsTesting.ValidateClose(t, wrapped)()

			if err := iotest.TestReader(wrapped, []byte(content)); err != nil {
				t.Errorf("iotest.TestReader: %v", err)
			}
		})
	}
}
