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

package polyfill

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"testing"
	"time"

	"github.com/cloudfra/ufs/internal/osutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// ---------------------------------------------------------------------------
// Polyfill test stubs
//
// Each stub implements a different subset of the File interface so tests can
// verify that WrapReadOnlyFSFile and WrapFSFile delegate or fill in exactly
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
	return testFileInfo{name: f.name, size: f.r.Size()}, nil
}
func (f *testBareFile) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *testBareFile) Close() error               { return nil }

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
	return testFileInfo{name: f.name, size: f.r.Size()}, nil
}
func (f *testSeekerFile) Read(p []byte) (int, error)                { return f.r.Read(p) }
func (f *testSeekerFile) Close() error                              { return nil }
func (f *testSeekerFile) Seek(off int64, whence int) (int64, error) { return f.r.Seek(off, whence) }
func (f *testSeekerFile) ReadAt(p []byte, off int64) (int, error)   { return f.r.ReadAt(p, off) }

// ---------------------------------------------------------------------------
// Tests for WrapReadOnlyFSFile
// ---------------------------------------------------------------------------

func TestWrapReadOnlyFSFile(t *testing.T) {
	t.Run("fast_path_when_already_satisfies_File", func(t *testing.T) {
		base := newTestFullFile("test.txt")
		got, err := WrapReadOnlyFSFile(base)
		if err != nil {
			t.Fatal(err)
		}
		if got != File(base) {
			t.Error("expected same value; file already satisfies File so no wrapper should be created")
		}
	})

	t.Run("read_through_buffer", func(t *testing.T) {
		const content = "hello world"
		wrapped, err := WrapReadOnlyFSFile(newTestBareFile("t.txt", content))
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
		wrapped, err := WrapReadOnlyFSFile(newTestBareFile("t.txt", content))
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
		wrapped, err := WrapReadOnlyFSFile(newTestBareFile("t.txt", content))
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
		wrapped, err := WrapReadOnlyFSFile(newTestBareFile("t.txt", content))
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
		wrapped, err := WrapReadOnlyFSFile(newTestBareFile("t.txt", "content"))
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
		wrapped, err := WrapReadOnlyFSFile(f)
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
		wrapped, err := WrapReadOnlyFSFile(newTestBareFile("myfile.txt", "hello"))
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
		got, err := WrapReadOnlyFSFile(newTestBareFile("t.txt", "content"))
		if err != nil {
			t.Fatal(err)
		}
		nf := got.(*wrappedFile)
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
// Tests for WrapFSFile
// ---------------------------------------------------------------------------

func TestWrapFSFile(t *testing.T) {
	t.Run("fast_path_when_already_satisfies_File", func(t *testing.T) {
		base := newTestFullFile("test.txt")
		got, err := WrapFSFile(base)
		if err != nil {
			t.Fatal(err)
		}
		if got != File(base) {
			t.Error("expected same value; file already satisfies File so no wrapper should be created")
		}
	})

	t.Run("no_buffer_when_seek_and_readat_present", func(t *testing.T) {
		f := newTestSeekerFile("t.txt", "hello world")
		wrapped, err := WrapFSFile(f)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		nf := wrapped.(*wrappedFile)
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
		wrapped, err := WrapFSFile(newTestBareFile("t.txt", "content"))
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
		wrapped, err := WrapFSFile(f)
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
		wrapped, err := WrapFSFile(f)
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
		wrapped, err := WrapFSFile(f)
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
// Tests for WrapFile (unified wrapper)
// ---------------------------------------------------------------------------

func TestWrapFile(t *testing.T) {
	t.Run("fast_path_when_already_satisfies_File", func(t *testing.T) {
		base := newTestFullFile("test.txt")
		for _, readOnly := range []bool{true, false} {
			got, err := WrapFile(base, readOnly, BufferMemory)
			if err != nil {
				t.Fatal(err)
			}
			if got != File(base) {
				t.Errorf("readOnly=%t: expected same value; file already satisfies File", readOnly)
			}
		}
	})

	t.Run("readonly_write_errors", func(t *testing.T) {
		wrapped, err := WrapFile(newTestBareFile("t.txt", "content"), true, BufferMemory)
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
		wrapped, err := WrapFile(f, false, BufferMemory)
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
// Tests for the buffer polyfill behavior (Seek / ReadAt / Read consistency)
// ---------------------------------------------------------------------------

func testPolyfillBuffering(t *testing.T, mode BufferMode) {
	t.Helper()

	t.Run("multiple_seeks_are_consistent", func(t *testing.T) {
		const content = "abcdefghij"
		wrapped, err := WrapFile(newTestBareFile("t.txt", content), false, mode)
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
		wrapped, err := WrapFile(newTestBareFile("t.txt", content), false, mode)
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
		wrapped, err := WrapFile(newTestBareFile("t.txt", content), true, mode)
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
		wrapped, err := WrapFile(newTestBareFile("t.txt", content), true, mode)
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

func TestWrapFileBuffering(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		t.Run("buffer_created_when_seek_missing", func(t *testing.T) {
			wrapped, err := WrapFile(newTestBareFile("t.txt", "hello"), false, BufferMemory)
			if err != nil {
				t.Fatal(err)
			}
			defer ufsTesting.ValidateClose(t, wrapped)()

			nf := wrapped.(*wrappedFile)
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
			wrapped, err := WrapFile(newTestBareFile("t.txt", "hello"), false, BufferDisk)
			if err != nil {
				t.Fatal(err)
			}
			defer ufsTesting.ValidateClose(t, wrapped)()

			nf := wrapped.(*wrappedFile)
			if nf.tmpFile == nil {
				t.Error("expected non-nil tmpFile when underlying lacks Seek")
			}
			if nf.buf != nil {
				t.Error("expected nil buf in disk mode")
			}
		})

		t.Run("tmpfile_cleaned_up_on_close", func(t *testing.T) {
			wrapped, err := WrapFile(newTestBareFile("t.txt", "hello"), false, BufferDisk)
			if err != nil {
				t.Fatal(err)
			}
			nf := wrapped.(*wrappedFile)
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

		testPolyfillBuffering(t, BufferDisk)
	})

	t.Run("no_buffer_when_seek_and_readat_present", func(t *testing.T) {
		f := newTestSeekerFile("t.txt", "hello world")
		wrapped, err := WrapFile(f, false, BufferDisk)
		if err != nil {
			t.Fatal(err)
		}
		defer ufsTesting.ValidateClose(t, wrapped)()

		nf := wrapped.(*wrappedFile)
		if nf.buf != nil {
			t.Error("expected nil buf when underlying provides both Seek and ReadAt")
		}
		if nf.tmpFile != nil {
			t.Error("expected nil tmpFile when underlying provides both Seek and ReadAt")
		}
	})
}

// testFileInfo is the fs.FileInfo of the test files above.
type testFileInfo struct {
	name string
	size int64
}

func (i testFileInfo) Name() string       { return i.name }
func (i testFileInfo) Size() int64        { return i.size }
func (i testFileInfo) Mode() fs.FileMode  { return 0 }
func (i testFileInfo) ModTime() time.Time { return time.Time{} }
func (i testFileInfo) IsDir() bool        { return false }
func (i testFileInfo) Sys() any           { return nil }

// testFullFile already satisfies File, so WrapFile returns it unchanged.
type testFullFile struct {
	*testSeekerFile
}

func newTestFullFile(name string) *testFullFile {
	return &testFullFile{testSeekerFile: newTestSeekerFile(name, "")}
}

func (f *testFullFile) Write(p []byte) (int, error)       { return len(p), nil }
func (f *testFullFile) WriteString(s string) (int, error) { return len(s), nil }

// closeCountFile counts how often it is closed.
type closeCountFile struct {
	*testBareFile
	closed int
}

func (f *closeCountFile) Close() error {
	f.closed++
	return nil
}

// TestWrapFileCloseAlwaysClosesWrappedFile verifies that Close closes the
// wrapped file even when the temporary file cannot be removed, and reports
// that failure.
func TestWrapFileCloseAlwaysClosesWrappedFile(t *testing.T) {
	inner := &closeCountFile{testBareFile: newTestBareFile("t.txt", "hello")}
	wrapped, err := WrapFile(inner, true, BufferDisk)
	if err != nil {
		t.Fatalf("WrapFile() = %v, want nil", err)
	}
	if err := osutil.Remove(wrapped.(*wrappedFile).tmpFile.Name()); err != nil {
		t.Fatal(err)
	}
	if err := wrapped.Close(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Close() = %v, want the error from removing the temporary file", err)
	}
	if inner.closed != 1 {
		t.Errorf("wrapped file closed %d times, want 1", inner.closed)
	}
}
