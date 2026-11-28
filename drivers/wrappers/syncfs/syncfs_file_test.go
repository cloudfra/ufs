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

package syncfs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
)

const content = "0123456789abcdefghijklmnopqrstuvwxyz"

// memFile is a handle with a cursor and none of its own guards: Read, Seek and
// Write move pos, and nothing else. It deliberately has no ReadAt or WriteAt, so
// the wrapper has to emulate them, and an unguarded field so the race detector
// reports any call the wrapper lets overlap.
type memFile struct {
	data []byte
	pos  int64
	// refuseSeek makes Seek fail the way a whole-file upload does.
	refuseSeek bool
	closed     bool
}

func (f *memFile) Stat() (fs.FileInfo, error) {
	return types.NewFileInfo("mem", int64(len(f.data)), 0o644, time.Time{}), nil
}

func (f *memFile) Read(p []byte) (int, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	if f.pos >= int64(len(f.data)) {
		return 0, io.EOF
	}
	// One byte at a time, so that a shared cursor that is not guarded is
	// overrun by the second reader rather than finished by the first.
	n := copy(p[:min(len(p), 1)], f.data[f.pos:])
	f.pos += int64(n)
	return n, nil
}

func (f *memFile) Write(p []byte) (int, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	end := f.pos + int64(len(p))
	if end > int64(len(f.data)) {
		f.data = append(f.data, make([]byte, end-int64(len(f.data)))...)
	}
	copy(f.data[f.pos:], p)
	f.pos = end
	return len(p), nil
}

func (f *memFile) Close() error {
	f.closed = true
	return nil
}

// seekFile adds Seek to memFile.
type seekFile struct{ *memFile }

func (f seekFile) Seek(offset int64, whence int) (int64, error) {
	if f.refuseSeek {
		return 0, fs.ErrInvalid
	}
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += f.pos
	case io.SeekEnd:
		offset += int64(len(f.data))
	default:
		return 0, fs.ErrInvalid
	}
	if offset < 0 {
		return 0, fs.ErrInvalid
	}
	f.pos = offset
	return offset, nil
}

func newSeqFile() *File {
	return NewFile(&memFile{data: []byte(content)})
}

func newSeekFile() (*File, *memFile) {
	inner := &memFile{data: []byte(content)}
	return NewFile(seekFile{inner}), inner
}

// TestReadSharedByTwoReaders reads one handle from two goroutines. Between them
// they must read the content exactly once: every byte is handed to one reader.
func TestReadSharedByTwoReaders(t *testing.T) {
	t.Parallel()
	f := newSeqFile()

	var mu sync.Mutex
	counts := map[byte]int{}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			buf := make([]byte, 3)
			for {
				n, err := f.Read(buf)
				mu.Lock()
				for _, b := range buf[:n] {
					counts[b]++
				}
				mu.Unlock()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						t.Errorf("Read() = %v, want nil or io.EOF", err)
					}
					return
				}
			}
		})
	}
	wg.Wait()

	for i := range len(content) {
		if got := counts[content[i]]; got != 1 {
			t.Errorf("byte %q was read %d times, want 1", content[i], got)
		}
	}
	if len(counts) != len(content) {
		t.Errorf("read %d distinct bytes, want %d", len(counts), len(content))
	}
}

// TestReadDirSharedByTwoWalkers walks one directory handle from two goroutines.
// Between them they must see every entry exactly once.
func TestReadDirSharedByTwoWalkers(t *testing.T) {
	t.Parallel()
	mapFS := fstest.MapFS{}
	want := make([]string, 0, 200)
	for i := range 200 {
		name := fmt.Sprintf("e%03d", i)
		mapFS["dir/"+name] = &fstest.MapFile{}
		want = append(want, name)
	}
	dir, err := mapFS.Open("dir")
	if err != nil {
		t.Fatalf("Open(dir) = %v, want nil", err)
	}
	f := NewFile(dir)
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	}()

	var mu sync.Mutex
	var got []string
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for {
				entries, err := f.ReadDir(1)
				mu.Lock()
				for _, e := range entries {
					got = append(got, e.Name())
				}
				mu.Unlock()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						t.Errorf("ReadDir(1) = %v, want nil or io.EOF", err)
					}
					return
				}
			}
		})
	}
	wg.Wait()

	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("two walkers saw %d entries %v, want each of %d once", len(got), got, len(want))
	}
}

