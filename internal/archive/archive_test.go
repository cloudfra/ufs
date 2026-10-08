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

package archive

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
)

const (
	testAssetsDir    = "../../testing/testassets/"
	testArchivesDir  = testAssetsDir + "archives/"
	testTarGzArchive = testArchivesDir + "testassets.tar.gz"
	testNoDirArchive = testArchivesDir + "nodir-testassets.zip"
)

var mountablePathTestCases = []struct {
	input string
	want  bool
}{
	{input: "", want: false},
	{input: pathutil.CwdPath, want: false},
	{input: "/", want: false},
	{input: "abc", want: false},
	{input: "/abc/d/", want: false},
	{input: "abc/d/", want: false},
	{input: "/abc/d", want: false},
	{input: "abc\\d", want: false},
	{input: "\\abc\\", want: false},
	{input: "ok.tar", want: true},
	{input: "ok.tar.gz", want: true},
	{input: "ok.tar.bz2", want: true},
	{input: "ok.tar.xz", want: true},
	{input: "ok.tar.lz4", want: true},
	{input: "ok.tar.br", want: true},
	{input: "ok.tar.zst", want: true},
	{input: "ok.zip", want: true},
	{input: "ok.tar.lzma", want: false},
	{input: "ok.7z", want: true},
	{input: "ok.7Z", want: true},
}

func TestIsMountablePath(t *testing.T) {
	t.Parallel()
	for _, tc := range mountablePathTestCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := IsMountablePath(tc.input); got != tc.want {
				t.Errorf("IsMountablePath(%q) got: %v, want: %v", tc.input, got, tc.want)
			}
		})
	}
}

// mustNew opens the archive at name and closes it when the test ends.
func mustNew(t *testing.T, name string) FS {
	t.Helper()
	fsys, err := New(t.Context(), name)
	if err != nil {
		t.Fatalf("New(%q) = %v", name, err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	return fsys
}

func TestNew(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"testassets.tar",
		"testassets.tar.gz",
		"testassets.tar.bz2",
		"testassets.tar.xz",
		"testassets.tar.lz4",
		"testassets.7z",
		"single-testassets.zip",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fsys := mustNew(t, testArchivesDir+name)

			data, err := fs.ReadFile(fsys, "index.html")
			if err != nil {
				t.Fatalf("ReadFile(index.html) = %v", err)
			}
			if len(data) == 0 {
				t.Error("ReadFile(index.html) returned no data")
			}
			info, err := fsys.Stat("assets")
			if err != nil {
				t.Fatalf("Stat(assets) = %v", err)
			}
			if !info.IsDir() {
				t.Error("Stat(assets) is not a directory")
			}
			entries, err := fsys.ReadDir("assets")
			if err != nil {
				t.Fatalf("ReadDir(assets) = %v", err)
			}
			if len(entries) == 0 {
				t.Error("ReadDir(assets) returned no entries")
			}
			f, err := fsys.Open("index.html")
			if err != nil {
				t.Fatalf("Open(index.html) = %v", err)
			}
			got, err := io.ReadAll(f)
			if err != nil {
				t.Errorf("ReadAll(index.html) = %v", err)
			}
			if !bytes.Equal(got, data) {
				t.Errorf("Open(index.html) read %q, want %q", got, data)
			}
			if err := f.Close(); err != nil {
				t.Errorf("Close(index.html) = %v", err)
			}
		})
	}
}

func TestNewDirectory(t *testing.T) {
	t.Parallel()
	fsys := mustNew(t, testAssetsDir+"files")
	if _, err := fsys.Stat("index.html"); err != nil {
		t.Errorf("Stat(index.html) = %v", err)
	}
}

func TestNewErrors(t *testing.T) {
	t.Parallel()

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		fsys, err := New(t.Context(), filepath.Join(t.TempDir(), "missing.tar.gz"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("New() = (%v, %v), want fs.ErrNotExist", fsys, err)
		}
	})

	t.Run("failed open releases the file", func(t *testing.T) {
		t.Parallel()
		// An xz header followed by garbage is identified as xz and then fails.
		path := filepath.Join(t.TempDir(), "corrupt.tar.xz")
		data := append([]byte{0xfd, 0x37, 0x7a, 0x58, 0x5a, 0x00}, bytes.Repeat([]byte{0xff}, 64)...)
		if err := os.WriteFile(path, data, osutil.DefaultFilePermissions); err != nil {
			t.Fatal(err)
		}
		fsys, err := New(t.Context(), path)
		if err == nil {
			if closeErr := fsys.Close(); closeErr != nil {
				t.Errorf("Close() = %v", closeErr)
			}
			t.Fatal("New() = nil, want an error for a corrupt archive")
		}
		// The file must not be held open, or Windows cannot remove it.
		if err := os.Remove(path); err != nil {
			t.Errorf("Remove() after a failed New = %v", err)
		}
	})
}

