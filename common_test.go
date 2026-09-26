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
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cloudfra/ufs/internal/pathutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

func checkInvalidPathOpen(t *testing.T, fsys fs.FS, path string) {
	t.Helper()
	_, err := fsys.Open(path)
	ufsTesting.AssertInvalidPathError(t, path, err, "open")
}

func checkInvalidPathReadDir(t *testing.T, fsys fs.ReadDirFS, path string) {
	t.Helper()
	_, err := fsys.ReadDir(path)
	ufsTesting.AssertInvalidPathError(t, path, err, "readdir")
}

func checkInvalidPathCreate(t *testing.T, fsys createFileFS, path string) {
	t.Helper()
	_, err := fsys.Create(path)
	ufsTesting.AssertInvalidPathError(t, path, err, "create")
}

func checkInvalidPathMkdirAll(t *testing.T, fsys mkdirAllFS, path string) {
	t.Helper()
	err := fsys.MkdirAll(path, fs.ModeDir)
	ufsTesting.AssertInvalidPathError(t, path, err, "mkdir")
}

func checkInvalidPathReadFile(t *testing.T, fsys fs.ReadFileFS, path string) {
	t.Helper()
	_, err := fsys.ReadFile(path)
	ufsTesting.AssertInvalidPathError(t, path, err, "readfile")
}

func checkInvalidPathReadLink(t *testing.T, fsys fs.ReadLinkFS, path string) {
	t.Helper()
	_, err := fsys.ReadLink(path)
	ufsTesting.AssertInvalidPathError(t, path, err, "readlink")
}

func checkInvalidPathLstat(t *testing.T, fsys fs.ReadLinkFS, path string) {
	t.Helper()
	_, err := fsys.Lstat(path)
	ufsTesting.AssertInvalidPathError(t, path, err, "lstat")
}

func checkInvalidPathRemove(t *testing.T, fsys RemoveFileFS, path string) {
	t.Helper()
	err := fsys.Remove(path)
	ufsTesting.AssertInvalidPathError(t, path, err, "remove")
}

func checkInvalidPathRemoveAll(t *testing.T, fsys RemoveFileFS, path string) {
	t.Helper()
	err := fsys.RemoveAll(path)
	ufsTesting.AssertInvalidPathError(t, path, err, "removeall")
}

func TestInvalidPath(t *testing.T) {
	invalidPaths := []string{
		"/absolute/path",
		"../relative/path",
		"invalid/../path",
		"",
	}

	for _, fsysTC := range getAllTestCaseList() {
		for _, path := range invalidPaths {
			t.Run(fmt.Sprintf("Open/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				checkInvalidPathOpen(t, fsysTC.createFS(t), path)
			})

			t.Run(fmt.Sprintf("ReadDir/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				checkInvalidPathReadDir(t, fsysTC.createFS(t), path)
			})

			t.Run(fmt.Sprintf("Create/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				checkInvalidPathCreate(t, fsysTC.createFS(t), path)
			})

			t.Run(fmt.Sprintf("MkdirAll/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				checkInvalidPathMkdirAll(t, fsysTC.createFS(t), path)
			})

			t.Run(fmt.Sprintf("ReadFileFS/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				if rf, ok := fsys.(fs.ReadFileFS); ok {
					checkInvalidPathReadFile(t, rf, path)
				}
			})

			t.Run(fmt.Sprintf("ReadLink/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				if rf, ok := fsys.(fs.ReadLinkFS); ok {
					checkInvalidPathReadLink(t, rf, path)
				}
			})

			t.Run(fmt.Sprintf("Lstat/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				if rf, ok := fsys.(fs.ReadLinkFS); ok {
					checkInvalidPathLstat(t, rf, path)
				}
			})

			t.Run(fmt.Sprintf("Remove/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				if r, ok := fsys.(RemoveFileFS); ok {
					checkInvalidPathRemove(t, r, path)
				}
			})

			t.Run(fmt.Sprintf("RemoveAll/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				if r, ok := fsys.(RemoveFileFS); ok {
					checkInvalidPathRemoveAll(t, r, path)
				}
			})
		}
	}
}

