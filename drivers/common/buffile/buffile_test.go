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

package buffile

import (
	"errors"
	"io"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"
)

func newTestFile(pathName, content string) *File {
	f := New(pathName, []byte(content), 0o644, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	return &f
}

func TestFileStat(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		f := newTestFile("dir/hello.txt", "hello world")
		info, err := f.Stat()
		if err != nil {
			t.Fatalf("Stat() = %v, want nil", err)
		}
		if info.Name() != "hello.txt" {
			t.Errorf("Name() = %q, want %q", info.Name(), "hello.txt")
		}
		if info.Size() != int64(len("hello world")) {
			t.Errorf("Size() = %d, want %d", info.Size(), len("hello world"))
		}
		if info.Mode() != 0o644 {
			t.Errorf("Mode() = %v, want %v", info.Mode(), fs.FileMode(0o644))
		}
		if !info.ModTime().Equal(f.modTime) {
			t.Errorf("ModTime() = %v, want %v", info.ModTime(), f.modTime)
		}
		if info.IsDir() {
			t.Error("IsDir() = true, want false")
		}
	})

	t.Run("empty_content", func(t *testing.T) {
		f := newTestFile("empty.txt", "")
		info, err := f.Stat()
		if err != nil {
			t.Fatalf("Stat() = %v, want nil", err)
		}
		if info.Size() != 0 {
			t.Errorf("Size() = %d, want 0", info.Size())
		}
	})

	t.Run("size_reflects_offset_not_read_position", func(t *testing.T) {
		// Size() must report total content length regardless of the current
		// read offset.
		f := newTestFile("x.txt", "abcdef")
		buf := make([]byte, 3)
		if _, err := f.Read(buf); err != nil {
			t.Fatal(err)
		}
		info, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() != 6 {
			t.Errorf("Size() after partial Read() = %d, want 6", info.Size())
		}
	})
}

func TestFileRead(t *testing.T) {
	t.Run("sequential_reads_to_eof", func(t *testing.T) {
		f := newTestFile("seq.txt", "abcdefghij")

		buf := make([]byte, 4)
		var got []byte
		for {
			n, err := f.Read(buf)
			got = append(got, buf[:n]...)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("Read() = %v", err)
			}
			if n == 0 {
				t.Fatal("Read() returned 0 bytes with nil error, would loop forever")
			}
		}
		if string(got) != "abcdefghij" {
			t.Errorf("accumulated reads = %q, want %q", got, "abcdefghij")
		}

		// Further reads after EOF must keep returning EOF.
		n, err := f.Read(buf)
		if n != 0 || err != io.EOF {
			t.Errorf("Read() after EOF = (%d, %v), want (0, io.EOF)", n, err)
		}
	})

	t.Run("empty_content_immediate_eof", func(t *testing.T) {
		f := newTestFile("empty.txt", "")
		n, err := f.Read(make([]byte, 8))
		if n != 0 || err != io.EOF {
			t.Errorf("Read() on empty file = (%d, %v), want (0, io.EOF)", n, err)
		}
	})

	t.Run("zero_length_buffer_not_eof", func(t *testing.T) {
		// A zero-length read on a non-exhausted file must not report EOF: the
		// offset is still short of the content length, it's just that no
		// bytes were requested.
		f := newTestFile("x.txt", "abc")
		n, err := f.Read(make([]byte, 0))
		if n != 0 || err != nil {
			t.Errorf("Read(zero-length buf) = (%d, %v), want (0, nil)", n, err)
		}
	})

	t.Run("buffer_larger_than_remaining_content", func(t *testing.T) {
		f := newTestFile("x.txt", "abc")
		buf := make([]byte, 10)
		n, err := f.Read(buf)
		if err != nil {
			t.Fatalf("Read() = %v, want nil", err)
		}
		if n != 3 || string(buf[:n]) != "abc" {
			t.Errorf("Read() = (%d, %q), want (3, %q)", n, buf[:n], "abc")
		}
	})
}

