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

package core

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestLocalFSLink covers localFS.Link, the method that makes it a types.LinkFS.
// Whether a file system can give one file a second name at all depends on the
// file system behind the root -- linkfs.go says a backend that cannot do it has
// to report an error rather than store a copy -- so the cases that need one
// real hard link live in localfs_link_unix_test.go. What is checked here is
// what localFS promises whatever sits underneath: both names are held to the
// root before anything is asked of the platform, and a name that does not exist
// is reported as fs.ErrNotExist rather than linked anyway.
func TestLocalFSLink(t *testing.T) {
	dir := t.TempDir()
	fsys, err := newLocalFS(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	lfs, ok := fsys.(types.LinkFS)
	if !ok {
		t.Fatal("localFS does not implement types.LinkFS")
	}

	if err := osutil.WriteFile(filepath.Join(dir, "target.txt"), []byte("seed")); err != nil {
		t.Fatal(err)
	}

	t.Run("bad_oldname", func(t *testing.T) {
		// The names are checked before the platform is asked, so a refused
		// name is reported by localFS as a *fs.PathError carrying "link", not
		// as an *os.LinkError.
		for _, name := range linkInvalidNames {
			err := lfs.Link(name, "alias.txt")
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Link(%q, \"alias.txt\") = %v, want ErrInvalid", name, err)
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("Link(%q, \"alias.txt\") = %v, want a *fs.PathError", name, err)
			} else if pathErr.Op != "link" {
				t.Errorf("Link(%q, \"alias.txt\") error Op = %q, want %q", name, pathErr.Op, "link")
			}
		}
		if _, err := fsys.Stat("alias.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(\"alias.txt\") after the refused calls = %v, want ErrNotExist", err)
		}
	})

	t.Run("bad_newname", func(t *testing.T) {
		for _, name := range linkInvalidNames {
			err := lfs.Link("target.txt", name)
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Link(\"target.txt\", %q) = %v, want ErrInvalid", name, err)
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("Link(\"target.txt\", %q) = %v, want a *fs.PathError", name, err)
			} else if pathErr.Op != "link" {
				t.Errorf("Link(\"target.txt\", %q) error Op = %q, want %q", name, pathErr.Op, "link")
			}
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("file created outside the root: %v", err)
		}
		for _, name := range []string{"a", "b"} {
			if _, err := fsys.Stat(name); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Stat(%q) after the refused calls = %v, want ErrNotExist: nothing must be created in the root", name, err)
			}
		}
	})

	t.Run("missing_oldname", func(t *testing.T) {
		err := lfs.Link("ghost.txt", "alias.txt")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Link(\"ghost.txt\", \"alias.txt\") = %v, want ErrNotExist", err)
		}
		// linkfs.go says the errors are *os.LinkError values.
		var linkErr *os.LinkError
		if !errors.As(err, &linkErr) {
			t.Errorf("Link(\"ghost.txt\", \"alias.txt\") = %v, want an *os.LinkError", err)
		}
		if _, err := fsys.Stat("alias.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(\"alias.txt\") after the failed call = %v, want ErrNotExist", err)
		}
	})
}

// linkInvalidNames are names that linkfs.go callers cannot reach a root with.
// fs.ValidPath calls "." valid, so it is not one of them.
var linkInvalidNames = []string{"", "/abs.txt", "../escape.txt", "a/../../b", "dir/../target.txt", "target.txt/", "a//b"}