func TestNewFromFile(t *testing.T) {
	t.Parallel()
	file, err := osutil.Open(testTarGzArchive)
	if err != nil {
		t.Fatal(err)
	}
	fsys, err := NewFromFile(t.Context(), "testassets.tar.gz", file)
	if err != nil {
		if closeErr := file.Close(); closeErr != nil {
			t.Errorf("Close() = %v", closeErr)
		}
		t.Fatalf("NewFromFile() = %v", err)
	}
	if _, err := fs.ReadFile(fsys, "index.html"); err != nil {
		t.Errorf("ReadFile(index.html) = %v", err)
	}

	// Close closes the file it was given.
	if err := fsys.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
	if err := file.Close(); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("file.Close() after FS.Close() = %v, want fs.ErrClosed", err)
	}
}

// streamOnlyFile is an fs.File without Seek or ReadAt.
type streamOnlyFile struct{ fs.File }

func TestNewFromFileRequiresRandomAccess(t *testing.T) {
	t.Parallel()
	file, err := osutil.Open(testTarGzArchive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	fsys, err := NewFromFile(t.Context(), "testassets.tar.gz", streamOnlyFile{file})
	if err == nil {
		t.Fatalf("NewFromFile() = %v, want an error for a file without Seek and ReadAt", fsys)
	}
}

func TestOpenImplicitDirectory(t *testing.T) {
	t.Parallel()
	// The zip has no directory entries, so "onetwothree" exists only as a
	// prefix of its files and Open must build the index to resolve it.
	fsys := mustNew(t, testNoDirArchive)

	for range 2 {
		f, err := fsys.Open("onetwothree")
		if err != nil {
			t.Fatalf("Open(onetwothree) = %v", err)
		}
		info, err := f.Stat()
		if err != nil {
			t.Fatalf("Stat() = %v", err)
		}
		if !info.IsDir() || info.Name() != "onetwothree" {
			t.Errorf("Open(onetwothree) = %q (dir %t), want the directory onetwothree", info.Name(), info.IsDir())
		}
		if err := f.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	}
	if !fsys.(*archiveFS).isIndexed.Load() {
		t.Error("archive was not indexed after opening an implicit directory")
	}
}

// closerFunc adapts a bare function to io.Closer.
type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// emptyFS is an fs.FS with no entries.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func TestCloseClosesUnderlyingFile(t *testing.T) {
	t.Parallel()

	var closeCalled atomic.Int32
	afs := makeArchiveFS(emptyFS{}, "test.zip", closerFunc(func() error {
		closeCalled.Add(1)
		return nil
	}))

	if err := afs.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if closeCalled.Load() != 1 {
		t.Error("underlying file closer was not called on Close()")
	}
}

func TestCloseWithoutCloserIsNoop(t *testing.T) {
	t.Parallel()

	afs := makeArchiveFS(emptyFS{}, "test.zip", nil)
	if err := afs.Close(); err != nil {
		t.Errorf("Close() = %v, want nil (no closer set)", err)
	}
}

func TestCloseReportsCloserError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("file close failed")
	afs := makeArchiveFS(emptyFS{}, "test.zip", closerFunc(func() error {
		return wantErr
	}))

	if err := afs.Close(); !errors.Is(err, wantErr) {
		t.Errorf("Close() = %v, want %v", err, wantErr)
	}
}

func TestCloseIdempotent(t *testing.T) {
	t.Parallel()

	var closeCalled atomic.Int32
	afs := makeArchiveFS(emptyFS{}, "test.zip", closerFunc(func() error {
		closeCalled.Add(1)
		return nil
	}))

	for range 3 {
		if err := afs.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	}
	if closeCalled.Load() != 1 {
		t.Errorf("closer called %d times, want exactly 1", closeCalled.Load())
	}
}