func TestFileReadAt(t *testing.T) {
	f := newTestFile("x.txt", "hello world")

	t.Run("from_start", func(t *testing.T) {
		buf := make([]byte, 5)
		n, err := f.ReadAt(buf, 0)
		if err != nil {
			t.Fatalf("ReadAt() = %v, want nil", err)
		}
		if n != 5 || string(buf) != "hello" {
			t.Errorf("ReadAt(0) = (%d, %q), want (5, %q)", n, buf, "hello")
		}
	})

	t.Run("from_middle", func(t *testing.T) {
		// The read ends exactly at content length; per the io.ReaderAt
		// contract this may report either nil or io.EOF, and this
		// implementation's convention (shared with the exact-boundary case
		// below) is to report io.EOF.
		buf := make([]byte, 5)
		n, err := f.ReadAt(buf, 6)
		if err != io.EOF {
			t.Fatalf("ReadAt() err = %v, want io.EOF", err)
		}
		if n != 5 || string(buf) != "world" {
			t.Errorf("ReadAt(6) = (%d, %q), want (5, %q)", n, buf, "world")
		}
	})

	t.Run("partial_at_end_returns_eof", func(t *testing.T) {
		buf := make([]byte, 5)
		n, err := f.ReadAt(buf, 9)
		if err != io.EOF {
			t.Errorf("ReadAt() err = %v, want io.EOF", err)
		}
		if n != 2 || string(buf[:n]) != "ld" {
			t.Errorf("ReadAt(9) = (%d, %q), want (2, %q)", n, buf[:n], "ld")
		}
	})

	t.Run("exact_length_at_content_boundary", func(t *testing.T) {
		buf := make([]byte, len("world"))
		n, err := f.ReadAt(buf, int64(len("hello ")))
		if err != io.EOF {
			t.Errorf("ReadAt() err = %v, want io.EOF (read ends exactly at content length)", err)
		}
		if n != len("world") || string(buf) != "world" {
			t.Errorf("ReadAt() = (%d, %q), want (%d, %q)", n, buf, len("world"), "world")
		}
	})

	t.Run("offset_at_content_length_is_eof", func(t *testing.T) {
		n, err := f.ReadAt(make([]byte, 4), int64(len("hello world")))
		if n != 0 || err != io.EOF {
			t.Errorf("ReadAt(len(content)) = (%d, %v), want (0, io.EOF)", n, err)
		}
	})

	t.Run("offset_beyond_content", func(t *testing.T) {
		n, err := f.ReadAt(make([]byte, 4), 1000)
		if n != 0 || err != io.EOF {
			t.Errorf("ReadAt(1000) = (%d, %v), want (0, io.EOF)", n, err)
		}
	})

	t.Run("negative_offset_errors_without_panicking", func(t *testing.T) {
		n, err := f.ReadAt(make([]byte, 4), -1)
		if n != 0 {
			t.Errorf("ReadAt(-1) n = %d, want 0", n)
		}
		if err == nil {
			t.Fatal("ReadAt(-1) succeeded, want error")
		}
		var pe *fs.PathError
		if !errors.As(err, &pe) {
			t.Fatalf("ReadAt(-1) error type = %T, want *fs.PathError", err)
		}
		if pe.Op != "readat" {
			t.Errorf("ReadAt(-1) PathError.Op = %q, want %q", pe.Op, "readat")
		}
		if !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("ReadAt(-1) error = %v, want it to wrap fs.ErrInvalid", err)
		}
	})

	t.Run("does_not_move_read_offset", func(t *testing.T) {
		// ReadAt must not disturb the Read/Seek cursor.
		f := newTestFile("x.txt", "abcdef")
		if _, err := f.Seek(2, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		if _, err := f.ReadAt(make([]byte, 2), 0); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 1)
		if _, err := f.Read(buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != "c" {
			t.Errorf("Read() after ReadAt() = %q, want %q (cursor should still be at offset 2)", buf, "c")
		}
	})

	t.Run("on_empty_content", func(t *testing.T) {
		f := newTestFile("empty.txt", "")
		n, err := f.ReadAt(make([]byte, 1), 0)
		if n != 0 || err != io.EOF {
			t.Errorf("ReadAt(0) on empty content = (%d, %v), want (0, io.EOF)", n, err)
		}
	})
}

