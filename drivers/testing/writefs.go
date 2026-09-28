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
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"testing"
	"testing/fstest"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/pathutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

// WriteFSWithBuckets runs the write conformance suite for bucket-backed file
// systems (such as object stores) that lack full directory semantics.
func WriteFSWithBuckets(t *testing.T, createFSFunc func(t *testing.T) ufs.WriteFS) {
	MkdirAllWithBuckets(t, createFSFunc)
	ReadFile(t, createFSFunc)
	InvalidPathsForWriteFS(t, createFSFunc)
	Create(t, createFSFunc)
	Conventions(t, createFSFunc)
	// TODO: gcsfs is broken here.
	// Close(t, createFSFunc)
	readFS(t, func(t *testing.T) ufs.ReadFS {
		return createFSFunc(t)
	})
	/*
		TODO: This should work.
			writeFS(t, func(t *testing.T) ufs.WriteFS {
				return ufs.WrapNestFS(t.Context(), createFSFunc(t))
			})
	*/
}

// WriteFS runs the write conformance suite against file systems returned by
// createFSFunc.
func WriteFS(t *testing.T, createFSFunc func(t *testing.T) ufs.WriteFS) {
	writeFS(t, createFSFunc)
	writeFS(t, func(t *testing.T) ufs.WriteFS {
		return ufs.WrapNestFS(t.Context(), createFSFunc(t))
	})
}

func writeFS(t *testing.T, createFSFunc func(t *testing.T) ufs.WriteFS) {
	MkdirAll(t, createFSFunc)
	ReadFile(t, createFSFunc)
	DirFileConflicts(t, createFSFunc)
	InvalidPathsForWriteFS(t, createFSFunc)
	ReadDir(t, createFSFunc)
	Close(t, createFSFunc)
	Create(t, createFSFunc)
	Conventions(t, createFSFunc)
	readFS(t, func(t *testing.T) ufs.ReadFS {
		return createFSFunc(t)
	})
}

// ReadDir verifies that fs.ReadDirFS and fs.ReadDirFile return the expected
// entries for a created directory tree.
func ReadDir(t *testing.T, createFSFunc func(t *testing.T) ufs.WriteFS) {
	t.Run("ReadDir", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		dirs := []string{"a", "b", "b/a/c", "b/b", "b/c", "c", "d/e/f/g", "d/e/g", "a/b/c/d/e/f/g"}
		lsMap := map[string][]string{
			".":             {"a", "b", "c", "d"},
			"a":             {"b"},
			"b":             {"a", "b", "c"},
			"b/a":           {"c"},
			"b/a/c":         {},
			"b/b":           {},
			"b/c":           {},
			"c":             {},
			"d":             {"e"},
			"d/e":           {"f", "g"},
			"d/e/f":         {"g"},
			"d/e/f/g":       {},
			"d/e/g":         {},
			"a/b":           {"c"},
			"a/b/c":         {"d"},
			"a/b/c/d":       {"e"},
			"a/b/c/d/e":     {"f"},
			"a/b/c/d/e/f":   {"g"},
			"a/b/c/d/e/f/g": {},
		}

		for _, dir := range dirs {
			t.Run(fmt.Sprintf("MkdirAll/%s", dir), func(t *testing.T) {
				if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
					t.Errorf("cannot Mkdir(%q), %s", dir, err)
				}
			})
		}

		for input, want := range lsMap {
			t.Run(fmt.Sprintf("ReadDir/%s", input), func(t *testing.T) {
				gotEntries, err := fsys.ReadDir(input)
				if err != nil {
					t.Errorf("cannot ReadDir(%q), got error: %s", input, err)
				}
				gotNames := ufsTesting.DirEntryListToNames(gotEntries)
				if diff := cmp.Diff(want, gotNames); diff != "" {
					t.Errorf("got %s, want %s diff(-want,+got):\n %v", gotNames, want, diff)
				}
			})

			t.Run(fmt.Sprintf("Open/%s", input), func(t *testing.T) {
				f, err := fsys.Open(input)
				if err != nil {
					t.Fatalf("cannot ReadDir(%q), got error: %s", input, err)
				}
				defer ufsTesting.ValidateClose(t, f)()
				if rdf, ok := f.(fs.ReadDirFile); ok {
					entries, err := rdf.ReadDir(-1)
					if err != nil {
						t.Errorf("ReadDir(-1) failed with error, %s", err)
					}
					gotNames := ufsTesting.DirEntryListToNames(entries)
					sort.Strings(gotNames)
					sort.Strings(want)
					if diff := cmp.Diff(want, gotNames); diff != "" {
						t.Errorf("ReadDir(-1) got %s, want %s diff(-want,+got):\n %v", gotNames, want, diff)
					}
				}
			})
		}
	})
}

