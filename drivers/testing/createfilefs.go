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

package testing

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

// createFileNames are created by createFiles, each in a directory that has to
// be created first, and each holds its own name as its content.
var createFileNames = []string{"b/a/c", "b/b", "b/c", "c", "d/e/f/g", "d/e/g", "a/b/c/d/e/f/g"}

// createRefusedNames are the names Create must refuse: the shared invalidPaths
// list plus the invalid spellings written against the tree createFiles seeds.
var createRefusedNames = append(invalidPaths, "dir/", "dir//inner.txt", "./dir", "x/./y.txt")

// createFilesTestFS is the minimum set of interfaces createFiles needs: it
// seeds directories with MkdirAll, creates a file with Create, and reads the
// result back through ReadFile, Open, ReadLink and Lstat.
type createFilesTestFS interface {
	types.SimpleFS
	types.CreateFileFS
	types.MkdirAllFS
	fs.ReadFileFS
	fs.ReadLinkFS
}

// createReplacesTestFS is the minimum set of interfaces createReplaces needs:
// it writes the same name twice with Create and checks what the name holds.
type createReplacesTestFS interface {
	types.SimpleFS
	types.CreateFileFS
	types.MkdirAllFS
	fs.ReadFileFS
	fs.StatFS
}

// createHandleSizeTestFS is the minimum set of interfaces createHandleSize
// needs: it holds a Create handle open and checks the size it reports.
type createHandleSizeTestFS interface {
	types.SimpleFS
	types.CreateFileFS
	fs.ReadFileFS
	fs.StatFS
}

// createRefusedTestFS is the minimum set of interfaces createRefused needs: it
// asks Create for names that must be refused and checks that none is left.
type createRefusedTestFS interface {
	types.SimpleFS
	types.CreateFileFS
	fs.StatFS
}

// createReadOnlyTestFS is the minimum set of interfaces createReadOnly needs:
// it asks a read-only file system for a create and checks what the name holds
// afterwards, which needs no read interface beyond fs.FS.
type createReadOnlyTestFS interface {
	types.SimpleFS
	types.CreateFileFS
}

// createFileFS runs the types.CreateFileFS conformance suite.
func createFileFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("CreateFileFS", func(t *testing.T) {
		requireType[types.CreateFileFS](t, args)
		t.Parallel()
		if args.ReadOnly {
			createReadOnly(t, args, requireFSTypeFunc[createReadOnlyTestFS](t, createFSFunc))
			createRefused(t, args, requireFSTypeFunc[createRefusedTestFS](t, createFSFunc))
			return
		}
		createFiles(t, args, requireFSTypeFunc[createFilesTestFS](t, createFSFunc))
		createReplaces(t, args, requireFSTypeFunc[createReplacesTestFS](t, createFSFunc))
		createHandleSize(t, args, requireFSTypeFunc[createHandleSizeTestFS](t, createFSFunc))
		createRefused(t, args, requireFSTypeFunc[createRefusedTestFS](t, createFSFunc))
	})
}

