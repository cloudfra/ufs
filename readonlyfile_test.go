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
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// newReadOnlyTestFile returns a readOnlyFile over a read-write memFS handle
// to a file holding content, along with the memFS to check the file through.
func newReadOnlyTestFile(t *testing.T, content string) (*readOnlyFile, types.WriteFS) {
	t.Helper()
	mfs := mustBaseFS(t, "memory:")
	t.Cleanup(ufsTesting.ValidateClose(t, mfs))

	w, err := mfs.Create("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(content); err != nil {
		t.Fatal(err)
	}
	ufsTesting.Must(t, w.Close())

	inner, err := mfs.Open("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	full, ok := inner.(types.File)
	if !ok {
		t.Fatalf("memFS.Open() = %T, want a types.File", inner)
	}
	// The test is only meaningful if the handle underneath can write.
	if _, err := full.Write(nil); err != nil {
		t.Fatalf("memFS handle Write(nil) = %v, want a writable handle", err)
	}
	f := &readOnlyFile{File: full}
	t.Cleanup(ufsTesting.ValidateClose(t, f))
	return f, mfs
}

func TestReadOnlyFileRejectsWrites(t *testing.T) {
	t.Parallel()
	const content = "content"

	testCases := []struct {
		name  string
		write func(f *readOnlyFile) (int, error)
	}{
		{name: "Write", write: func(f *readOnlyFile) (int, error) { return f.Write([]byte("changed")) }},
		{name: "Write empty", write: func(f *readOnlyFile) (int, error) { return f.Write(nil) }},
		{name: "WriteString", write: func(f *readOnlyFile) (int, error) { return f.WriteString("changed") }},
		{name: "io.WriteString", write: func(f *readOnlyFile) (int, error) { return io.WriteString(f, "changed") }},
		{name: "io.Copy", write: func(f *readOnlyFile) (int, error) {
			n, err := io.Copy(f, endlessReader{})
			return int(n), err
		}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, mfs := newReadOnlyTestFile(t, content)

			n, err := tc.write(f)
			if n != 0 || !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("%s = (%d, %v), want (0, fs.ErrInvalid)", tc.name, n, err)
			}
			got, err := fs.ReadFile(mfs, "file.txt")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != content {
				t.Errorf("file holds %q after a rejected write, want %q", got, content)
			}
		})
	}
}

// endlessReader always has more data, so a copy from it only stops when the
// destination rejects the write.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	return copy(p, "changed"), nil
}

func TestReadOnlyFileReads(t *testing.T) {
	t.Parallel()
	const content = "hello world"
	f, _ := newReadOnlyTestFile(t, content)

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}
	if info.Name() != "file.txt" || info.Size() != int64(len(content)) {
		t.Errorf("Stat() = %q of %d bytes, want %q of %d bytes", info.Name(), info.Size(), "file.txt", len(content))
	}

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll() = %v, want nil", err)
	}
	if string(got) != content {
		t.Errorf("ReadAll() = %q, want %q", got, content)
	}

	buf := make([]byte, 5)
	// A ReadAt that reaches the end of the file may report io.EOF with the data.
	if n, err := f.ReadAt(buf, 6); n != 5 || (err != nil && !errors.Is(err, io.EOF)) || string(buf) != "world" {
		t.Errorf("ReadAt(6) = (%d, %v) %q, want 5 bytes %q", n, err, buf, "world")
	}

	if pos, err := f.Seek(0, io.SeekStart); pos != 0 || err != nil {
		t.Fatalf("Seek(0) = (%d, %v), want (0, nil)", pos, err)
	}
	if n, err := io.ReadFull(f, buf); n != 5 || err != nil || string(buf) != "hello" {
		t.Errorf("Read after Seek = (%d, %v) %q, want (5, nil) %q", n, err, buf, "hello")
	}
}

// TestReadOnlyFileHidesOtherWritePaths verifies that the wrapper exposes
// nothing beyond types.File, so no optional interface of the handle underneath, such
// as io.ReaderFrom or io.WriterAt, offers a way around Write.
func TestReadOnlyFileHidesOtherWritePaths(t *testing.T) {
	t.Parallel()
	f, _ := newReadOnlyTestFile(t, "content")
	var v any = f

	if _, ok := v.(io.ReaderFrom); ok {
		t.Error("readOnlyFile implements io.ReaderFrom")
	}
	if _, ok := v.(io.WriterAt); ok {
		t.Error("readOnlyFile implements io.WriterAt")
	}
	if _, ok := v.(interface{ Truncate(int64) error }); ok {
		t.Error("readOnlyFile implements Truncate")
	}
}

func TestReadOnlyFileCloseClosesHandle(t *testing.T) {
	t.Parallel()
	inner := &closeCountFile{}
	f := &readOnlyFile{File: inner}

	if err := f.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if inner.closed != 1 {
		t.Errorf("handle closed %d times, want 1", inner.closed)
	}

	inner.err = errors.New("close failed")
	if err := f.Close(); !errors.Is(err, inner.err) {
		t.Errorf("Close() = %v, want %v", err, inner.err)
	}
}

// closeCountFile is a types.File that only supports Close, which it counts.
type closeCountFile struct {
	types.File
	closed int
	err    error
}

func (f *closeCountFile) Close() error {
	f.closed++
	return f.err
}

// TestReadOnlyHandleKeepsOSHandles verifies that a handle backed by an OS file
// is returned unchanged, also when the backend embeds the OS file in its own
// type, as localFS does on Windows, and that any other handle is wrapped.
func TestReadOnlyHandleKeepsOSHandles(t *testing.T) {
	t.Parallel()
	name := filepath.Join(t.TempDir(), "file.txt")
	if err := osutil.WriteFile(name, []byte("content")); err != nil {
		t.Fatal(err)
	}
	type embedsOSFile struct{ *os.File }
	for _, wrap := range []func(*os.File) types.File{
		func(f *os.File) types.File { return f },
		func(f *os.File) types.File { return &embedsOSFile{f} },
	} {
		osFile, err := osutil.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		f := wrap(osFile)
		if got := readOnlyHandle(f); got != f {
			t.Errorf("readOnlyHandle() = %T, want the %T it was given", got, f)
		}
		ufsTesting.Must(t, osFile.Close())
	}

	if _, ok := readOnlyHandle(&closeCountFile{}).(*readOnlyFile); !ok {
		t.Error("readOnlyHandle() did not wrap a handle that is not an OS file")
	}
}

// TestNestFSOpenHandleCannotWrite verifies that a handle from Open cannot
// change the file, for a backend whose handles can write.
func TestNestFSOpenHandleCannotWrite(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()
	const want = "original"
	f, err := fsys.Create("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(want); err != nil {
		t.Fatal(err)
	}
	ufsTesting.Must(t, f.Close())

	opened, err := fsys.Open("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, opened)()
	w, ok := opened.(io.Writer)
	if !ok {
		t.Fatalf("Open() = %T, want a handle with a Write method that fails", opened)
	}
	if _, err := w.Write([]byte("changed")); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Write() on a handle from Open = %v, want fs.ErrInvalid", err)
	}

	got, err := fsys.ReadFile("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("ReadFile() = %q, want %q", got, want)
	}
}
