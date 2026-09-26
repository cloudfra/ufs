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

//go:build !wasm

package boltfs

import (
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func writeTestFile(t *testing.T, fsys ufs.FS, name, content string) {
	t.Helper()
	f, err := fsys.Create(name)
	if err != nil {
		t.Fatalf("Create(%q) = %v", name, err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("WriteString(%q) = %v", name, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close(%q) = %v", name, err)
	}
}

// TestBoltFSViaNew verifies the driver registers itself with ufs so that
// bolt: URIs resolve through the public factory.
func TestBoltFSViaNew(t *testing.T) {
	uri := testBoltFSURI(t)
	fsys, err := ufs.New(t.Context(), uri)
	if err != nil {
		t.Fatalf("ufs.New(%q) = %v", uri, err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	writeTestFile(t, fsys, "dir/file.txt", "via new")
	ufsTesting.AssertContains(t, fsys, "dir/file.txt", "via new")
}

func TestMakeBoltFSInvalidURI(t *testing.T) {
	for _, name := range []string{"memory:", "bolt:"} {
		t.Run(name, func(t *testing.T) {
			if fsys, err := makeBoltFS(name); err == nil {
				t.Errorf("makeBoltFS(%q) succeeded, want error", name)
				ufsTesting.ValidateClose(t, fsys)()
			}
		})
	}
}

// TestBoltFSReopenPersists verifies content survives closing and reopening
// the same database file.
func TestBoltFSReopenPersists(t *testing.T) {
	uri := testBoltFSURI(t)
	fsys, err := makeBoltFS(uri)
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.MkdirAll("a/b", 0o750); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, fsys, "a/b/c.txt", "persisted")
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := makeBoltFS(uri)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, reopened)()
	ufsTesting.AssertContains(t, reopened, "a/b/c.txt", "persisted")
	info, err := reopened.Stat("a/b")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode() != fs.ModeDir|0o750 {
		t.Errorf("Stat(a/b).Mode() = %v, want %v", info.Mode(), fs.ModeDir|0o750)
	}
}

// TestBoltFSMissingParentNotCreated verifies that operations on a path under
// a missing directory report fs.ErrNotExist without creating that directory
// as a side effect.
func TestBoltFSMissingParentNotCreated(t *testing.T) {
	fsys := newTestBoltFS(t)

	if err := fsys.Remove("ghost/file.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Remove(ghost/file.txt) = %v, want fs.ErrNotExist", err)
	}
	if err := fsys.RemoveAll("ghost2/sub/file.txt"); err != nil {
		t.Errorf("RemoveAll(ghost2/sub/file.txt) = %v, want nil", err)
	}
	for _, name := range []string{"ghost", "ghost2", "ghost2/sub"} {
		if _, err := fsys.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) = %v, want fs.ErrNotExist (created as a side effect)", name, err)
		}
	}
}

func TestBoltFSReadFileDirectory(t *testing.T) {
	fsys := newTestBoltFS(t)
	if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dir", pathutil.CwdPath} {
		if _, err := fsys.ReadFile(name); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("ReadFile(%q) = %v, want fs.ErrInvalid", name, err)
		}
	}
}

func TestBoltFSCreateOnDirectory(t *testing.T) {
	fsys := newTestBoltFS(t)
	if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dir", pathutil.CwdPath} {
		if _, err := fsys.Create(name); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Create(%q) = %v, want fs.ErrInvalid", name, err)
		}
	}
	info, err := fsys.Stat("dir")
	if err != nil || !info.IsDir() {
		t.Errorf("Stat(dir) = (%v, %v), want a directory", info, err)
	}
}

func TestBoltFSFileAsParent(t *testing.T) {
	fsys := newTestBoltFS(t)
	writeTestFile(t, fsys, "file", "x")

	if err := fsys.MkdirAll("file/sub", fs.ModePerm); err == nil {
		t.Error("MkdirAll(file/sub) succeeded, want error")
	}
	if _, err := fsys.Create("file/child.txt"); err == nil {
		t.Error("Create(file/child.txt) succeeded, want error")
	}
	if _, err := fsys.Stat("file/child.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(file/child.txt) = %v, want fs.ErrNotExist", err)
	}
	ufsTesting.AssertContains(t, fsys, "file", "x")
}

// TestBoltFSNamesSortingBeforeSelfKey covers names that sort before the
// reserved "." selfKey in bbolt's byte ordering, which must neither be
// hidden from listings nor make a directory look empty.
func TestBoltFSNamesSortingBeforeSelfKey(t *testing.T) {
	fsys := newTestBoltFS(t)
	for _, name := range []string{"dir/-dash", "dir/!bang", "dir/zeta", "dir/Alpha"} {
		writeTestFile(t, fsys, name, name)
	}
	if err := fsys.MkdirAll("dir/+sub", fs.ModePerm); err != nil {
		t.Fatal(err)
	}

	entries, err := fsys.ReadDir("dir")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"!bang", "+sub", "-dash", "Alpha", "zeta"}
	if diff := cmp.Diff(want, ufsTesting.DirEntryListToNames(entries)); diff != "" {
		t.Errorf("ReadDir(dir) mismatch (-want +got):\n%s", diff)
	}

	if err := fsys.MkdirAll("only", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, fsys, "only/-first", "x")
	if err := fsys.Remove("only"); !errors.Is(err, ufserrors.ErrDirNotEmpty) {
		t.Errorf("Remove(only) = %v, want directory not empty", err)
	}
}

func TestBoltFileCloseAfterFSClose(t *testing.T) {
	fsys, err := makeBoltFS(testBoltFSURI(t))
	if err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("late.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("late"); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}
	// The failed commit must keep the content dirty so every Close reports
	// the lost write instead of the second one silently succeeding.
	for i := range 2 {
		if err := f.Close(); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("Close() #%d after FS Close = %v, want fs.ErrClosed", i+1, err)
		}
	}
}

func TestBoltFileOpenedForReadCloseIsNoop(t *testing.T) {
	fsys := newTestBoltFS(t)
	writeTestFile(t, fsys, "r.txt", "read only")
	f, err := fsys.Open("r.txt")
	if err != nil {
		t.Fatal(err)
	}
	before, err := fsys.Stat("r.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(f); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := fsys.Stat("r.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("ModTime changed from %v to %v after closing an unmodified file", before.ModTime(), after.ModTime())
	}
}

func TestBoltFSWatchOnFile(t *testing.T) {
	fsys := newTestBoltFS(t)
	writeTestFile(t, fsys, "file.txt", "x")
	_, err := fsys.(ufs.Watcher).Watch(t.Context(), "file.txt", func(ufs.NotifyOp, string) {})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Watch(file.txt) = %v, want fs.ErrInvalid", err)
	}
}

func TestBoltFSURIAndString(t *testing.T) {
	uri := boltFSPrefix + filepath.ToSlash(filepath.Join(t.TempDir(), "s.db"))
	fsys, err := makeBoltFS(uri)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()
	u, err := fsys.URI()
	if err != nil {
		t.Fatal(err)
	}
	if u.String() != uri {
		t.Errorf("URI() = %q, want %q", u, uri)
	}
	if got, want := fsys.String(), "boltFS("+uri+")"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
