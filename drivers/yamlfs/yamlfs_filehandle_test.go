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

package yamlfs

import (
	"errors"
	"io"
	"io/fs"
	"strings"
	"sync"
	"testing"
)

// A leaf handle keeps the position that the next read continues from, and a
// mutex around the methods the handle declares only guards those methods. The
// reader used to be an embedded field, which put the eight methods of
// *bytes.Reader onto the handle too — WriteTo, Len, Size, ReadByte, UnreadByte,
// ReadRune, UnreadRune and Reset — and those move or read through the same
// position with no lock taken. io.Copy asks for io.WriterTo before it settles
// for Read, so every copy through a wrapper went straight to the unguarded half
// of the handle while a concurrent reader was in the guarded half.
//
// These tests fix the method set and then walk one handle from two goroutines
// through the paths that matter: a copy and a read competing for one position,
// and a positional read racing a seek.

// leafDoc is the tree these tests open: one leaf whose content is twenty-six
// different bytes, so a test can tell whether two readers between them read the
// content over exactly once.
const leafDoc = `
root:
  alpha: ABCDEFGHIJKLMNOPQRSTUVWXYZ
`

// leafContent is what that leaf holds.
const leafContent = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// leafHandle opens the leaf these tests read.
func leafHandle(t *testing.T) fs.File {
	t.Helper()
	fsys, err := New("leaf.yaml", []byte(leafDoc))
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	file, err := fsys.Open("root/alpha")
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil", "root/alpha", err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("Close of the leaf handle: %v", err)
		}
	})
	return file
}

// TestLeafHandleExposesNoUnguardedPositionMover checks the method set rather
// than the locking, because locking cannot cover this: a method promoted from
// the embedded reader moves the shared position without taking the mutex, so the
// only way to make those calls safe is not to answer them at all.
func TestLeafHandleExposesNoUnguardedPositionMover(t *testing.T) {
	t.Parallel()
	file := leafHandle(t)

	if _, ok := file.(io.WriterTo); ok {
		t.Errorf("a leaf handle answers io.WriterTo, which moves the handle's position with no lock taken and is the path io.Copy prefers over Read")
	}
	if _, ok := file.(io.ByteReader); ok {
		t.Errorf("a leaf handle answers io.ByteReader, which moves the handle's position with no lock taken")
	}
	if _, ok := file.(io.RuneScanner); ok {
		t.Errorf("a leaf handle answers io.RuneScanner, which moves the handle's position with no lock taken")
	}
	if _, ok := file.(interface{ Len() int }); ok {
		t.Errorf("a leaf handle answers Len, which reports the handle's position with no lock taken")
	}
	if _, ok := file.(interface{ Size() int64 }); ok {
		t.Errorf("a leaf handle answers Size, which reports the handle's position with no lock taken")
	}
	if _, ok := file.(interface {
		Reset(io.Reader)
	}); ok {
		t.Errorf("a leaf handle answers Reset, which replaces the handle's position with no lock taken")
	}

	// The handle still has to answer what the code around it asks for.
	if _, ok := file.(io.Reader); !ok {
		t.Errorf("a leaf handle does not answer io.Reader")
	}
	if _, ok := file.(io.ReaderAt); !ok {
		t.Errorf("a leaf handle does not answer io.ReaderAt")
	}
	if _, ok := file.(io.Seeker); !ok {
		t.Errorf("a leaf handle does not answer io.Seeker")
	}
}

// TestLeafHandleSharedByCopyAndRead reads one leaf handle from two goroutines:
// one through io.Copy, and one through Read with a buffer too small for the
// content in one call. Both advance the one position the handle keeps, so
// between them they have to be handed every byte of the content exactly once,
// with none doubled and none dropped.
func TestLeafHandleSharedByCopyAndRead(t *testing.T) {
	t.Parallel()
	file := leafHandle(t)

	// Each goroutine writes only its own result: sharing a buffer between them
	// would be a race inside the test rather than in the handle, and this test
	// is here to measure the handle.
	var copied string
	var read []byte

	var running sync.WaitGroup
	running.Go(func() {
		builder := &strings.Builder{}
		if _, err := io.Copy(builder, file); err != nil {
			t.Errorf("io.Copy over the shared handle: %v", err)
		}
		copied = builder.String()
	})
	running.Go(func() {
		buf := make([]byte, 5)
		for {
			n, err := file.Read(buf)
			read = append(read, buf[:n]...)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					t.Errorf("Read over the shared handle: %v", err)
				}
				return
			}
		}
	})
	running.Wait()

	got := []byte(copied)
	got = append(got, read...)
	if len(got) != len(leafContent) {
		t.Fatalf("the copy took %q and the read took %q, %d bytes between them, want the %d bytes of the content read over exactly once",
			copied, read, len(got), len(leafContent))
	}
	if !holdsContentOnce(got) {
		t.Errorf("the copy took %q and the read took %q, which is not the content read over exactly once: %q",
			copied, read, leafContent)
	}
}

// holdsContentOnce reports whether got holds every byte of the content exactly
// once. The content is twenty-six different bytes, so counting them answers it.
func holdsContentOnce(got []byte) bool {
	var counts [26]int8
	for _, b := range []byte(leafContent) {
		counts[b-'A']++
	}
	for _, b := range got {
		if b < 'A' || b > 'Z' {
			return false
		}
		counts[b-'A']--
		if counts[b-'A'] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

// TestLeafHandleReadAtWhileSeeking races a positional read against a seek on one
// handle. ReadAt has to answer from the offset it was given however the other
// hand moves the read position, and that is only true while both of them hold
// the mutex: the two methods touch the same reader and only one of them may move
// the position.
func TestLeafHandleReadAtWhileSeeking(t *testing.T) {
	t.Parallel()
	file := leafHandle(t)

	var running sync.WaitGroup
	running.Go(func() {
		seeker, ok := file.(io.Seeker)
		if !ok {
			t.Errorf("a leaf handle does not answer io.Seeker")
			return
		}
		for range 50 {
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				t.Errorf("Seek(0, io.SeekStart) over the shared handle: %v", err)
				return
			}
			if _, err := seeker.Seek(-1, io.SeekEnd); err != nil {
				t.Errorf("Seek(-1, io.SeekEnd) over the shared handle: %v", err)
				return
			}
		}
	})
	running.Go(func() {
		reader, ok := file.(io.ReaderAt)
		if !ok {
			t.Errorf("a leaf handle does not answer io.ReaderAt")
			return
		}
		buf := make([]byte, 4)
		for off := int64(0); off+4 <= int64(len(leafContent)); off++ {
			n, err := reader.ReadAt(buf, off)
			if err != nil && !errors.Is(err, io.EOF) {
				t.Errorf("ReadAt at offset %d over the shared handle: %v", off, err)
				return
			}
			if string(buf[:n]) != leafContent[off:off+4] {
				t.Errorf("ReadAt at offset %d answered %q while the read position was being moved, want %q",
					off, buf[:n], leafContent[off:off+4])
				return
			}
		}
	})
	running.Wait()
}
