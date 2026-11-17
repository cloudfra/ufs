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

// Package testing provides shared conformance tests that drivers run against their
// types.ReadFS, types.WriteFS, and ufs.FS implementations.
package testing

import (
	"bytes"
	"io"
	"io/fs"
	"maps"
	"path"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/ops"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func readFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.ReadFS) {
	Constructor(t, args, createFSFunc)
	InvalidPathsForReadFS(t, args, createFSFunc)
	readConsistency(t, args, createFSFunc)
}

// readConsistency verifies that every read path of a file system reports the
// same tree: the listing it yields must satisfy the io/fs conventions and agree
// across Open, ReadDir, ReadFile, Lstat and ReadLink.
func readConsistency(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.ReadFS) {
	t.Run("ReadConsistency", func(t *testing.T) {
		t.Run("Conventions", func(t *testing.T) {
			t.Parallel()
			fsys := createFSFunc(t)
			t.Cleanup(ufsTesting.ValidateClose(t, fsys))
			names, err := ops.List(fsys, pathutil.CwdPath)
			if err != nil {
				t.Fatalf("List() = %v", err)
			}
			if err := fstest.TestFS(fsys, names...); err != nil {
				t.Errorf("fstest.TestFS() = %v", err)
			}
		})
		t.Run("Listings", func(t *testing.T) {
			t.Parallel()
			fsys := createFSFunc(t)
			t.Cleanup(ufsTesting.ValidateClose(t, fsys))
			readDirListings(t, fsys)
		})
		t.Run("EntryInfo", func(t *testing.T) {
			t.Parallel()
			fsys := createFSFunc(t)
			t.Cleanup(ufsTesting.ValidateClose(t, fsys))
			entryInfo(t, fsys)
		})
		t.Run("ReadPaths", func(t *testing.T) {
			t.Parallel()
			fsys := createFSFunc(t)
			t.Cleanup(ufsTesting.ValidateClose(t, fsys))
			readPaths(t, fsys)
		})
	})
}

// dirListings returns the listing of every directory reachable from the root,
// keyed by directory. A directory that fails to list is omitted and reported.
func dirListings(t *testing.T, fsys types.ReadFS) map[string][]fs.DirEntry {
	t.Helper()
	listings := make(map[string][]fs.DirEntry)
	dirs := []string{pathutil.CwdPath}
	for len(dirs) > 0 {
		dir := dirs[0]
		dirs = dirs[1:]
		entries, err := fsys.ReadDir(dir)
		if err != nil {
			t.Errorf("ReadDir(%q) = %v", dir, err)
			continue
		}
		listings[dir] = entries
		for _, entry := range entries {
			if entry.IsDir() {
				dirs = append(dirs, path.Join(dir, entry.Name()))
			}
		}
	}
	return listings
}

// readDirListings verifies that each listing is sorted, that every name it
// reports opens, and that an opened directory handle lists what ReadDir reports.
func readDirListings(t *testing.T, fsys types.ReadFS) {
	t.Helper()
	listings := dirListings(t, fsys)
	for _, dir := range slices.Sorted(maps.Keys(listings)) {
		entries := listings[dir]
		for i := 1; i < len(entries); i++ {
			if entries[i-1].Name() > entries[i].Name() {
				t.Errorf("ReadDir(%q) lists %q before %q, want sorted by name", dir, entries[i-1].Name(), entries[i].Name())
			}
		}
		for _, entry := range entries {
			name := path.Join(dir, entry.Name())
			file, err := fsys.Open(name)
			if err != nil {
				t.Errorf("Open(%q) = %v, want the name listed by ReadDir(%q) to open", name, err, dir)
				continue
			}
			if entry.IsDir() {
				handle, ok := file.(fs.ReadDirFile)
				if !ok {
					t.Errorf("Open(%q) = %T, want a directory handle that is fs.ReadDirFile", name, file)
				} else {
					handleEntries, err := handle.ReadDir(-1)
					if err != nil {
						t.Errorf("ReadDir(-1) on the handle from Open(%q) = %v", name, err)
					} else if !sameNames(listings[name], handleEntries) {
						t.Errorf("the handle from Open(%q) lists %v, ReadDir(%q) lists %v, want the same names in the same order",
							name, namesOf(handleEntries), name, namesOf(listings[name]))
					}
				}
			}
			if err := file.Close(); err != nil {
				t.Errorf("Close() on the handle from Open(%q) = %v", name, err)
			}
		}
	}
}