// TestReadDirOfFile checks that a handle that is not a directory refuses to
// be listed.
func TestReadDirOfFile(t *testing.T) {
	t.Parallel()
	if _, err := newSeqFile().ReadDir(-1); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("ReadDir(-1) of a file = %v, want an error matching fs.ErrInvalid", err)
	}
}

// TestReadAtEmulatedBySeek reads at offsets of a handle that can only seek, in
// parallel with each other and with reads from the cursor. Each ReadAt must
// return the bytes at its offset, and the cursor must not move under Read.
func TestReadAtEmulatedBySeek(t *testing.T) {
	t.Parallel()
	f, _ := newSeekFile()

	var wg sync.WaitGroup
	for off := range len(content) {
		wg.Go(func() {
			buf := make([]byte, 4)
			n, err := f.ReadAt(buf, int64(off))
			want := content[off:min(off+4, len(content))]
			if string(buf[:n]) != want {
				t.Errorf("ReadAt(4, %d) = %q, want %q", off, buf[:n], want)
			}
			if n < 4 && !errors.Is(err, io.EOF) {
				t.Errorf("ReadAt(4, %d) short read error = %v, want io.EOF", off, err)
			}
			if n == 4 && err != nil {
				t.Errorf("ReadAt(4, %d) error = %v, want nil", off, err)
			}
		})
	}
	var read []byte
	wg.Go(func() {
		buf := make([]byte, 5)
		for {
			n, err := f.Read(buf)
			read = append(read, buf[:n]...)
			if err != nil {
				return
			}
		}
	})
	wg.Wait()

	if string(read) != content {
		t.Errorf("Read from the cursor alongside ReadAt = %q, want %q", read, content)
	}
}

// TestReadAtWithoutSeek checks that a handle that can neither seek nor read at
// an offset is read at its cursor and refuses any other offset.
func TestReadAtWithoutSeek(t *testing.T) {
	t.Parallel()
	f := newSeqFile()

	buf := make([]byte, 10)
	if n, err := f.ReadAt(buf, 0); err != nil || string(buf[:n]) != content[:10] {
		t.Fatalf("ReadAt(10, 0) = %q, %v, want %q, nil", buf[:n], err, content[:10])
	}
	if n, err := f.ReadAt(buf, 10); err != nil || string(buf[:n]) != content[10:20] {
		t.Fatalf("ReadAt(10, 10) = %q, %v, want %q, nil", buf[:n], err, content[10:20])
	}
	if _, err := f.ReadAt(buf, 0); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("ReadAt(10, 0) behind the cursor = %v, want an error matching errors.ErrUnsupported", err)
	}
	// io.ReadFull fills the buffer from a handle that hands back one byte per
	// Read, so the last call is short only because the content ended.
	big := make([]byte, 100)
	n, err := f.ReadAt(big, 20)
	if string(big[:n]) != content[20:] || !errors.Is(err, io.EOF) {
		t.Errorf("ReadAt(100, 20) = %q, %v, want %q, io.EOF", big[:n], err, content[20:])
	}
}

