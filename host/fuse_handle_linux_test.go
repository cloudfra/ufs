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

package host

import (
	"io"
	"io/fs"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
)

// These tests drive fuseFileHandle directly, the way go-fuse calls it, so they
// run without /dev/fuse.

const handleContent = "the quick brown fox jumps over the lazy dog"

// seekOnly hides the ReadAt of the handle it wraps, which leaves a handle that
// is read by seeking: the case where a seek and a read from two requests could
// interleave.
type seekOnly struct {
	f fs.File
}

func (s seekOnly) Stat() (fs.FileInfo, error) { return s.f.Stat() }
func (s seekOnly) Read(p []byte) (int, error) { return s.f.Read(p) }
func (s seekOnly) Close() error               { return s.f.Close() }

func (s seekOnly) Seek(off int64, whence int) (int64, error) {
	return s.f.(io.Seeker).Seek(off, whence)
}

// readOnly hides Seek as well, which leaves a handle that is read in order.
type readOnly struct {
	f fs.File
}

func (r readOnly) Stat() (fs.FileInfo, error) { return r.f.Stat() }
func (r readOnly) Read(p []byte) (int, error) { return r.f.Read(p) }
func (r readOnly) Close() error               { return r.f.Close() }

// appendOnly takes writes in order and refuses every seek, as the whole-file
// upload of drivers/grpcfs does.
type appendOnly struct {
	data []byte
}

func (a *appendOnly) Stat() (fs.FileInfo, error) { return nil, fs.ErrInvalid }
func (a *appendOnly) Read([]byte) (int, error)   { return 0, fs.ErrInvalid }
func (a *appendOnly) Close() error               { return nil }
func (a *appendOnly) Seek(int64, int) (int64, error) {
	return 0, &fs.PathError{Op: "seek", Path: "upload", Err: fs.ErrInvalid}
}

func (a *appendOnly) Write(p []byte) (int, error) {
	a.data = append(a.data, p...)
	return len(p), nil
}

func openHandleContent(t *testing.T) fs.File {
	t.Helper()
	f, err := fstest.MapFS{"f": &fstest.MapFile{Data: []byte(handleContent)}}.Open("f")
	if err != nil {
		t.Fatalf("Open(f) = %v, want nil", err)
	}
	return f
}

func readHandle(t *testing.T, fh *fuseFileHandle, size int, off int64) (string, syscall.Errno) {
	t.Helper()
	res, errno := fh.Read(t.Context(), make([]byte, size), off)
	if errno != 0 {
		return "", errno
	}
	data, status := res.Bytes(nil)
	if !status.Ok() {
		t.Fatalf("ReadResult.Bytes() status = %v", status)
	}
	return string(data), 0
}

// TestFuseHandleConcurrentSeekReads reads one seek-only handle at every offset
// from parallel requests. Each must get the bytes at its own offset.
func TestFuseHandleConcurrentSeekReads(t *testing.T) {
	t.Parallel()
	fh := newFuseFileHandle(seekOnly{openHandleContent(t)})

	var wg sync.WaitGroup
	for off := range len(handleContent) {
		wg.Go(func() {
			got, errno := readHandle(t, fh, 5, int64(off))
			want := handleContent[off:min(off+5, len(handleContent))]
			if errno != 0 || got != want {
				t.Errorf("Read(5, %d) = %q, %v, want %q, 0", off, got, errno, want)
			}
		})
	}
	wg.Wait()
	if errno := fh.Release(t.Context()); errno != 0 {
		t.Errorf("Release() = %v, want 0", errno)
	}
}

// TestFuseHandleInOrderRead checks a handle that cannot seek: reads that
// continue where it stands are answered, and one aimed elsewhere is ENOTSUP.
func TestFuseHandleInOrderRead(t *testing.T) {
	t.Parallel()
	fh := newFuseFileHandle(readOnly{openHandleContent(t)})

	if got, errno := readHandle(t, fh, 4, 0); errno != 0 || got != handleContent[:4] {
		t.Fatalf("Read(4, 0) = %q, %v, want %q, 0", got, errno, handleContent[:4])
	}
	if got, errno := readHandle(t, fh, 6, 4); errno != 0 || got != handleContent[4:10] {
		t.Fatalf("Read(6, 4) = %q, %v, want %q, 0", got, errno, handleContent[4:10])
	}
	if _, errno := readHandle(t, fh, 4, 0); errno != syscall.ENOTSUP {
		t.Errorf("Read(4, 0) behind the handle = %v, want ENOTSUP", errno)
	}
}

// TestFuseHandleInOrderWrite checks a handle that takes its bytes only in order:
// writes that continue it land, and one out of order is EINVAL rather than
// landing in the wrong place.
func TestFuseHandleInOrderWrite(t *testing.T) {
	t.Parallel()
	upload := &appendOnly{}
	fh := newFuseFileHandle(upload)

	if n, errno := fh.Write(t.Context(), []byte("abc"), 0); n != 3 || errno != 0 {
		t.Fatalf("Write(abc, 0) = %d, %v, want 3, 0", n, errno)
	}
	if n, errno := fh.Write(t.Context(), []byte("def"), 3); n != 3 || errno != 0 {
		t.Fatalf("Write(def, 3) = %d, %v, want 3, 0", n, errno)
	}
	if _, errno := fh.Write(t.Context(), []byte("x"), 1); errno != syscall.EINVAL {
		t.Errorf("Write(x, 1) out of order = %v, want EINVAL", errno)
	}
	if got := string(upload.data); got != "abcdef" {
		t.Errorf("uploaded %q, want %q", got, "abcdef")
	}
}
