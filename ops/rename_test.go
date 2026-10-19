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

package ops_test

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/ops"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// noRenameFS hides the Rename method of the file system it wraps, so that
// [ops.Rename] has to copy and remove.
type noRenameFS struct {
	types.WriteFS
}

// failRemoveFS is a noRenameFS whose Remove and RemoveAll fail for the path
// fail.
type failRemoveFS struct {
	noRenameFS
	fail string
}

var errRemove = errors.New("remove failed")

func (fsys failRemoveFS) Remove(name string) error {
	if name == fsys.fail {
		return errRemove
	}
	return fsys.noRenameFS.Remove(name)
}

func (fsys failRemoveFS) RemoveAll(name string) error {
	if name == fsys.fail {
		return errRemove
	}
	return fsys.noRenameFS.RemoveAll(name)
}

func writeTestFile(tb testing.TB, fsys types.WriteFS, name string, data []byte) {
	tb.Helper()
	f, err := fsys.Create(name)
	if err != nil {
		tb.Fatalf("Create(%q) = %v", name, err)
	}
	if _, err := f.Write(data); err != nil {
		tb.Fatalf("Write(%q) = %v", name, err)
	}
	ufsTesting.Must(tb, f.Close())
}

func assertFileContent(tb testing.TB, fsys fs.FS, name, want string) {
	tb.Helper()
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		tb.Errorf("types.ReadFile(%q) = %v, want nil", name, err)
		return
	}
	if string(data) != want {
		tb.Errorf("types.ReadFile(%q) = %q, want %q", name, data, want)
	}
}

func assertNotExist(tb testing.TB, fsys fs.FS, name string) {
	tb.Helper()
	if _, err := fs.Stat(fsys, name); !errors.Is(err, fs.ErrNotExist) {
		tb.Errorf("Stat(%q) = %v, want %v", name, err, fs.ErrNotExist)
	}
}

func TestRenameByCopy(t *testing.T) {
	t.Parallel()
	base := newTestMemFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, base))
	fsys := noRenameFS{base}
	ufsTesting.Must(t, fsys.MkdirAll("a/sub", fs.ModePerm))
	writeTestFile(t, fsys, "a/f.txt", []byte("f"))
	writeTestFile(t, fsys, "a/sub/g.txt", []byte("g"))
	writeTestFile(t, fsys, "c.txt", []byte("c"))

	if err := ops.Rename(fsys, "c.txt", "d.txt"); err != nil {
		t.Fatalf("Rename(c.txt, d.txt) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "d.txt", "c")
	assertNotExist(t, fsys, "c.txt")

	if err := ops.Rename(fsys, "a", "b"); err != nil {
		t.Fatalf("Rename(a, b) = %v, want nil", err)
	}
	assertFileContent(t, fsys, "b/f.txt", "f")
	assertFileContent(t, fsys, "b/sub/g.txt", "g")
	assertNotExist(t, fsys, "a")

	if err := ops.Rename(fsys, "b", "b/sub/b"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Rename(b, b/sub/b) = %v, want %v", err, fs.ErrInvalid)
	}
	assertFileContent(t, fsys, "b/sub/g.txt", "g")
}

// TestRenameByCopyRemoveFails checks that a failed Remove of a file leaves
// only the original, and that of a directory keeps the copy.
func TestRenameByCopyRemoveFails(t *testing.T) {
	t.Parallel()
	base := newTestMemFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, base))
	ufsTesting.Must(t, base.MkdirAll("a", fs.ModePerm))
	writeTestFile(t, base, "a/f.txt", []byte("f"))
	writeTestFile(t, base, "c.txt", []byte("c"))
	err := ops.Rename(failRemoveFS{noRenameFS{base}, "c.txt"}, "c.txt", "d.txt")
	var linkErr *os.LinkError
	if !errors.Is(err, errRemove) || !errors.As(err, &linkErr) {
		t.Fatalf("Rename(c.txt, d.txt) = %v, want an *os.LinkError for %v", err, errRemove)
	}
	assertFileContent(t, base, "c.txt", "c")
	assertNotExist(t, base, "d.txt")

	if err := ops.Rename(failRemoveFS{noRenameFS{base}, "a"}, "a", "b"); !errors.Is(err, errRemove) {
		t.Fatalf("Rename(a, b) = %v, want %v", err, errRemove)
	}
	assertFileContent(t, base, "a/f.txt", "f")
	assertFileContent(t, base, "b/f.txt", "f")
}
