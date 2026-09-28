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

// Package testing provides shared conformance tests that drivers run against their
// ufs.ReadFS, ufs.WriteFS, and ufs.FS implementations.
package testing

import (
	"io/fs"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/pathutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func readFS(t *testing.T, createFSFunc func(t *testing.T) ufs.ReadFS) {
	Constructor(t, createFSFunc)
	InvalidPathsForReadFS(t, createFSFunc)
	String(t, createFSFunc)
}

// ReadFS runs the read-only conformance suite, including URI checks, against
// file systems returned by createFSFunc.
func ReadFS(t *testing.T, createFSFunc func(t *testing.T) ufs.ReadFS) {
	readFS(t, createFSFunc)
	URI(t, createFSFunc)
}

// String verifies that the file system's String method returns a URI-like
// description containing "//".
func String(t *testing.T, createFSFunc func(t *testing.T) ufs.ReadFS) {
	t.Run("String", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))
		if got := fsys.String(); !strings.Contains(got, "//") {
			t.Errorf("%s.String() should contain %q: got: %q", fsys, "//", got)
		}
	})
}

// Constructor verifies that createFSFunc returns a non-nil file system that
// can be closed repeatedly.
func Constructor(t *testing.T, createFSFunc func(t *testing.T) ufs.ReadFS) {
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

// URI verifies that the file system reports a URI marked read-only.
func URI(t *testing.T, createFSFunc func(t *testing.T) ufs.ReadFS) {
	t.Run("URI", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		defer ufsTesting.ValidateClose(t, fsys)()

		u, err := fsys.URI()
		if err != nil {
			t.Fatalf("URI() returned error: %v", err)
		}
		if u == nil {
			t.Fatal("URI() = nil, want a URL")
		}
		if got := u.Query().Get("ro"); got != "true" {
			t.Errorf("URI().Query().Get(\"ro\") = %q, want %q", got, "true")
		}
	})
}

// InvalidPathsForReadFS verifies that read operations reject invalid paths.
func InvalidPathsForReadFS(t *testing.T, createFS func(*testing.T) ufs.ReadFS) {
	tests := []struct {
		name   string
		wantOp string
		op     func(fsys ufs.ReadFS, path string) error
	}{
		{
			name:   "Open",
			wantOp: "open",
			op:     func(fsys ufs.ReadFS, path string) error { _, err := fsys.Open(path); return err },
		},
		{
			name:   "Stat",
			wantOp: "stat",
			op:     func(fsys ufs.ReadFS, path string) error { _, err := fsys.Stat(path); return err },
		},
		{
			name:   "Lstat",
			wantOp: "lstat",
			op:     func(fsys ufs.ReadFS, path string) error { _, err := fsys.Lstat(path); return err },
		},
		{
			name:   "ReadFile",
			wantOp: "readfile",
			op:     func(fsys ufs.ReadFS, path string) error { _, err := fsys.ReadFile(path); return err },
		},
		{
			name:   "ReadDir",
			wantOp: "readdir",
			op:     func(fsys ufs.ReadFS, path string) error { _, err := fsys.ReadDir(path); return err },
		},
		{
			name:   "ReadLink",
			wantOp: "readlink",
			op:     func(fsys ufs.ReadFS, path string) error { _, err := fsys.ReadLink(path); return err },
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

func assertDirFileTreeUnchanged(t *testing.T, fsys ufs.ReadFS) {
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
