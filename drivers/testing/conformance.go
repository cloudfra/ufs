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
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

// RoundTrip creates a small tree of directories and files, verifies each
// file's content reads back through Open (and ReadFile when implemented), and
// then runs [fstest.TestFS] over the result. Finally it closes the file
// system if it implements [io.Closer].
func RoundTrip[F WritableFile](t *testing.T, newFS Factory) {
	t.Helper()
	fsys := newFS(t)
	wfs := requireWrite[F](t, fsys)

	wantFiles := []string{"a", "ab/b/c", "ab/d/c", "def", "abc", "abc.txt", "temp/abc.txt"}
	for _, dir := range []string{"ab/b", "temp", "ab/d"} {
		if err := wfs.MkdirAll(dir, fs.ModePerm); err != nil {
			t.Fatalf("MkdirAll(%q) = %v", dir, err)
		}
	}

	for _, name := range wantFiles {
		t.Run("crud_"+name, func(t *testing.T) {
			wantData := ufsTesting.RandomString(1000)
			wf, err := wfs.Create(name)
			if err != nil {
				t.Fatalf("Create(%q) = %v", name, err)
			}
			if info, err := wf.Stat(); err != nil {
				t.Errorf("Stat() of created %q = %v", name, err)
			} else if info.IsDir() {
				t.Errorf("created %q is a directory, want file", name)
			}
			if n, err := io.WriteString(wf, wantData); err != nil || n != len(wantData) {
				t.Errorf("WriteString(%q) = (%d, %v), want (%d, nil)", name, n, err, len(wantData))
			}
			if err := wf.Close(); err != nil {
				t.Errorf("Close() of written %q = %v", name, err)
			}

			rf, err := fsys.Open(name)
			if err != nil {
				t.Fatalf("Open(%q) = %v", name, err)
			}
			defer ufsTesting.ValidateClose(t, rf)()
			if info, err := rf.Stat(); err != nil {
				t.Errorf("Stat() of opened %q = %v", name, err)
			} else if info.IsDir() {
				t.Errorf("opened %q is a directory, want file", name)
			}
			if got, err := io.ReadAll(rf); err != nil {
				t.Errorf("io.ReadAll(%q) = %v", name, err)
			} else if diff := cmp.Diff(wantData, string(got)); diff != "" {
				t.Errorf("io.ReadAll(%q) mismatch (-want +got):\n%s", name, diff)
			}
			if got, err := readFile(t, fsys, name); err != nil || string(got) != wantData {
				t.Errorf("ReadFile(%q) = (%d bytes, %v), want the %d bytes written", name, len(got), err, len(wantData))
			}
		})
	}

	if err := fstest.TestFS(fsys, wantFiles...); err != nil {
		t.Errorf("fstest.TestFS() = %v", err)
	}

	if c, ok := fsys.(io.Closer); ok {
		if err := c.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	}
}

// invalidPaths are names that fs.ValidPath rejects.
var invalidPaths = []string{
	"/absolute/path",
	"../relative/path",
	"invalid/../path",
	"",
}

// InvalidPaths verifies that every implemented operation rejects names that
// are not [fs.ValidPath] with a *fs.PathError naming the operation and path.
func InvalidPaths[F WritableFile](t *testing.T, newFS Factory) {
	t.Helper()
	type op struct {
		name   string
		wantOp string
		call   func(fsys fs.FS, name string) (error, bool)
	}
	ops := []op{
		{name: "Open", wantOp: "open", call: func(fsys fs.FS, name string) (error, bool) {
			_, err := fsys.Open(name)
			return err, true
		}},
		{name: "ReadDir", wantOp: "readdir", call: func(fsys fs.FS, name string) (error, bool) {
			rfs, ok := fsys.(fs.ReadDirFS)
			if !ok {
				return nil, false
			}
			_, err := rfs.ReadDir(name)
			return err, true
		}},
		{name: "ReadFile", wantOp: "readfile", call: func(fsys fs.FS, name string) (error, bool) {
			rfs, ok := fsys.(fs.ReadFileFS)
			if !ok {
				return nil, false
			}
			_, err := rfs.ReadFile(name)
			return err, true
		}},
		{name: "ReadLink", wantOp: "readlink", call: func(fsys fs.FS, name string) (error, bool) {
			lfs, ok := fsys.(fs.ReadLinkFS)
			if !ok {
				return nil, false
			}
			_, err := lfs.ReadLink(name)
			return err, true
		}},
		{name: "Lstat", wantOp: "lstat", call: func(fsys fs.FS, name string) (error, bool) {
			lfs, ok := fsys.(fs.ReadLinkFS)
			if !ok {
				return nil, false
			}
			_, err := lfs.Lstat(name)
			return err, true
		}},
		{name: "Create", wantOp: "create", call: func(fsys fs.FS, name string) (error, bool) {
			cfs, ok := fsys.(CreateFS[F])
			if !ok {
				return nil, false
			}
			_, err := cfs.Create(name)
			return err, true
		}},
		{name: "MkdirAll", wantOp: "mkdir", call: func(fsys fs.FS, name string) (error, bool) {
			mfs, ok := fsys.(MkdirAllFS)
			if !ok {
				return nil, false
			}
			return mfs.MkdirAll(name, fs.ModeDir), true
		}},
		{name: "Remove", wantOp: "remove", call: func(fsys fs.FS, name string) (error, bool) {
			rfs, ok := fsys.(RemoveFS)
			if !ok {
				return nil, false
			}
			return rfs.Remove(name), true
		}},
		{name: "RemoveAll", wantOp: "removeall", call: func(fsys fs.FS, name string) (error, bool) {
			rfs, ok := fsys.(RemoveFS)
			if !ok {
				return nil, false
			}
			return rfs.RemoveAll(name), true
		}},
	}
	for _, o := range ops {
		for _, name := range invalidPaths {
			t.Run(fmt.Sprintf("%s/%s", o.name, name), func(t *testing.T) {
				t.Parallel()
				err, ok := o.call(newFS(t), name)
				if !ok {
					t.Skipf("file system does not implement %s", o.name)
				}
				ufsTesting.AssertInvalidPathError(t, name, err, o.wantOp)
			})
		}
	}
}