func TestFileSeek(t *testing.T) {
	t.Run("start_current_end", func(t *testing.T) {
		f := newTestFile("x.txt", "0123456789")

		pos, err := f.Seek(3, io.SeekStart)
		if err != nil || pos != 3 {
			t.Fatalf("Seek(3, Start) = (%d, %v), want (3, nil)", pos, err)
		}

		pos, err = f.Seek(2, io.SeekCurrent)
		if err != nil || pos != 5 {
			t.Fatalf("Seek(2, Current) = (%d, %v), want (5, nil)", pos, err)
		}

		pos, err = f.Seek(-4, io.SeekCurrent)
		if err != nil || pos != 1 {
			t.Fatalf("Seek(-4, Current) = (%d, %v), want (1, nil)", pos, err)
		}

		pos, err = f.Seek(0, io.SeekEnd)
		if err != nil || pos != 10 {
			t.Fatalf("Seek(0, End) = (%d, %v), want (10, nil)", pos, err)
		}

		pos, err = f.Seek(-3, io.SeekEnd)
		if err != nil || pos != 7 {
			t.Fatalf("Seek(-3, End) = (%d, %v), want (7, nil)", pos, err)
		}
	})

	t.Run("seek_past_end_is_allowed", func(t *testing.T) {
		// Matches os.File semantics: seeking beyond EOF is legal; it only
		// affects a later Write .
		f := newTestFile("x.txt", "abc")
		pos, err := f.Seek(100, io.SeekStart)
		if err != nil {
			t.Fatalf("Seek(100, Start) = %v, want nil", err)
		}
		if pos != 100 {
			t.Errorf("Seek(100, Start) = %d, want 100", pos)
		}
		n, err := f.Read(make([]byte, 4))
		if n != 0 || err != io.EOF {
			t.Errorf("Read() after seeking past end = (%d, %v), want (0, io.EOF)", n, err)
		}
	})

	t.Run("negative_result_errors", func(t *testing.T) {
		f := newTestFile("x.txt", "abcdef")
		for _, tc := range []struct {
			name   string
			offset int64
			whence int
		}{
			{"start_negative", -1, io.SeekStart},
			{"current_negative", -1, io.SeekCurrent},
			{"end_negative", -100, io.SeekEnd},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if _, err := f.Seek(tc.offset, tc.whence); err == nil {
					t.Error("Seek() succeeded, want error for resulting negative position")
				}
			})
		}
	})

	t.Run("invalid_whence", func(t *testing.T) {
		f := newTestFile("x.txt", "abc")
		_, err := f.Seek(0, 99)
		if err == nil {
			t.Fatal("Seek(0, 99) succeeded, want error")
		}
		var pe *fs.PathError
		if !errors.As(err, &pe) {
			t.Fatalf("Seek() error type = %T, want *fs.PathError", err)
		}
		if pe.Op != "seek" {
			t.Errorf("Seek() PathError.Op = %q, want %q", pe.Op, "seek")
		}
		if pe.Path != "x.txt" {
			t.Errorf("Seek() PathError.Path = %q, want %q", pe.Path, "x.txt")
		}
	})

	t.Run("failed_seek_does_not_move_offset", func(t *testing.T) {
		f := newTestFile("x.txt", "abcdef")
		if _, err := f.Seek(3, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Seek(-100, io.SeekCurrent); err == nil {
			t.Fatal("Seek() succeeded, want error")
		}
		buf := make([]byte, 1)
		if _, err := f.Read(buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != "d" {
			t.Errorf("Read() after failed Seek() = %q, want %q (offset should be unchanged)", buf, "d")
		}
	})
}

// TestFileConcurrentAccess exercises Read, ReadAt, Seek and Stat from many
// goroutines at once. It exists to be run with the race detector (make test
// runs go test -race): File's mu must serialize all of these.
func TestFileConcurrentAccess(t *testing.T) {
	f := newTestFile("concurrent.txt", "the quick brown fox jumps over the lazy dog")

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(4)
		go func() {
			defer wg.Done()
			buf := make([]byte, 4)
			if _, err := f.Read(buf); err != nil && !errors.Is(err, io.EOF) {
				t.Errorf("got error on file read, %s", err)
			}
		}()
		go func() {
			defer wg.Done()
			buf := make([]byte, 4)
			if bytesRead, err := f.ReadAt(buf, 2); err != nil {
				t.Errorf("got error on file read, %s", err)
			} else if bytesRead != len(buf) {
				t.Errorf("read %d bytes, want %d", bytesRead, len(buf))
			}
		}()
		go func() {
			defer wg.Done()
			if newOffset, err := f.Seek(1, io.SeekCurrent); err != nil {
				t.Errorf("got error on file read, %s", err)
			} else if newOffset <= 0 {
				t.Errorf("offset mismatch, got %d, want > 0", newOffset)
			}
		}()
		go func() {
			defer wg.Done()

			if stat, err := f.Stat(); err != nil {
				t.Errorf("got error on file read, %s", err)
			} else if stat == nil {
				t.Error("stat is nil")
			}
		}()
	}
	wg.Wait()
}