// entryInfo verifies that the [fs.DirEntry] a listing reports describes the
// same node as Lstat does, and that every listed symbolic link names a target.
func entryInfo(t *testing.T, fsys types.ReadFS) {
	t.Helper()
	listings := dirListings(t, fsys)
	for _, dir := range slices.Sorted(maps.Keys(listings)) {
		for _, entry := range listings[dir] {
			name := path.Join(dir, entry.Name())
			info, err := fsys.Lstat(name)
			if err != nil {
				t.Errorf("Lstat(%q) = %v, want the name listed by ReadDir(%q) to exist", name, err, dir)
				continue
			}
			listed, err := entry.Info()
			if err != nil {
				t.Errorf("Info() on the entry for %q listed by ReadDir(%q) = %v", name, dir, err)
				continue
			}
			if listed.Name() != info.Name() || listed.IsDir() != info.IsDir() || listed.Size() != info.Size() || listed.Mode() != info.Mode() {
				t.Errorf("the entry for %q listed by ReadDir(%q) is %q size %d mode %v, Lstat is %q size %d mode %v",
					name, dir, listed.Name(), listed.Size(), listed.Mode(),
					info.Name(), info.Size(), info.Mode())
			}
			if info.Mode().Type() == fs.ModeSymlink {
				target, err := fsys.ReadLink(name)
				if err != nil {
					t.Errorf("ReadLink(%q) = %v, want the symbolic link listed by ReadDir(%q) to name a target", name, err, dir)
				} else if target == "" {
					t.Errorf("ReadLink(%q) = %q, want a target", name, target)
				}
			}
		}
	}
}

// readPaths verifies that ReadFile and an opened handle report the same content
// and metadata for every regular file, and that nothing exists below one.
func readPaths(t *testing.T, fsys types.ReadFS) {
	t.Helper()
	listings := dirListings(t, fsys)
	for _, dir := range slices.Sorted(maps.Keys(listings)) {
		for _, entry := range listings[dir] {
			// Symbolic links are pinned by entryInfo: reading through one is not
			// defined the same way by every driver.
			if !entry.Type().IsRegular() {
				continue
			}
			name := path.Join(dir, entry.Name())
			data, err := fsys.ReadFile(name)
			if err != nil {
				t.Errorf("ReadFile(%q) = %v", name, err)
				continue
			}
			file, err := fsys.Open(name)
			if err != nil {
				t.Errorf("Open(%q) = %v", name, err)
				continue
			}
			opened, err := io.ReadAll(file)
			if err != nil {
				t.Errorf("ReadAll() on the handle from Open(%q) = %v", name, err)
			} else if !bytes.Equal(opened, data) {
				t.Errorf("ReadAll() on the handle from Open(%q) = %q, ReadFile(%q) = %q, want the same content", name, opened, name, data)
			}
			info, err := file.Stat()
			if err != nil {
				t.Errorf("Stat() on the handle from Open(%q) = %v", name, err)
			} else if info.Name() != path.Base(name) || info.Size() != int64(len(data)) {
				t.Errorf("Stat() on the handle from Open(%q) = %q size %d, want %q size %d", name, info.Name(), info.Size(), path.Base(name), len(data))
			}
			if err := file.Close(); err != nil {
				t.Errorf("Close() on the handle from Open(%q) = %v", name, err)
			}
			below := name + "/below"
			if _, err := fsys.Lstat(below); err == nil {
				t.Errorf("Lstat(%q) = nil, want an error because %q is a regular file", below, name)
			}
			if _, err := fsys.ReadFile(below); err == nil {
				t.Errorf("ReadFile(%q) = nil, want an error because %q is a regular file", below, name)
			}
		}
	}
}