// Close verifies that closing a file system is idempotent and that every
// implemented operation on a closed file system fails with [fs.ErrClosed].
// It is skipped if the file system does not implement [io.Closer].
func Close[F WritableFile](t *testing.T, newFS Factory) {
	t.Helper()
	fsys := newFS(t)
	c, ok := fsys.(io.Closer)
	if !ok {
		t.Skipf("%T does not implement io.Closer", fsys)
	}
	for i := range 10 {
		if err := c.Close(); err != nil {
			t.Errorf("Close() #%d = %v, want nil", i+1, err)
		}
	}

	t.Run("Open", func(t *testing.T) {
		f, err := fsys.Open(".")
		if f != nil {
			buf := make([]byte, 64)
			if n, err := f.Read(buf); n != 0 && err != fs.ErrClosed {
				t.Errorf("Read() on a file from a closed file system = (%d, %v), want (0, fs.ErrClosed)", n, err)
			}
		}
		if !errors.Is(err, fs.ErrClosed) {
			t.Errorf("Open(.) = %v, want fs.ErrClosed", err)
		}
	})

	t.Run("ReadDir", func(t *testing.T) {
		entries, err := fs.ReadDir(fsys, ".")
		if len(entries) != 0 {
			t.Errorf("ReadDir(.) on a closed file system returned %v, want none", entries)
		}
		if !errors.Is(err, fs.ErrClosed) {
			t.Errorf("ReadDir(.) = %v, want fs.ErrClosed", err)
		}
	})

	t.Run("Create", func(t *testing.T) {
		cfs, ok := fsys.(CreateFS[F])
		if !ok {
			t.Skipf("%T does not implement Create", fsys)
		}
		f, err := cfs.Create("file.txt")
		if !errors.Is(err, fs.ErrClosed) {
			t.Errorf("Create(file.txt) = %v, want fs.ErrClosed", err)
		}
		if err == nil {
			buf := make([]byte, 64)
			if n, err := f.Write(buf); n != 0 && err != fs.ErrClosed {
				t.Errorf("Write() on a file from a closed file system = (%d, %v), want (0, fs.ErrClosed)", n, err)
			}
		}
	})

	t.Run("MkdirAll", func(t *testing.T) {
		mfs, ok := fsys.(MkdirAllFS)
		if !ok {
			t.Skipf("%T does not implement MkdirAll", fsys)
		}
		if err := mfs.MkdirAll("a/b/c", fs.ModePerm); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("MkdirAll(a/b/c) = %v, want fs.ErrClosed", err)
		}
	})

	t.Run("Remove", func(t *testing.T) {
		rfs, ok := fsys.(RemoveFS)
		if !ok {
			t.Skipf("%T does not implement Remove", fsys)
		}
		if err := rfs.Remove("file.txt"); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("Remove(file.txt) = %v, want fs.ErrClosed", err)
		}
		if err := rfs.RemoveAll("dir"); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("RemoveAll(dir) = %v, want fs.ErrClosed", err)
		}
	})
}