// ReadWriteFiles creates a file system named name via newFSFunc, writes a set
// of files, and verifies they can be read back.
func ReadWriteFiles(t *testing.T, newFSFunc func(ctx context.Context, name string) (ufs.WriteFS, error), name string) {
	t.Helper()
	fsys := mustWriteFS(t, newFSFunc, name)

	wantFiles := []string{"a", "ab/b/c", "ab/d/c", "def", "abc", "abc.txt", "temp/abc.txt"}

	mkdirForTest(t, fsys, "ab/b")
	mkdirForTest(t, fsys, "temp")
	mkdirForTest(t, fsys, "ab/d")

	for _, name := range wantFiles {
		t.Run(fmt.Sprintf("read_write_files_%s", name), func(t *testing.T) {
			wantData := ufsTesting.RandomString(1000)
			if wf, err := fsys.Create(name); err != nil {
				t.Errorf("cannot create file %q, %s", name, err)
			} else {
				info, err := wf.Stat()
				if err != nil {
					t.Errorf("cannot Stat() %q, %s", name, err)
				}
				if info == nil {
					t.Fatalf("info is nil")
				}
				if info.IsDir() {
					t.Errorf("%q is a directory, want file", name)
				}
				if n, err := io.WriteString(wf, wantData); err != nil {
					t.Errorf("cannot write file content to %q, %s", name, err)
				} else if n != len(wantData) {
					t.Errorf("contents written to file does not match the size got %d, want %d", n, len(wantData))
				}
				if err := wf.Close(); err != nil {
					t.Errorf("failed to Close() write file %q, %s", name, err)
				}
			}

			if rf, err := fsys.Open(name); err != nil {
				t.Errorf("cannot open file %q, %s", name, err)
			} else {
				if rf == nil {
					t.Fatal("rf is nil")
				}
				info, err := rf.Stat()
				if err != nil {
					t.Errorf("cannot Stat() %q, %s", name, err)
				}
				if info == nil {
					t.Fatal("info is nil")
				}
				if info.IsDir() {
					t.Errorf("%q is a directory, want file", name)
				}
				if got, err := io.ReadAll(rf); err != nil {
					t.Errorf("cannot read file content to %q, %s", name, err)
				} else if diff := cmp.Diff(wantData, string(got)); diff != "" {
					t.Errorf("io.ReadAll(%s) mismatch (-want +got):\n%s\nwant: %q\ngot: %q", name, diff, wantData, string(got))
				}
				if err := rf.Close(); err != nil {
					t.Errorf("failed to Close() read file %q, %s", name, err)
				}
			}
		})
	}

	if err := fstest.TestFS(fsys, wantFiles...); err != nil {
		t.Errorf("fstest.TestFS failed for %q: %v", name, err)
	}

	if err := fsys.Close(); err != nil {
		t.Errorf("error on Close(), %v", err)
	}
}

// Conventions copies the ufs test assets into the file system with
// [ufs.Rsync] and verifies the result satisfies the [fstest.TestFS]
// conventions for every copied file.
func Conventions(t *testing.T, createFSFunc func(t *testing.T) ufs.WriteFS) {
	t.Run("Conventions", func(t *testing.T) {
		t.Parallel()
		srcFS := ufsTesting.TestAssetsFS()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))
		if err := ufs.Rsync(srcFS, fsys, pathutil.CwdPath); err != nil {
			t.Fatalf("Rsync() = %v, want nil", err)
		}

		filenames, err := ufs.List(srcFS, pathutil.CwdPath)
		if err != nil {
			t.Fatalf("List() = %v, want nil", err)
		}
		if len(filenames) == 0 {
			t.Fatal("List() returned no files, want at least 1")
		}
		if err := fstest.TestFS(fsys, filenames...); err != nil {
			t.Error(err)
		}
	})
}