func TestFSConventions(t *testing.T) {
	srcFS, err := newLocalFS(t.Context(), testLocalFSName)
	if err != nil {
		t.Fatalf("cannot mount localFS(%q), %s", testLocalFSName, err)
	}
	t.Cleanup(ufsTesting.ValidateClose(t, srcFS))
	for _, fsysTC := range getReadWriteTestCaseList() {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)
			if err := Rsync(srcFS, fsys, pathutil.CwdPath); err != nil {
				t.Errorf("rsync failed with error, %s", err)
			}

			allFilenames, err := List(srcFS, pathutil.CwdPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(allFilenames) == 0 {
				t.Fatal("expected at least 1 file name")
			}
			if err := fstest.TestFS(fsys, allFilenames...); err != nil {
				t.Error(err)
			}
		})
	}
}

// checkCloseIdempotent verifies that closing fsys repeatedly always succeeds.
func checkCloseIdempotent(t *testing.T, fsys io.Closer) {
	t.Helper()
	for i := range 10 {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() [%d] failed with error, %s", i, err)
		}
	}
}

func checkClosedOpen(t *testing.T, fsys fs.FS) {
	t.Helper()
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
}

func checkClosedCreate(t *testing.T, fsys createFileFS) {
	t.Helper()
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
}

func checkClosedMkdirAll(t *testing.T, fsys mkdirAllFS) {
	t.Helper()
	if err := fsys.MkdirAll("a/b/c", fs.ModePerm); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("MkdirAll('a/b/c') did not return fs.ErrClosed, got: %s", err)
	}
}

func checkClosedRemove(t *testing.T, fsys RemoveFileFS) {
	t.Helper()
	if err := fsys.Remove("file.txt"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Remove('file.txt') did not return fs.ErrClosed, got: %s", err)
	}
}

func checkClosedRemoveAll(t *testing.T, fsys RemoveFileFS) {
	t.Helper()
	if err := fsys.RemoveAll("dir"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("RemoveAll('dir') did not return fs.ErrClosed, got: %s", err)
	}
}

func checkClosedReadDir(t *testing.T, fsys fs.FS) {
	t.Helper()
	entries, err := fs.ReadDir(fsys, ".")
	if len(entries) != 0 {
		t.Errorf("ReadDir('.') returned results for a closed file system, got: %v", entries)
	}
	if !errors.Is(err, fs.ErrClosed) {
		t.Errorf("ReadDir('.') did not return fs.ErrClosed, got: %s", err)
	}
}

func TestFSClose(t *testing.T) {
	for _, fsysTC := range getAllRegularTestCaseList() {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)
			checkCloseIdempotent(t, fsys)

			t.Run("Open", func(t *testing.T) { checkClosedOpen(t, fsys) })
			t.Run("Create", func(t *testing.T) { checkClosedCreate(t, fsys) })
			t.Run("MkdirAll", func(t *testing.T) { checkClosedMkdirAll(t, fsys) })
			t.Run("Remove", func(t *testing.T) { checkClosedRemove(t, fsys) })
			t.Run("RemoveAll", func(t *testing.T) { checkClosedRemoveAll(t, fsys) })
			t.Run("ReadDir", func(t *testing.T) { checkClosedReadDir(t, fsys) })
		})
	}
}

// checkStringContains verifies that fsys.String() contains want.
func checkStringContains(t *testing.T, fsys fmt.Stringer, want string) {
	t.Helper()
	if got := fsys.String(); !strings.Contains(got, want) {
		t.Errorf("%s.String() should contain %q: got: %q", fsys, want, got)
	}
}

func TestFSString(t *testing.T) {
	for _, tc := range getAllTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkStringContains(t, tc.createFS(t), tc.wantString)
		})
	}
}

func checkMkdirAllDir(t *testing.T, fsys mkdirAllFS, dir string) {
	t.Helper()
	if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
		t.Errorf("cannot Mkdir(%q), %s", dir, err)
	}
}

// checkReadDirNames verifies that fs.ReadDirFS.ReadDir lists a directory's
// children in sorted order.
func checkReadDirNames(t *testing.T, fsys fs.ReadDirFS, input string, want []string) {
	t.Helper()
	gotEntries, err := fsys.ReadDir(input)
	if err != nil {
		t.Errorf("cannot ReadDir(%q), got error: %s", input, err)
	}
	gotNames := ufsTesting.DirEntryListToNames(gotEntries)
	if diff := cmp.Diff(want, gotNames); diff != "" {
		t.Errorf("got %s, want %s diff(-want,+got):\n %v", gotNames, want, diff)
	}
}