// TestReadAtNative checks that a handle with its own ReadAt is read through it.
func TestReadAtNative(t *testing.T) {
	t.Parallel()
	mapFS := fstest.MapFS{"f": &fstest.MapFile{Data: []byte(content)}}
	inner, err := mapFS.Open("f")
	if err != nil {
		t.Fatalf("Open(f) = %v, want nil", err)
	}
	f := NewFile(inner)

	var wg sync.WaitGroup
	for off := range len(content) - 1 {
		wg.Go(func() {
			buf := make([]byte, 2)
			if n, err := f.ReadAt(buf, int64(off)); err != nil || string(buf[:n]) != content[off:off+2] {
				t.Errorf("ReadAt(2, %d) = %q, %v, want %q, nil", off, buf[:n], err, content[off:off+2])
			}
		})
	}
	wg.Wait()
	if err := f.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// TestWriteAtEmulatedBySeek writes at offsets of a handle that can only seek
// and checks each lands where it was aimed while the cursor stays put.
func TestWriteAtEmulatedBySeek(t *testing.T) {
	t.Parallel()
	f, inner := newSeekFile()
	inner.data = make([]byte, len(content))

	var wg sync.WaitGroup
	for off := range len(content) {
		wg.Go(func() {
			if n, err := f.WriteAt([]byte{content[off]}, int64(off)); n != 1 || err != nil {
				t.Errorf("WriteAt(%q, %d) = %d, %v, want 1, nil", content[off], off, n, err)
			}
		})
	}
	wg.Wait()

	if got := string(inner.data); got != content {
		t.Errorf("content after parallel WriteAt = %q, want %q", got, content)
	}
	if pos, err := f.Seek(0, io.SeekCurrent); pos != 0 || err != nil {
		t.Errorf("Seek(0, SeekCurrent) after WriteAt = %d, %v, want 0, nil", pos, err)
	}
}

// TestWriteAtInOrder checks a handle that refuses to seek, as a whole-file
// upload does: a write at its cursor is taken and any other offset is refused
// rather than written in the wrong place.
func TestWriteAtInOrder(t *testing.T) {
	t.Parallel()
	inner := &memFile{refuseSeek: true}
	f := NewFile(seekFile{inner})

	if n, err := f.WriteAt([]byte("abc"), 0); n != 3 || err != nil {
		t.Fatalf("WriteAt(abc, 0) = %d, %v, want 3, nil", n, err)
	}
	if n, err := f.WriteAt([]byte("def"), 3); n != 3 || err != nil {
		t.Fatalf("WriteAt(def, 3) = %d, %v, want 3, nil", n, err)
	}
	if _, err := f.WriteAt([]byte("x"), 1); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("WriteAt(x, 1) behind the cursor = %v, want an error matching errors.ErrUnsupported", err)
	}
	if _, err := f.WriteAt([]byte("x"), 9); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("WriteAt(x, 9) past the cursor = %v, want an error matching errors.ErrUnsupported", err)
	}
	if got := string(inner.data); got != "abcdef" {
		t.Errorf("content = %q, want %q", got, "abcdef")
	}
}

// TestWriteToReadOnlyHandle checks that a handle without Write refuses writes.
func TestWriteToReadOnlyHandle(t *testing.T) {
	t.Parallel()
	mapFS := fstest.MapFS{"f": &fstest.MapFile{Data: []byte(content)}}
	inner, err := mapFS.Open("f")
	if err != nil {
		t.Fatalf("Open(f) = %v, want nil", err)
	}
	f := NewFile(inner)
	if _, err := f.Write([]byte("x")); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Write() = %v, want an error matching fs.ErrPermission", err)
	}
	if _, err := f.WriteAt([]byte("x"), 0); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("WriteAt() = %v, want an error matching fs.ErrPermission", err)
	}
}

// TestCloseExcludesCalls closes the handle while other goroutines use it. The
// inner handle's closed flag is unguarded, so the race detector reports any
// call the wrapper lets overlap Close.
func TestCloseExcludesCalls(t *testing.T) {
	t.Parallel()
	f, _ := newSeekFile()

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			buf := make([]byte, 2)
			for range 50 {
				if _, err := f.ReadAt(buf, 1); err != nil && !errors.Is(err, fs.ErrClosed) {
					t.Errorf("ReadAt(2, 1) racing Close = %v, want nil or fs.ErrClosed", err)
				}
				if _, err := f.Read(buf); err != nil && !errors.Is(err, fs.ErrClosed) && !errors.Is(err, io.EOF) {
					t.Errorf("Read() racing Close = %v, want nil, io.EOF or fs.ErrClosed", err)
				}
			}
		})
	}
	wg.Go(func() {
		if err := f.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	})
	wg.Wait()
}

// BenchmarkReadAtNative measures what the wrapper adds to a native ReadAt.
func BenchmarkReadAtNative(b *testing.B) {
	mapFS := fstest.MapFS{"f": &fstest.MapFile{Data: []byte(content)}}
	inner, err := mapFS.Open("f")
	if err != nil {
		b.Fatalf("Open(f) = %v, want nil", err)
	}
	f := NewFile(inner)
	buf := make([]byte, 8)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := f.ReadAt(buf, 4); err != nil {
			b.Fatal(err)
		}
	}
}
