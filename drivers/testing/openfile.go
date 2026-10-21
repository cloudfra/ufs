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

package testing

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

const (
	openFileDir  = "openfile"
	openFileName = openFileDir + "/a.txt"
)

// OpenFile verifies [ufs.OpenFile] on a writable file system: a file can be
// opened to read it and to replace it, and every other flag either works as
// it does in package os or fails with [errors.ErrUnsupported] and leaves the
// file alone.
func OpenFile(t *testing.T, createFS func(*testing.T) types.WriteFS) {
	openFile(t, createFS, false)
}

// OpenFileAllFlags is [OpenFile] for a file system that honors every flag. A
// flag that fails with [errors.ErrUnsupported] is an error.
func OpenFileAllFlags(t *testing.T, createFS func(*testing.T) types.WriteFS) {
	openFile(t, createFS, true)
}

// OpenFileReadOnly verifies the part of [ufs.OpenFile] that every file
// system supports, whether it is writable or not.
func OpenFileReadOnly(t *testing.T, createFS func(*testing.T) types.ReadFS) {
	t.Run("OpenFileReadOnly", func(t *testing.T) {
		t.Parallel()
		fsys := createFS(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		f, err := ufs.OpenFile(fsys, ".", os.O_RDONLY, 0)
		if err != nil {
			t.Fatalf("OpenFile(., O_RDONLY) = %v, want nil", err)
		}
		if err := f.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}

		for _, flag := range []int{os.O_RDONLY, os.O_WRONLY | os.O_CREATE | os.O_TRUNC, os.O_RDWR} {
			if f, err := ufs.OpenFile(fsys, "../a.txt", flag, fs.ModePerm); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("OpenFile(../a.txt, %#x) = %v, %v, want %v", flag, f, err, fs.ErrInvalid)
			}
		}
	})
}

func openFile(t *testing.T, createFS func(*testing.T) types.WriteFS, allFlags bool) {
	t.Run("OpenFile", func(t *testing.T) {
		t.Parallel()
		fsys := createFS(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		if err := fsys.MkdirAll(openFileDir, fs.ModePerm); err != nil {
			t.Fatalf("MkdirAll(%q) = %v, want nil", openFileDir, err)
		}

		const replace = os.O_WRONLY | os.O_CREATE | os.O_TRUNC

		// Opening a file that does not exist fails, unless it is created.
		for _, flag := range []int{os.O_RDONLY, os.O_WRONLY | os.O_TRUNC, os.O_RDWR | os.O_TRUNC} {
			if f, err := ufs.OpenFile(fsys, openFileName, flag, fs.ModePerm); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("OpenFile(%q, %#x) of a missing file = %v, %v, want %v", openFileName, flag, f, err, fs.ErrNotExist)
			}
		}
		if _, err := fsys.Stat(openFileName); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) after failed opens = %v, want %v", openFileName, err, fs.ErrNotExist)
		}

		// Every file system can create a file, replace it and read it.
		openFileWrite(t, fsys, replace, "first content")
		openFileWant(t, fsys, "first content")
		openFileWrite(t, fsys, replace, "second")
		openFileWant(t, fsys, "second")
		openFileWrite(t, fsys, os.O_RDWR|os.O_TRUNC, "third")
		openFileWant(t, fsys, "third")

		// The other flags change a file in place or create it atomically,
		// which a file system may not be able to do.
		for _, tc := range []struct {
			name  string
			flag  int
			write string
			want  string
		}{
			{name: "append", flag: os.O_WRONLY | os.O_APPEND, write: "+more", want: "third+more"},
			{name: "in_place", flag: os.O_WRONLY, write: "TH", want: "THird+more"},
			{name: "in_place_read_write", flag: os.O_RDWR, write: "th", want: "third+more"},
			{name: "create_existing", flag: os.O_WRONLY | os.O_CREATE, write: "T", want: "Third+more"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				before, err := fsys.ReadFile(openFileName)
				if err != nil {
					t.Fatalf("ReadFile(%q) = %v, want nil", openFileName, err)
				}
				f, err := ufs.OpenFile(fsys, openFileName, tc.flag, fs.ModePerm)
				if errors.Is(err, errors.ErrUnsupported) && !allFlags {
					openFileWant(t, fsys, string(before))
					return
				}
				if err != nil {
					t.Fatalf("OpenFile(%q, %#x) = %v, want nil", openFileName, tc.flag, err)
				}
				openFileWriteClose(t, f, tc.write)
				// A file system that honors only some of the flags starts
				// from different content than the one that honors all.
				if allFlags {
					openFileWant(t, fsys, tc.want)
				}
			})
		}

		t.Run("exclusive", func(t *testing.T) {
			const exclusive = os.O_WRONLY | os.O_CREATE | os.O_EXCL
			before, err := fsys.ReadFile(openFileName)
			if err != nil {
				t.Fatalf("ReadFile(%q) = %v, want nil", openFileName, err)
			}
			f, err := ufs.OpenFile(fsys, openFileName, exclusive, fs.ModePerm)
			if err == nil {
				ufsTesting.ValidateClose(t, f)()
				t.Fatalf("OpenFile(%q, O_EXCL) of an existing file = nil, want an error", openFileName)
			}
			if errors.Is(err, errors.ErrUnsupported) && !allFlags {
				openFileWant(t, fsys, string(before))
				return
			}
			if !errors.Is(err, fs.ErrExist) {
				t.Errorf("OpenFile(%q, O_EXCL) of an existing file = %v, want %v", openFileName, err, fs.ErrExist)
			}
			openFileWant(t, fsys, string(before))
		})

		for _, flag := range []int{os.O_RDONLY, replace, os.O_RDWR, os.O_WRONLY | os.O_APPEND} {
			if f, err := ufs.OpenFile(fsys, "../a.txt", flag, fs.ModePerm); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("OpenFile(../a.txt, %#x) = %v, %v, want %v", flag, f, err, fs.ErrInvalid)
			}
		}
	})
}

// openFileWrite opens the test file with flag and writes content to it.
func openFileWrite(t *testing.T, fsys types.WriteFS, flag int, content string) {
	t.Helper()
	f, err := ufs.OpenFile(fsys, openFileName, flag, fs.ModePerm)
	if err != nil {
		t.Fatalf("OpenFile(%q, %#x) = %v, want nil", openFileName, flag, err)
	}
	openFileWriteClose(t, f, content)
}

func openFileWriteClose(t *testing.T, f fs.File, content string) {
	t.Helper()
	wf, ok := f.(types.File)
	if !ok {
		ufsTesting.ValidateClose(t, f)()
		t.Fatalf("OpenFile(%q) for writing = %T, want a types.File", openFileName, f)
	}
	if n, err := wf.WriteString(content); err != nil || n != len(content) {
		t.Errorf("WriteString(%q) = %d, %v, want %d, nil", content, n, err, len(content))
	}
	if err := wf.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// openFileWant reads the test file through a read-only OpenFile and compares
// it with want.
func openFileWant(t *testing.T, fsys types.WriteFS, want string) {
	t.Helper()
	f, err := ufs.OpenFile(fsys, openFileName, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(%q, O_RDONLY) = %v, want nil", openFileName, err)
	}
	got, err := io.ReadAll(f)
	if err != nil {
		t.Errorf("ReadAll(%q) = %v, want nil", openFileName, err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	if string(got) != want {
		t.Errorf("content of %q = %q, want %q", openFileName, got, want)
	}
}
