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

package ufs

import (
	"errors"
	"io"
	"io/fs"
	"sync"
	"testing"
	"time"
)

func newTestBufFile(pathName, content string) *bufFile {
	f := newBufFile(pathName, []byte(content), 0o644, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	return &f
}

func TestBufFileStat(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		f := newTestBufFile("dir/hello.txt", "hello world")
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
		f := newTestBufFile("empty.txt", "")
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
		f := newTestBufFile("x.txt", "abcdef")
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

func TestBufFileRead(t *testing.T) {
	t.Run("sequential_reads_to_eof", func(t *testing.T) {
		f := newTestBufFile("seq.txt", "abcdefghij")

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
		f := newTestBufFile("empty.txt", "")
		n, err := f.Read(make([]byte, 8))
		if n != 0 || err != io.EOF {
			t.Errorf("Read() on empty file = (%d, %v), want (0, io.EOF)", n, err)
		}
	})

	t.Run("zero_length_buffer_not_eof", func(t *testing.T) {
		// A zero-length read on a non-exhausted file must not report EOF: the
		// offset is still short of the content length, it's just that no
		// bytes were requested.
		f := newTestBufFile("x.txt", "abc")
		n, err := f.Read(make([]byte, 0))
		if n != 0 || err != nil {
			t.Errorf("Read(zero-length buf) = (%d, %v), want (0, nil)", n, err)
		}
	})

	t.Run("buffer_larger_than_remaining_content", func(t *testing.T) {
		f := newTestBufFile("x.txt", "abc")
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

func TestBufFileReadAt(t *testing.T) {
	f := newTestBufFile("x.txt", "hello world")

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
		f := newTestBufFile("x.txt", "abcdef")
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
		f := newTestBufFile("empty.txt", "")
		n, err := f.ReadAt(make([]byte, 1), 0)
		if n != 0 || err != io.EOF {
			t.Errorf("ReadAt(0) on empty content = (%d, %v), want (0, io.EOF)", n, err)
		}
	})
}

func TestBufFileSeek(t *testing.T) {
	t.Run("start_current_end", func(t *testing.T) {
		f := newTestBufFile("x.txt", "0123456789")

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
		// affects a later Write (which bufFile itself does not implement).
		f := newTestBufFile("x.txt", "abc")
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
		f := newTestBufFile("x.txt", "abcdef")
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
		f := newTestBufFile("x.txt", "abc")
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
		f := newTestBufFile("x.txt", "abcdef")
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

// TestBufFileConcurrentAccess exercises Read, ReadAt, Seek and Stat from many
// goroutines at once. It exists to be run with the race detector (make test
// runs go test -race): bufFile's mu must serialize all of these.
func TestBufFileConcurrentAccess(_ *testing.T) {
	f := newTestBufFile("concurrent.txt", "the quick brown fox jumps over the lazy dog")

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(4)
		go func() {
			defer wg.Done()
			buf := make([]byte, 4)
			_, _ = f.Read(buf)
		}()
		go func() {
			defer wg.Done()
			buf := make([]byte, 4)
			_, _ = f.ReadAt(buf, 2)
		}()
		go func() {
			defer wg.Done()
			_, _ = f.Seek(1, io.SeekCurrent)
		}()
		go func() {
			defer wg.Done()
			_, _ = f.Stat()
		}()
	}
	wg.Wait()
}