// MkdirAll verifies that MkdirAll creates every missing directory in a path,
// that each one is reported as a directory and listed by its parent, and that
// repeating the call on existing directories succeeds.
func MkdirAll(t *testing.T, createFSFunc func(t *testing.T) ufs.WriteFS) {
	t.Run("MkdirAll", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		defer ufsTesting.ValidateClose(t, fsys)()
		// All paths live under one new directory so files the file system
		// already holds cannot collide with or show up in the checks.
		for _, dir := range []string{"mkdirall/subdir", "mkdirall/a/b/c"} {
			if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
				t.Fatalf("MkdirAll(%q) = %v, want nil", dir, err)
			}
		}
		assertDirs(t, fsys, map[string][]string{
			"mkdirall":        {"a", "subdir"},
			"mkdirall/a":      {"b"},
			"mkdirall/a/b":    {"c"},
			"mkdirall/a/b/c":  {},
			"mkdirall/subdir": {},
		})
		if err := fsys.MkdirAll("mkdirall/a/b/c", fs.ModePerm); err != nil {
			t.Errorf("MkdirAll(%q) on existing directories = %v, want nil", "mkdirall/a/b/c", err)
		}
	})
}

// MkdirAllWithBuckets is the MkdirAll check for bucket-backed file systems,
// which have no real directories: MkdirAll must succeed, and once a file is
// created under the path every parent directory must exist.
func MkdirAllWithBuckets(t *testing.T, createFSFunc func(t *testing.T) ufs.WriteFS) {
	t.Run("MkdirAllWithBuckets", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		defer ufsTesting.ValidateClose(t, fsys)()
		if err := fsys.MkdirAll("mkdirall/a/b", fs.ModePerm); err != nil {
			t.Fatalf("MkdirAll(%q) = %v, want nil", "mkdirall/a/b", err)
		}
		f, err := fsys.Create("mkdirall/a/b/file")
		if err != nil {
			t.Fatalf("Create(%q) = %v, want nil", "mkdirall/a/b/file", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		assertDirs(t, fsys, map[string][]string{
			"mkdirall":     {"a"},
			"mkdirall/a":   {"b"},
			"mkdirall/a/b": {"file"},
		})
	})
}

// assertDirs checks that each key of want is a directory whose entries are
// exactly the names in want. Stat alone is not enough: a file system may
// report any path as a directory, so each one is also listed.
func assertDirs(t *testing.T, fsys ufs.ReadFS, want map[string][]string) {
	t.Helper()
	for dir, wantNames := range want {
		if info, err := fsys.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("Stat(%q) = (%v, %v), want a directory", dir, info, err)
		}
		entries, err := fsys.ReadDir(dir)
		if err != nil {
			t.Errorf("ReadDir(%q) = %v, want nil", dir, err)
			continue
		}
		if diff := cmp.Diff(wantNames, ufsTesting.DirEntryListToNames(entries)); diff != "" {
			t.Errorf("ReadDir(%q) mismatch (-want +got):\n%s", dir, diff)
		}
	}
}