// namesOf returns the names in entries, for failures.
func namesOf(entries []fs.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// sameNames reports whether both listings hold the same names in the same order.
func sameNames(first, second []fs.DirEntry) bool {
	if len(first) != len(second) {
		return false
	}
	for i := range first {
		if first[i].Name() != second[i].Name() {
			return false
		}
	}
	return true
}

// Constructor verifies that createFSFunc returns a non-nil file system that
// can be closed repeatedly.
func Constructor(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.ReadFS) {
	t.Run("Constructor", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		defer ufsTesting.ValidateClose(t, fsys)()
		if fsys == nil {
			t.Fatalf("file system is nil")
		}
		ufsTesting.ValidateClose(t, fsys)()
		ufsTesting.ValidateClose(t, fsys)()
	})
}

// InvalidPathsForReadFS verifies that read operations reject invalid paths.
func InvalidPathsForReadFS(t *testing.T, _ Args, createFS func(*testing.T) types.ReadFS) {
	tests := []struct {
		name   string
		wantOp string
		op     func(fsys types.ReadFS, path string) error
	}{
		{
			name:   "Open",
			wantOp: "open",
			op:     func(fsys types.ReadFS, path string) error { _, err := fsys.Open(path); return err },
		},
		{
			name:   "Stat",
			wantOp: "stat",
			op:     func(fsys types.ReadFS, path string) error { _, err := fsys.Stat(path); return err },
		},
		{
			name:   "Lstat",
			wantOp: "lstat",
			op:     func(fsys types.ReadFS, path string) error { _, err := fsys.Lstat(path); return err },
		},
		{
			name:   "ReadFile",
			wantOp: "readfile",
			op:     func(fsys types.ReadFS, path string) error { _, err := fsys.ReadFile(path); return err },
		},
		{
			name:   "ReadDir",
			wantOp: "readdir",
			op:     func(fsys types.ReadFS, path string) error { _, err := fsys.ReadDir(path); return err },
		},
		{
			name:   "ReadLink",
			wantOp: "readlink",
			op:     func(fsys types.ReadFS, path string) error { _, err := fsys.ReadLink(path); return err },
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

func assertDirFileTreeUnchanged(t *testing.T, fsys types.ReadFS) {
	t.Helper()
	for _, name := range []string{pathutil.CwdPath, "dir", "dir/sub"} {
		if info, err := fsys.Stat(name); err != nil || !info.IsDir() {
			t.Errorf("Stat(%q) = (%v, %v), want a directory", name, info, err)
		}
	}
	if got, err := fsys.ReadFile("dir/file"); err != nil || string(got) != "content" {
		t.Errorf("ReadFile(dir/file) = (%q, %v), want (%q, nil)", got, err, "content")
	}
	// Nothing may exist under the regular file. localFS reports ENOTDIR here
	// rather than fs.ErrNotExist, so only the absence of an entry is checked.
	for _, name := range []string{"dir/file/x", "dir/file/x/y"} {
		if info, err := fsys.Stat(name); err == nil {
			t.Errorf("Stat(%q) = (%v, nil), want an error", name, info)
		}
	}
}

// OpenWithNew verifies that [ufs.New] opens uri, which checks that the driver
// for uri is registered, and that each file in want holds exactly its
// content. Drivers outside the ufs package register on import, so call it from
// the driver's own tests.
func OpenWithNew(t *testing.T, uri string, want map[string]string) {
	t.Helper()
	fsys, err := ufs.New(t.Context(), uri)
	if err != nil {
		t.Fatalf("ufs.New(%q) = %v, want nil", uri, err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()
	for _, name := range slices.Sorted(maps.Keys(want)) {
		got, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Errorf("ReadFile(%q) = %v, want nil", name, err)
		} else if string(got) != want[name] {
			t.Errorf("ReadFile(%q) = %q, want %q", name, got, want[name])
		}
	}
}