// checkOpenReadDir verifies that fs.ReadDirFile.ReadDir(-1) lists a
// directory's children, in any order.
func checkOpenReadDir(t *testing.T, fsys fs.FS, input string, want []string) {
	t.Helper()
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
}

func TestFSReadDir(t *testing.T) {
	for _, fsysTC := range getAllRegularTestCaseList() {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)

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
				t.Run(fmt.Sprintf("MkdirAll/%s", dir), func(t *testing.T) { checkMkdirAllDir(t, fsys, dir) })
			}

			for input, want := range lsMap {
				t.Run(fmt.Sprintf("ReadDir/%s", input), func(t *testing.T) { checkReadDirNames(t, fsys, input, want) })
				t.Run(fmt.Sprintf("Open/%s", input), func(t *testing.T) { checkOpenReadDir(t, fsys, input, want) })
			}
		})
	}
}

// checkCreateWriteString creates filename via fsys.Create, writes filename's
// own text as its content, and closes it.
func checkCreateWriteString(t *testing.T, fsys createFileFS, filename string) {
	t.Helper()
	f, err := fsys.Create(filename)
	if err != nil {
		t.Errorf("Open(%q) failed, %s", filename, err)
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
}

func checkReadFileEquals(t *testing.T, fsys fs.ReadFileFS, filename, want string) {
	t.Helper()
	data, err := fsys.ReadFile(filename)
	if err != nil {
		t.Errorf("ReadFile(%q) got error, %s", filename, err)
	}
	if got := string(data); got != want {
		t.Errorf("ReadFile(%q) mismatch, got: %q, want: %q", filename, got, want)
	}
}

func checkOpenReadEquals(t *testing.T, fsys fs.FS, filename, want string) {
	t.Helper()
	rf, err := fsys.Open(filename)
	if err != nil {
		t.Errorf("Open(%q) got error, %s", filename, err)
	}
	data, err := io.ReadAll(rf)
	if err != nil {
		t.Errorf("io.ReadAll(%q) got error, %s", filename, err)
	}
	if err := rf.Close(); err != nil {
		t.Errorf("Close() got error, %s", err)
	}
	if got := string(data); got != want {
		t.Errorf("io.ReadAll(%q) mismatch, got: %q, want: %q", filename, got, want)
	}
}

func checkReadLinkOfRegularFile(t *testing.T, fsys fs.ReadLinkFS, filename string) {
	t.Helper()
	lfilename, err := fsys.ReadLink(filename)
	if err == nil {
		t.Errorf("ReadLink(%q) error was nil", filename)
	} else if _, ok := err.(*fs.PathError); !ok {
		t.Errorf("ReadLink(%q) error was not of type *fs.PathError, %s", filename, err)
	}
	if lfilename != "" {
		t.Errorf("ReadLink(%q) mismatch, got: %q, want: ''", filename, lfilename)
	}
}

func checkLstatOfRegularFile(t *testing.T, fsys fs.ReadLinkFS, filename string) {
	t.Helper()
	stat, err := fsys.Lstat(filename)
	if err != nil {
		t.Errorf("Lstat(%q) got error, %s", filename, err)
	}
	if stat == nil {
		t.Errorf("Lstat(%q) returned nil FileInfo", filename)
	}
}

func TestFSCreate(t *testing.T) {
	for _, fsysTC := range getAllRegularTestCaseList() {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)

			filenames := []string{"b/a/c", "b/b", "b/c", "c", "d/e/f/g", "d/e/g", "a/b/c/d/e/f/g"}

			for _, filename := range filenames {
				t.Run(fmt.Sprintf("Create/%s", filename), func(t *testing.T) {
					checkMkdirAllDir(t, fsys, path.Dir(filename))
					t.Run("Create", func(t *testing.T) { checkCreateWriteString(t, fsys, filename) })
					t.Run("ReadFile", func(t *testing.T) { checkReadFileEquals(t, fsys, filename, filename) })
					t.Run("Open", func(t *testing.T) { checkOpenReadEquals(t, fsys, filename, filename) })
					t.Run("ReadLink", func(t *testing.T) { checkReadLinkOfRegularFile(t, fsys, filename) })
					t.Run("Lstat", func(t *testing.T) { checkLstatOfRegularFile(t, fsys, filename) })
				})
			}
		})
	}
}