func TestFileWrite(t *testing.T) {
	modTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	testCases := []struct {
		name    string
		initial string
		seek    int64
		write   string
		want    string
	}{
		{name: "append_to_empty", initial: "", seek: 0, write: "abc", want: "abc"},
		{name: "overwrite_in_place", initial: "hello world", seek: 0, write: "HI", want: "HIllo world"},
		{name: "overwrite_and_extend", initial: "hello", seek: 3, write: "LOOO", want: "helLOOO"},
		{name: "zero_fill_gap", initial: "abc", seek: 5, write: "XY", want: "abc\x00\x00XY"},
		{name: "empty_write_past_end_does_not_grow", initial: "abc", seek: 10, write: "", want: "abc"},
	}
	for _, tc := range testCases {
		for _, useString := range []bool{false, true} {
			name := tc.name + "_bytes"
			if useString {
				name = tc.name + "_string"
			}
			t.Run(name, func(t *testing.T) {
				f := newTestFile("w.txt", tc.initial)
				if _, err := f.Seek(tc.seek, io.SeekStart); err != nil {
					t.Fatal(err)
				}
				var n int
				var err error
				if useString {
					n, err = f.WriteString(tc.write)
				} else {
					n, err = f.Write([]byte(tc.write))
				}
				if err != nil || n != len(tc.write) {
					t.Fatalf("write = (%d, %v), want (%d, nil)", n, err, len(tc.write))
				}
				if pos, err := f.Seek(0, io.SeekCurrent); err != nil || pos != tc.seek+int64(len(tc.write)) {
					t.Errorf("Seek(0, SeekCurrent) after write = (%d, %v), want (%d, nil)", pos, err, tc.seek+int64(len(tc.write)))
				}
				if got := readAllAt(t, f); got != tc.want {
					t.Errorf("content = %q, want %q", got, tc.want)
				}
				// An empty write changes nothing, so it must not force a commit.
				if _, _, ok := f.TakeDirty(modTime); ok != (tc.write != "") {
					t.Errorf("TakeDirty() ok = %t, want %t", ok, tc.write != "")
				}
			})
		}
	}
}