// createFiles checks that a file created in a nested directory reads back
// through ReadFile, Open, and Lstat, and is not reported as a symlink.
func createFiles(t *testing.T, _ Args, createFSFunc func(t *testing.T) createFilesTestFS) {
	t.Run("Create", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		for _, filename := range createFileNames {
			t.Run(fmt.Sprintf("Create/%s", filename), func(t *testing.T) {
				dir := path.Dir(filename)
				if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
					t.Errorf("cannot Mkdir(%q), %s", dir, err)
				}

				t.Run("Create", func(t *testing.T) {
					f, err := fsys.Create(filename)
					if err != nil {
						t.Fatalf("Open(%q) failed, %s", filename, err)
					}
					bytesWritten, err := f.WriteString(filename)
					if err != nil {
						t.Errorf("WriteString(%q) failed to write, %s", filename, err)
					}
					if bytesWritten != len(filename) {
						t.Errorf("WriteString(%q) bytesWritten mismatch, want: %d, got: %d", filename, len(filename), bytesWritten)
					}
					if err := f.Close(); err != nil {
						t.Errorf("Close() got error, %s", err)
					}
				})

				t.Run("ReadFile", func(t *testing.T) {
					data, err := fsys.ReadFile(filename)
					if err != nil {
						t.Errorf("ReadFile(%q) got error, %s", filename, err)
					}
					gotData := string(data)
					if gotData != filename {
						t.Errorf("ReadFile(%q) mismatch, got: %q, want: %q", filename, gotData, filename)
					}
				})

				t.Run("Open", func(t *testing.T) {
					rf, err := fsys.Open(filename)
					if err != nil {
						t.Fatalf("Open(%q) got error, %s", filename, err)
					}
					data, err := io.ReadAll(rf)
					if err != nil {
						t.Errorf("io.ReadAll(%q) got error, %s", filename, err)
					}
					if err := rf.Close(); err != nil {
						t.Errorf("Close() got error, %s", err)
					}
					gotData := string(data)
					if gotData != filename {
						t.Errorf("io.ReadAll(%q) mismatch, got: %q, want: %q", filename, gotData, filename)
					}
				})

				t.Run("ReadLink", func(t *testing.T) {
					lfilename, err := fsys.ReadLink(filename)
					if err == nil {
						t.Errorf("ReadLink(%q) error was nil", filename)
					} else if _, ok := err.(*fs.PathError); !ok {
						t.Errorf("ReadLink(%q) error was not of type *fs.PathError, %s", filename, err)
					}
					if lfilename != "" {
						t.Errorf("ReadLink(%q) mismatch, got: %q, want: ''", filename, lfilename)
					}
				})

				t.Run("Lstat", func(t *testing.T) {
					stat, err := fsys.Lstat(filename)
					if err != nil {
						t.Errorf("Lstat(%q) got error, %s", filename, err)
					}
					if stat == nil {
						t.Errorf("Lstat(%q) returned nil FileInfo", filename)
					}
				})
			})
		}
	})
}

// createReplaces checks that Create replaces an existing file instead of
// appending to it or leaving the tail of the old content behind.
func createReplaces(t *testing.T, _ Args, createFSFunc func(t *testing.T) createReplacesTestFS) {
	t.Run("CreateReplaces", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		const name = "keep/replaced.txt"
		if err := fsys.MkdirAll("keep", fs.ModePerm); err != nil {
			t.Fatalf("MkdirAll(%q) = %v, want nil", "keep", err)
		}

		// The replacement is shorter than what it overwrites, so an implementation
		// that does not truncate leaves the tail of the old content in the file.
		oldContent := fmt.Sprintf("%020d", 1) + fmt.Sprintf("%020d", 2) + fmt.Sprintf("%020d", 3)
		WriteFile(t, fsys, name, oldContent)
		WriteFile(t, fsys, name, "b")

		got, err := fsys.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%q) = %v, want nil", name, err)
		}
		if diff := cmp.Diff("b", string(got)); diff != "" {
			t.Errorf("ReadFile(%q) content mismatch (-want +got):\n%s", name, diff)
		}

		info, err := fsys.Stat(name)
		if err != nil {
			t.Fatalf("Stat(%q) = %v, want nil", name, err)
		}
		if info.Size() != int64(len("b")) {
			t.Errorf("Stat(%q).Size() = %d after a %d byte replacement, want %d with none of the %d bytes it replaced left in the file", name, info.Size(), len("b"), len("b"), len(oldContent))
		}
		if info.Name() != path.Base(name) {
			t.Errorf("Stat(%q).Name() = %q, want %q", name, info.Name(), path.Base(name))
		}
		if info.IsDir() {
			t.Errorf("Stat(%q).IsDir() = true, want a regular file", name)
		}
	})
}