// MkdirAll verifies that a directory can be created.
func MkdirAll(t *testing.T, newFS Factory) {
	t.Helper()
	mfs := requireMkdirAll(t, newFS(t))
	if err := mfs.MkdirAll("subdir", fs.ModePerm); err != nil {
		t.Errorf("MkdirAll(subdir) = %v, want nil", err)
	}
}

// ReadDir creates a tree of nested directories and verifies that
// [fs.ReadDir] and [fs.ReadDirFile.ReadDir] list each directory's children
// in sorted order.
func ReadDir(t *testing.T, newFS Factory) {
	t.Helper()
	fsys := newFS(t)
	mfs := requireMkdirAll(t, fsys)

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
		if err := mfs.MkdirAll(dir, fs.ModePerm); err != nil {
			t.Fatalf("MkdirAll(%q) = %v", dir, err)
		}
	}

	for input, want := range lsMap {
		t.Run("ReadDir/"+input, func(t *testing.T) {
			// fs.ReadDir calls fsys.ReadDir directly when it implements
			// fs.ReadDirFS, so an unsorted result is caught here.
			entries, err := fs.ReadDir(fsys, input)
			if err != nil {
				t.Errorf("ReadDir(%q) = %v", input, err)
			}
			if diff := cmp.Diff(want, ufsTesting.DirEntryListToNames(entries)); diff != "" {
				t.Errorf("ReadDir(%q) mismatch (-want +got):\n%s", input, diff)
			}
		})

		t.Run("Open/"+input, func(t *testing.T) {
			f, err := fsys.Open(input)
			if err != nil {
				t.Fatalf("Open(%q) = %v", input, err)
			}
			defer ufsTesting.ValidateClose(t, f)()
			rdf, ok := f.(fs.ReadDirFile)
			if !ok {
				t.Skipf("directory %q does not implement fs.ReadDirFile", input)
			}
			entries, err := rdf.ReadDir(-1)
			if err != nil {
				t.Errorf("ReadDir(-1) = %v", err)
			}
			// fs.ReadDirFile.ReadDir does not promise any order.
			got := ufsTesting.DirEntryListToNames(entries)
			slices.Sort(got)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("ReadDir(-1) mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// CreateAndRead creates files in nested directories and verifies each reads
// back through Open and ReadFile, is not a symlink according to ReadLink,
// and has Lstat metadata, where the file system implements those.
func CreateAndRead[F WritableFile](t *testing.T, newFS Factory) {
	t.Helper()
	fsys := newFS(t)
	wfs := requireWrite[F](t, fsys)

	for _, name := range []string{"b/a/c", "b/b", "b/c", "c", "d/e/f/g", "d/e/g", "a/b/c/d/e/f/g"} {
		t.Run(name, func(t *testing.T) {
			if err := wfs.MkdirAll(path.Dir(name), fs.ModePerm); err != nil {
				t.Fatalf("MkdirAll(%q) = %v", path.Dir(name), err)
			}
			writeFile(t, wfs, name, name)

			if got, err := readFile(t, fsys, name); err != nil || string(got) != name {
				t.Errorf("ReadFile(%q) = (%q, %v), want (%q, nil)", name, got, err, name)
			}

			lfs, ok := fsys.(fs.ReadLinkFS)
			if !ok {
				return
			}
			target, err := lfs.ReadLink(name)
			var pe *fs.PathError
			if !errors.As(err, &pe) {
				t.Errorf("ReadLink(%q) = %v, want a *fs.PathError for a regular file", name, err)
			}
			if target != "" {
				t.Errorf("ReadLink(%q) = %q, want \"\"", name, target)
			}
			if info, err := lfs.Lstat(name); err != nil || info == nil {
				t.Errorf("Lstat(%q) = (%v, %v), want file info", name, info, err)
			}
		})
	}
}

// ReadFile verifies that content written through Create reads back through
// ReadFile. It is skipped if the file system does not implement
// [fs.ReadFileFS].
func ReadFile[F WritableFile](t *testing.T, newFS Factory) {
	t.Helper()
	fsys := newFS(t)
	wfs := requireWrite[F](t, fsys)
	rfs, ok := fsys.(fs.ReadFileFS)
	if !ok {
		t.Skipf("%T does not implement fs.ReadFileFS", fsys)
	}
	wantData := ufsTesting.RandomString(100)
	writeFile(t, wfs, "readfile_test.txt", wantData)
	got, err := rfs.ReadFile("readfile_test.txt")
	if err != nil {
		t.Fatalf("ReadFile() = %v", err)
	}
	if diff := cmp.Diff(wantData, string(got)); diff != "" {
		t.Errorf("ReadFile() mismatch (-want +got):\n%s", diff)
	}
}
