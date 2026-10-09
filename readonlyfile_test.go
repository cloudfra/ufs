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
	"testing"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

// newReadOnlyTestFile returns a readOnlyFile over a read-write memFS handle
// to a file holding content, along with the memFS to check the file through.
func newReadOnlyTestFile(t *testing.T, content string) (*readOnlyFile, *memFS) {
	t.Helper()
	mfs := makeMemFS("memory:///")
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
	full, ok := inner.(File)
	if !ok {
		t.Fatalf("memFS.Open() = %T, want a File", inner)
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
			got, err := mfs.ReadFile("file.txt")
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
// nothing beyond File, so no optional interface of the handle underneath, such
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

// closeCountFile is a File that only supports Close, which it counts.
type closeCountFile struct {
	File
	closed int
	err    error
}

func (f *closeCountFile) Close() error {
	f.closed++
	return f.err
}