func TestFileTakeDirty(t *testing.T) {
	f := newTestFile("d.txt", "abc")
	if _, _, ok := f.TakeDirty(time.Now()); ok {
		t.Error("TakeDirty() on unmodified file ok = true, want false")
	}

	if _, err := f.WriteString("X"); err != nil {
		t.Fatal(err)
	}
	commitTime := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	content, mode, ok := f.TakeDirty(commitTime)
	if !ok {
		t.Fatal("TakeDirty() after write ok = false, want true")
	}
	if string(content) != "Xbc" {
		t.Errorf("content = %q, want %q", content, "Xbc")
	}
	if mode != 0o644 {
		t.Errorf("mode = %v, want %v", mode, fs.FileMode(0o644))
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(commitTime) {
		t.Errorf("ModTime() = %v, want %v", info.ModTime(), commitTime)
	}

	// The returned content must be a copy that later writes do not mutate.
	if _, err := f.WriteString("Y"); err != nil {
		t.Fatal(err)
	}
	if string(content) != "Xbc" {
		t.Errorf("content aliased the live buffer: got %q after a later write", content)
	}

	if _, _, ok := f.TakeDirty(time.Now()); !ok {
		t.Error("TakeDirty() after second write ok = false, want true")
	}
	if _, _, ok := f.TakeDirty(time.Now()); ok {
		t.Error("TakeDirty() twice without a write ok = true, want false")
	}

	f.MarkDirty()
	if _, _, ok := f.TakeDirty(time.Now()); !ok {
		t.Error("TakeDirty() after MarkDirty() ok = false, want true")
	}
}

func TestFilePath(t *testing.T) {
	f := newTestFile("a/b/c.txt", "")
	if got := f.Path(); got != "a/b/c.txt" {
		t.Errorf("Path() = %q, want %q", got, "a/b/c.txt")
	}
}

// readAllAt returns the file's whole content via ReadAt, leaving the offset
// untouched.
func readAllAt(t *testing.T, f *File) string {
	t.Helper()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, info.Size())
	if n, err := f.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) || n != len(buf) {
		t.Fatalf("ReadAt() = (%d, %v), want (%d, nil or EOF)", n, err, len(buf))
	}
	return string(buf)
}

// TestFileIOTestReader runs the standard library's reader conformance checks
// (Read, ReadAt and Seek) against File, both freshly opened and after writes
// that grow the content.
func TestFileIOTestReader(t *testing.T) {
	large := strings.Repeat("0123456789abcdef", 1<<12)
	testCases := []struct {
		name string
		make func(t *testing.T) *File
		want string
	}{
		{name: "empty", make: func(*testing.T) *File { return newTestFile("e.txt", "") }, want: ""},
		{name: "opened", make: func(*testing.T) *File { return newTestFile("o.txt", "hello world") }, want: "hello world"},
		{name: "opened_large", make: func(*testing.T) *File { return newTestFile("l.txt", large) }, want: large},
		{
			name: "after_writes",
			make: func(t *testing.T) *File {
				f := newTestFile("w.txt", "hello world")
				for _, step := range []struct {
					off int64
					s   string
				}{{0, "HI"}, {11, "!"}, {14, "tail"}} {
					if _, err := f.Seek(step.off, io.SeekStart); err != nil {
						t.Fatal(err)
					}
					if _, err := f.WriteString(step.s); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					t.Fatal(err)
				}
				return f
			},
			want: "HIllo world!\x00\x00tail",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := iotest.TestReader(tc.make(t), []byte(tc.want)); err != nil {
				t.Error(err)
			}
		})
	}
}

// BenchmarkFileWrite measures growing a file through sequential and sparse
// (seek past the end, zero-filled gap) writes.
func BenchmarkFileWrite(b *testing.B) {
	for _, bc := range []struct {
		name   string
		chunk  int
		total  int
		stride int64
	}{
		{name: "sequential_4KiB_to_1MiB", chunk: 4 << 10, total: 1 << 20},
		{name: "sequential_64KiB_to_16MiB", chunk: 64 << 10, total: 16 << 20},
		{name: "sparse_4KiB_every_8KiB", chunk: 4 << 10, total: 1 << 20, stride: 8 << 10},
	} {
		b.Run(bc.name, func(b *testing.B) {
			data := make([]byte, bc.chunk)
			b.ReportAllocs()
			for b.Loop() {
				f := New("bench.bin", nil, 0o644, time.Time{})
				for i := 0; i*bc.chunk < bc.total; i++ {
					if bc.stride > 0 {
						if _, err := f.Seek(int64(i)*bc.stride, io.SeekStart); err != nil {
							b.Fatal(err)
						}
					}
					if _, err := f.Write(data); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
