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

// writableHandle returns a handle holding content that can write, along with the
// store behind it. It stands in for the read-write handle a backend such as memFS
// returns for a file that was opened for reading.
func writableHandle(name, content string) (*fakeHandle, *store) {
	st := newStore()
	st.seedFile(name, content)
	return &fakeHandle{store: st, name: name, writable: true}, st
}

// TestToReadOnlyFileRejectsWrites checks that every way of writing through the
// returned handle fails and leaves the content of the handle underneath alone.
func TestToReadOnlyFileRejectsWrites(t *testing.T) {
	t.Parallel()
	const name = "file.txt"
	const content = "content"

	testCases := []struct {
		name  string
		write func(f types.File) (int, error)
	}{
		{name: "Write", write: func(f types.File) (int, error) { return f.Write([]byte("changed")) }},
		{name: "Write empty", write: func(f types.File) (int, error) { return f.Write(nil) }},
		{name: "WriteString", write: func(f types.File) (int, error) { return f.WriteString("changed") }},
		{name: "io.WriteString", write: func(f types.File) (int, error) { return io.WriteString(f, "changed") }},
		{name: "io.Copy", write: func(f types.File) (int, error) {
			n, err := io.Copy(f, endlessReader{})
			return int(n), err
		}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inner, st := writableHandle(name, content)
			// The test is only meaningful if the handle underneath can write.
			if _, err := inner.Write(nil); err != nil {
				t.Fatalf("handle Write(nil) = %v, want a writable handle", err)
			}
			f := ToReadOnlyFile(inner)

			n, err := tc.write(f)
			if n != 0 || !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("%s = (%d, %v), want (0, fs.ErrInvalid)", tc.name, n, err)
			}
			if got := string(st.files[name]); got != content {
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

// TestToReadOnlyFileKeepsReads checks that Stat, Read, ReadAt and Seek of the
// handle underneath still work through the wrapper.
func TestToReadOnlyFileKeepsReads(t *testing.T) {
	t.Parallel()
	const name = "file.txt"
	const content = "hello world"

	inner, _ := writableHandle(name, content)
	f := ToReadOnlyFile(inner)
	defer ufsTesting.ValidateClose(t, f)()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}
	if info.Name() != name || info.Size() != int64(len(content)) {
		t.Errorf("Stat() = %q of %d bytes, want %q of %d bytes", info.Name(), info.Size(), name, len(content))
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

// TestToReadOnlyFileHidesOtherWritePaths verifies that the wrapper exposes
// nothing beyond types.File, so no optional interface of the handle underneath,
// such as io.ReaderFrom or io.WriterAt, offers a way around Write.
func TestToReadOnlyFileHidesOtherWritePaths(t *testing.T) {
	t.Parallel()
	inner, _ := writableHandle("file.txt", "content")
	var v any = ToReadOnlyFile(inner)

	if _, ok := v.(io.ReaderFrom); ok {
		t.Error("the read-only handle implements io.ReaderFrom")
	}
	if _, ok := v.(io.WriterAt); ok {
		t.Error("the read-only handle implements io.WriterAt")
	}
	if _, ok := v.(interface{ Truncate(int64) error }); ok {
		t.Error("the read-only handle implements Truncate")
	}
}

// TestToReadOnlyFileCloseClosesHandle checks that Close reaches the handle
// underneath and reports its error.
func TestToReadOnlyFileCloseClosesHandle(t *testing.T) {
	t.Parallel()
	inner := &closeCountFile{}
	f := ToReadOnlyFile(inner)

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

// TestToReadOnlyFileKeepsOSHandles verifies that a handle backed by an OS file
// is returned unchanged, also when the backend embeds the OS file in its own
// type as localFS does on Windows, that such a handle really cannot be written
// through because the OS opened it for reading, and that any other handle is
// wrapped.
func TestToReadOnlyFileKeepsOSHandles(t *testing.T) {
	t.Parallel()
	const content = "content"
	name := filepath.Join(t.TempDir(), "file.txt")
	if err := osutil.WriteFile(name, []byte(content)); err != nil {
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
		inner := wrap(osFile)
		if got := ToReadOnlyFile(inner); got != inner {
			t.Errorf("ToReadOnlyFile() = %T, want the %T it was given", got, inner)
		}
		if _, err := inner.Write([]byte("changed")); err == nil {
			t.Error("Write() on a handle the OS opened for reading = nil, want an error")
		}
		ufsTesting.Must(t, osFile.Close())
	}

	got, err := os.ReadFile(filepath.Clean(name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("file holds %q after a rejected write, want %q", got, content)
	}

	if _, ok := ToReadOnlyFile(&closeCountFile{}).(*readOnlyFile); !ok {
		t.Error("ToReadOnlyFile() did not wrap a handle that is not an OS file")
	}
}