// createHandleSize checks what a Create handle reports as it is written and
// closed, which catches a file system that never flushes what it buffers.
func createHandleSize(t *testing.T, _ Args, createFSFunc func(t *testing.T) createHandleSizeTestFS) {
	t.Run("CreateHandleSize", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		const name = "handle_size.txt"
		const content = "hello world"
		f, err := fsys.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) = %v, want nil", name, err)
		}

		info, err := f.Stat()
		if err != nil {
			t.Fatalf("Stat on the Create handle for %q = %v, want nil", name, err)
		}
		if info.Size() != 0 {
			t.Errorf("the handle from Create(%q) is %d bytes before anything is written, want 0", name, info.Size())
		}

		if _, err := f.WriteString(content); err != nil {
			t.Fatalf("WriteString(%q) = %v, want nil", name, err)
		}

		// What the handle reports mid-write is not checked: measuring the drivers
		// shows *core.memFS, *core.localFS and *boltfs.boltFS return 11 for those
		// bytes while *gcsfs.gcsFS returns 0 because it uploads at Close, so the
		// contract says nothing until the handle is closed.
		if err := f.Close(); err != nil {
			t.Fatalf("Close on the handle from Create(%q) = %v, want nil", name, err)
		}

		info, err = fsys.Stat(name)
		if err != nil {
			t.Fatalf("Stat(%q) after closing its handle = %v, want nil", name, err)
		}
		if info.Size() != int64(len(content)) {
			t.Errorf("Stat(%q).Size() = %d after closing its handle, want %d", name, info.Size(), len(content))
		}
		got, err := fsys.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%q) after closing its handle = %v, want nil", name, err)
		}
		if diff := cmp.Diff(content, string(got)); diff != "" {
			t.Errorf("ReadFile(%q) mismatch after closing its handle (-want +got):\n%s", name, diff)
		}
	})
}

// createRefused checks the names Create must refuse with fs.ErrInvalid and that
// a refused Create leaves no node behind.
func createRefused(t *testing.T, _ Args, createFSFunc func(t *testing.T) createRefusedTestFS) {
	t.Run("CreateRefused", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		for _, name := range createRefusedNames {
			f, err := fsys.Create(name)
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Create(%q) = %v, want an error wrapping %v", name, err, fs.ErrInvalid)
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("Create(%q) = %T, want an *fs.PathError", name, err)
			} else if pathErr.Op != "create" {
				t.Errorf("Create(%q) Op = %q, want create", name, pathErr.Op)
			}
			if f != nil {
				t.Errorf("Create(%q) returned a handle with an error, want nil", name)
				if err := f.Close(); err != nil {
					t.Errorf("Close after a refused create of %q = %v, want nil", name, err)
				}
			}

			if info, serr := fsys.Stat(name); serr == nil {
				t.Errorf("Stat(%q) after a refused create = nil, want an error: the create left %q behind", name, info.Name())
			}
		}
	})
}

// createReadOnly checks Create on a file system declared read-only through
// Args.ReadOnly: it either refuses the create or discards it, but the bytes
// never become readable.
func createReadOnly(t *testing.T, _ Args, createFSFunc func(t *testing.T) createReadOnlyTestFS) {
	t.Run("CreateReadOnly", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		const name = "createfilefs_read_only.txt"
		const content = "written through Create"

		f, err := fsys.Create(name)
		if err != nil {
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("Create(%q) = %T %v, want an *fs.PathError", name, err, err)
			} else if pathErr.Op != "create" {
				t.Errorf("Create(%q) Op = %q, want create", name, pathErr.Op)
			}
			if !errors.Is(err, fs.ErrPermission) {
				t.Errorf("Create(%q) = %v, want an error wrapping %v", name, err, fs.ErrPermission)
			}
			if f != nil {
				t.Errorf("Create(%q) returned a handle with an error, want nil", name)
				if err := f.Close(); err != nil {
					t.Errorf("Close after the refused create of %q = %v, want nil", name, err)
				}
			}
		} else {
			// A read-only file system may accept the create only to discard it, so
			// the handle itself has to take the write.
			if _, err := f.WriteString(content); err != nil {
				t.Errorf("WriteString on the handle from Create(%q) = %v, want nil: a read-only file system either refuses Create or discards it", name, err)
			}
			if err := f.Close(); err != nil {
				t.Errorf("Close on the handle from Create(%q) = %v, want nil", name, err)
			}
		}

		got, err := fs.ReadFile(fsys, name)
		if err == nil && string(got) == content {
			t.Errorf("ReadFile(%q) = %q after Create on a read-only file system, want the create refused or discarded", name, got)
		}
	})
}
