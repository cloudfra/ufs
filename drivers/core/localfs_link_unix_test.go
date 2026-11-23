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

//go:build unix

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

// TestLocalFSLinkUnix covers linkfs.go against a real hard link: two names for
// one stored file, sharing content and metadata. It is behind //go:build unix
// because whether a file system behind a root supports hard links at all is a
// question about that file system; where they are not supported the backend has
// to report an error instead, which no test can tell apart from a failure.
func TestLocalFSLinkUnix(t *testing.T) {
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
	ofs, ok := fsys.(fs.ReadLinkFS)
	if !ok {
		t.Fatal("localFS does not implement fs.ReadLinkFS")
	}
	wfs, ok := fsys.(types.WriteFileFS)
	if !ok {
		t.Fatal("localFS does not implement types.WriteFileFS")
	}

	if err := osutil.WriteFile(filepath.Join(dir, "target.txt"), []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.WriteFile(filepath.Join(dir, "existing.txt"), []byte("kept")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.MkdirAll(filepath.Join(dir, "dir")); err != nil {
		t.Fatal(err)
	}

	t.Run("both_names_hold_the_same_file", func(t *testing.T) {
		if err := lfs.Link("target.txt", "alias.txt"); err != nil {
			t.Fatalf("Link(\"target.txt\", \"alias.txt\") = %v, want nil", err)
		}
		got, err := fsys.ReadFile("alias.txt")
		if err != nil || string(got) != "hello" {
			t.Errorf("ReadFile(\"alias.txt\") = (%q, %v), want %q and nil", got, err, "hello")
		}
		// Metadata as well as content: one file, two names.
		target, err := ofs.Lstat("target.txt")
		if err != nil {
			t.Fatalf("Lstat(\"target.txt\") = %v, want nil", err)
		}
		alias, err := ofs.Lstat("alias.txt")
		if err != nil {
			t.Fatalf("Lstat(\"alias.txt\") = %v, want nil", err)
		}
		if !os.SameFile(target, alias) {
			t.Errorf("Lstat(\"target.txt\") and Lstat(\"alias.txt\") are not the same file: %v and %v", target, alias)
		}
		// And it stays one file: a write through one name is read through the
		// other, which is what linkfs.go means by a copy not staying in step.
		if err := wfs.WriteFile("target.txt", []byte("changed"), 0o644); err != nil {
			t.Fatalf("WriteFile(\"target.txt\") = %v, want nil", err)
		}
		got, err = fsys.ReadFile("alias.txt")
		if err != nil || string(got) != "changed" {
			t.Errorf("ReadFile(\"alias.txt\") after writing target.txt = (%q, %v), want %q and nil", got, err, "changed")
		}
	})

	t.Run("removing_one_name_leaves_the_other", func(t *testing.T) {
		if err := osutil.WriteFile(filepath.Join(dir, "src.txt"), []byte("only-name")); err != nil {
			t.Fatal(err)
		}
		if err := lfs.Link("src.txt", "kept.txt"); err != nil {
			t.Fatalf("Link(\"src.txt\", \"kept.txt\") = %v, want nil", err)
		}
		if err := fsys.Remove("src.txt"); err != nil {
			t.Fatalf("Remove(\"src.txt\") = %v, want nil", err)
		}
		got, err := fsys.ReadFile("kept.txt")
		if err != nil || string(got) != "only-name" {
			t.Errorf("ReadFile(\"kept.txt\") after removing src.txt = (%q, %v), want %q and nil", got, err, "only-name")
		}
	})

	t.Run("existing_newname", func(t *testing.T) {
		err := lfs.Link("target.txt", "existing.txt")
		if !errors.Is(err, fs.ErrExist) {
			t.Errorf("Link(\"target.txt\", \"existing.txt\") = %v, want ErrExist", err)
		}
		var linkErr *os.LinkError
		if !errors.As(err, &linkErr) {
			t.Errorf("Link(\"target.txt\", \"existing.txt\") = %v, want an *os.LinkError", err)
		}
		got, err := fsys.ReadFile("existing.txt")
		if err != nil || string(got) != "kept" {
			t.Errorf("existing.txt = (%q, %v), want %q and nil: a refused link must leave the file alone", got, err, "kept")
		}
	})

	t.Run("missing_parent_of_newname", func(t *testing.T) {
		err := lfs.Link("target.txt", "ghost/alias.txt")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Link(\"target.txt\", \"ghost/alias.txt\") = %v, want ErrNotExist", err)
		}
		if _, err := fsys.Stat("ghost"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(\"ghost\") after the failed link = %v, want ErrNotExist: the parent must not be created", err)
		}
	})

	t.Run("a_directory_is_refused", func(t *testing.T) {
		// linkfs.go says most platforms refuse to hard link a directory, and
		// they report it in their own way, so only the refusal and an
		// untouched tree are required here.
		if err := lfs.Link("dir", "alias-dir"); err == nil {
			t.Error("Link(\"dir\", \"alias-dir\") succeeded, want an error")
		}
		if _, err := fsys.Stat("alias-dir"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(\"alias-dir\") after the refused link = %v, want ErrNotExist", err)
		}
		info, err := fsys.Stat("dir")
		if err != nil || !info.IsDir() {
			t.Errorf("Stat(\"dir\") = (%v, %v), want a directory", info, err)
		}
	})
}