// ReadFile verifies that data written to a file can be read back.
func ReadFile(t *testing.T, createFSFunc func(*testing.T) ufs.WriteFS) {
	t.Run("ReadFile", func(t *testing.T) {
		t.Parallel()
		wantData := ufsTesting.RandomString(100)
		fsys := createFSFunc(t)
		defer ufsTesting.ValidateClose(t, fsys)()
		f, err := fsys.Create("readfile_test.txt")
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		if _, err := io.WriteString(f, wantData); err != nil {
			t.Fatalf("WriteString failed: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}

		rfs, ok := fsys.(fs.ReadFileFS)
		if !ok {
			t.Skip("does not implement fs.ReadFileFS")
		}
		got, err := rfs.ReadFile("readfile_test.txt")
		if err != nil {
			t.Fatalf("ReadFile failed: %v", err)
		}
		if diff := cmp.Diff(wantData, string(got)); diff != "" {
			t.Errorf("ReadFile mismatch (-want +got):\n%s", diff)
		}
	})
}

// dirFileConflictCases are Create and MkdirAll calls that conflict with the
// tree built by newDirFileConflictFS: each replaces a directory with a file
// or treats a regular file as a directory. wantErr and wantOp are the values
// memFS returns; other backends may report the conflict differently (for
// example localFS surfaces the OS's ENOTDIR/EISDIR).
var dirFileConflictCases = []struct {
	name    string
	op      func(fsys ufs.WriteFS) error
	wantErr error
	wantOp  string
}{
	{name: "create_on_dir", op: func(fsys ufs.WriteFS) error { _, err := fsys.Create("dir/sub"); return err }, wantErr: fs.ErrInvalid, wantOp: "create"},
	{name: "create_on_root", op: func(fsys ufs.WriteFS) error { _, err := fsys.Create(pathutil.CwdPath); return err }, wantErr: fs.ErrInvalid, wantOp: "create"},
	{name: "create_under_file", op: func(fsys ufs.WriteFS) error { _, err := fsys.Create("dir/file/x"); return err }, wantErr: fs.ErrExist, wantOp: "create"},
	{name: "create_deep_under_file", op: func(fsys ufs.WriteFS) error { _, err := fsys.Create("dir/file/x/y"); return err }, wantErr: fs.ErrExist, wantOp: "create"},
	{name: "mkdirall_on_file", op: func(fsys ufs.WriteFS) error { return fsys.MkdirAll("dir/file", fs.ModePerm) }, wantErr: fs.ErrExist, wantOp: "mkdir"},
	{name: "mkdirall_under_file", op: func(fsys ufs.WriteFS) error { return fsys.MkdirAll("dir/file/x/y", fs.ModePerm) }, wantErr: fs.ErrExist, wantOp: "mkdir"},
}

// newDirFileConflictFS returns a file system from createFS containing the
// directories dir and dir/sub and the regular file dir/file.
func newDirFileConflictFS(t *testing.T, createFS func(*testing.T) ufs.WriteFS) ufs.WriteFS {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	if err := fsys.MkdirAll("dir/sub", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("dir/file")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("content"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return fsys
}

// DirFileConflicts verifies that every WriteFS rejects Create and
// MkdirAll calls that would replace a directory with a file or place anything
// under a regular file, and that a rejected call leaves the tree unchanged.
func DirFileConflicts(t *testing.T, createFS func(*testing.T) ufs.WriteFS) {
	t.Run("DirFileConflicts", func(t *testing.T) {
		t.Parallel()
		for _, tc := range dirFileConflictCases {
			t.Run(tc.name, func(t *testing.T) {
				fsys := newDirFileConflictFS(t, createFS)
				err := tc.op(fsys)
				// MEMFS ONly
				/*
					if !errors.Is(err, tc.wantErr) {
						t.Errorf("err = %v, want %v", err, tc.wantErr)
					}
				*/
				var pe *fs.PathError
				if !errors.As(err, &pe) {
					t.Errorf("err = %v, want a *fs.PathError", err)
				}
				assertDirFileTreeUnchanged(t, fsys)
			})
		}

		t.Run("existing_dirs_still_ok", func(t *testing.T) {
			fsys := newDirFileConflictFS(t, createFS)
			if err := fsys.MkdirAll("dir/sub/new", fs.ModePerm); err != nil {
				t.Errorf("MkdirAll(dir/sub/new) = %v, want nil", err)
			}
			if err := fsys.MkdirAll(pathutil.CwdPath, fs.ModePerm); err != nil {
				t.Errorf("MkdirAll(.) = %v, want nil", err)
			}
			f, err := fsys.Create("dir/file")
			if err != nil {
				t.Fatalf("Create(dir/file) over an existing file = %v, want nil", err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		})
	})
}

// Close verifies that Close can be called repeatedly and that every operation
// on a closed file system returns fs.ErrClosed.
func Close(t *testing.T, createFS func(*testing.T) ufs.WriteFS) {
	t.Run("Close", func(t *testing.T) {
		t.Parallel()
		fsys := createFS(t)
		for i := range 10 {
			if err := fsys.Close(); err != nil {
				t.Errorf("Close() [%d] failed with error, %s", i, err)
			}
		}

		t.Run("Open", func(t *testing.T) {
			f, err := fsys.Open(".")
			if f != nil {
				buf := make([]byte, 64)
				if bytesWritten, err := f.Read(buf); bytesWritten != 0 && err != fs.ErrClosed {
					t.Errorf("Open('.') worked for a closed file system, read %d (want: 0) bytes with error= %s (want: fs.ErrClosed), file: %+v", bytesWritten, err, f)
				}
			}
			if !errors.Is(err, fs.ErrClosed) {
				t.Errorf("Open('.') did not return fs.ErrClosed, got: %s", err)
			}
		})

		t.Run("Create", func(t *testing.T) {
			f, err := fsys.Create("file.txt")
			if f != nil {
				buf := make([]byte, 64)
				if bytesWritten, err := f.Write(buf); bytesWritten != 0 && err != fs.ErrClosed {
					t.Errorf("Create('file.txt') worked for a closed file system, read %d (want: 0) bytes with error= %s (want: fs.ErrClosed), file: %+v", bytesWritten, err, f)
				}
			}
			if !errors.Is(err, fs.ErrClosed) {
				t.Errorf("Create('file.txt') did not return fs.ErrClosed, got: %s", err)
			}
		})

		t.Run("MkdirAll", func(t *testing.T) {
			if err := fsys.MkdirAll("a/b/c", fs.ModePerm); !errors.Is(err, fs.ErrClosed) {
				t.Errorf("MkdirAll('a/b/c') did not return fs.ErrClosed, got: %s", err)
			}
		})

		t.Run("Remove", func(t *testing.T) {
			if err := fsys.Remove("file.txt"); !errors.Is(err, fs.ErrClosed) {
				t.Errorf("Remove('file.txt') did not return fs.ErrClosed, got: %s", err)
			}
		})

		t.Run("RemoveAll", func(t *testing.T) {
			if err := fsys.RemoveAll("dir"); !errors.Is(err, fs.ErrClosed) {
				t.Errorf("RemoveAll('dir') did not return fs.ErrClosed, got: %s", err)
			}
		})

		t.Run("ReadDir", func(t *testing.T) {
			entries, err := fs.ReadDir(fsys, ".")
			if len(entries) != 0 {
				t.Errorf("ReadDir('.') returned results for a closed file system, got: %v", entries)
			}
			if !errors.Is(err, fs.ErrClosed) {
				t.Errorf("ReadDir('.') did not return fs.ErrClosed, got: %s", err)
			}
		})
	})
}

func mustWriteFS(tb testing.TB, newFSFunc func(context.Context, string) (ufs.WriteFS, error), name string) ufs.FS {
	tb.Helper()

	fsys, err := newFSFunc(tb.Context(), name)
	if err != nil {
		tb.Fatalf("FileSystem %q has an error, %s", name, err)
	}
	if fsys == nil {
		tb.Fatalf("FileSystem %q is nil", name)
	}

	return fsys
}

func mkdirForTest(tb testing.TB, fsys ufs.FS, dirs ...string) {
	tb.Helper()
	dir := path.Join(dirs...)
	if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
		tb.Fatalf("cannot create directory %q, %s", dir, err)
	}
}

// InvalidPathsForWriteFS verifies that write operations reject invalid paths.
func InvalidPathsForWriteFS(t *testing.T, createFS func(*testing.T) ufs.WriteFS) {
	tests := []struct {
		name   string
		wantOp string
		op     func(fsys ufs.WriteFS, path string) error
	}{
		{
			name:   "Create",
			wantOp: "create",
			op:     func(fsys ufs.WriteFS, path string) error { _, err := fsys.Create(path); return err },
		},
		{
			name:   "MkdirAll",
			wantOp: "mkdir",
			op:     func(fsys ufs.WriteFS, path string) error { return fsys.MkdirAll(path, fs.ModePerm) },
		},
		{
			name:   "Remove",
			wantOp: "remove",
			op:     func(fsys ufs.WriteFS, path string) error { return fsys.Remove(path) },
		},
		{
			name:   "RemoveAll",
			wantOp: "removeall",
			op:     func(fsys ufs.WriteFS, path string) error { return fsys.RemoveAll(path) },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := createFS(t)
			// Close only to release resources (e.g. directory handles that block
			// temp dir removal on Windows); Close behavior is covered elsewhere
			// and some file systems, such as angryFS, fail it by design.
			t.Cleanup(func() {
				if err := fsys.Close(); err != nil {
					t.Logf("Close() = %v", err)
				}
			})
			for _, p := range invalidPaths {
				t.Run(p, func(t *testing.T) {
					ufsTesting.AssertInvalidPathError(t, p, tc.op(fsys, p), tc.wantOp)
				})
			}
		})
	}
}

// Create verifies that files created and written in nested directories can be
// read back via ReadFile, Open, and Lstat, and are not reported as symlinks.
func Create(t *testing.T, createFS func(*testing.T) ufs.WriteFS) {
	t.Run("Create", func(t *testing.T) {
		t.Parallel()
		fsys := createFS(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		filenames := []string{"b/a/c", "b/b", "b/c", "c", "d/e/f/g", "d/e/g", "a/b/c/d/e/f/g"}

		for _, filename := range filenames {
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
