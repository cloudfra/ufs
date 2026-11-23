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

// TestLocalFSChmod covers localFS.Chmod, the method that makes it a
// types.ChmodFS. Which bits of mode a backend keeps is a platform question --
// chmodfs.go says Unix keeps the permission bits and Windows keeps only enough
// to say whether the file is writable -- so this file asks only about that much
// and about the two things the interface says everywhere: a name that does not
// exist is fs.ErrNotExist, and a name outside the root is refused. The exact
// bits are checked in localfs_chmod_unix_test.go.
func TestLocalFSChmod(t *testing.T) {
	dir := t.TempDir()
	fsys, err := newLocalFS(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	cfs, ok := fsys.(types.ChmodFS)
	if !ok {
		t.Fatal("localFS does not implement types.ChmodFS")
	}

	if err := osutil.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.WriteFile(filepath.Join(dir, "target.txt"), []byte("seed")); err != nil {
		t.Fatal(err)
	}

	t.Run("writability_of_a_file", func(t *testing.T) {
		if err := cfs.Chmod("notes.txt", 0o444); err != nil {
			t.Fatalf("Chmod(\"notes.txt\", 0444) = %v, want nil", err)
		}
		info, err := fsys.Stat("notes.txt")
		if err != nil {
			t.Fatalf("Stat(\"notes.txt\") = %v, want nil", err)
		}
		if info.Mode().Perm()&0o200 != 0 {
			t.Errorf("mode after Chmod(0444) = %v, want a file its owner cannot write", info.Mode())
		}
		if err := cfs.Chmod("notes.txt", 0o666); err != nil {
			t.Fatalf("Chmod(\"notes.txt\", 0666) = %v, want nil", err)
		}
		info, err = fsys.Stat("notes.txt")
		if err != nil {
			t.Fatalf("Stat(\"notes.txt\") = %v, want nil", err)
		}
		if info.Mode().Perm()&0o200 == 0 {
			t.Errorf("mode after Chmod(0666) = %v, want a writable file", info.Mode())
		}
		// Chmod changes the mode of a node that already exists, so the data
		// stays where it is.
		got, err := fsys.ReadFile("notes.txt")
		if err != nil || string(got) != "hello" {
			t.Errorf("notes.txt = (%q, %v), want %q and nil", got, err, "hello")
		}
	})

	t.Run("missing_name", func(t *testing.T) {
		err := cfs.Chmod("ghost.txt", 0o644)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Chmod(\"ghost.txt\") = %v, want ErrNotExist", err)
		}
		var pathErr *fs.PathError
		if !errors.As(err, &pathErr) {
			t.Errorf("Chmod(\"ghost.txt\") = %v, want a *fs.PathError", err)
		}
	})

	t.Run("invalid_paths", func(t *testing.T) {
		before, err := localFSPerm(fsys, "target.txt")
		if err != nil {
			t.Fatal(err)
		}
		names := []string{"", "/abs.txt", "../escape.txt", "a/../../b", "dir/../target.txt", "target.txt/", "a//b"}
		for _, name := range names {
			err := cfs.Chmod(name, 0o600)
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Chmod(%q) = %v, want ErrInvalid", name, err)
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("Chmod(%q) = %v, want a *fs.PathError", name, err)
			} else if pathErr.Op != "chmod" {
				t.Errorf("Chmod(%q) error Op = %q, want %q", name, pathErr.Op, "chmod")
			}
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("file created outside the root: %v", err)
		}
		// A refused name must not change the mode of anything that is there.
		after, err := localFSPerm(fsys, "target.txt")
		if err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Errorf("mode of target.txt after the refused calls = %v, want %v", after, before)
		}
	})
}
