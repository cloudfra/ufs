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

// TestLocalFSWriteFile covers localFS.WriteFile, the method that makes it a
// types.WriteFileFS. The cases follow the promise written on that interface:
// the data replaces whatever was at name, mode applies only to a file that is
// created, and a missing parent is reported instead of created. Permission bits
// themselves are checked in localfs_writefile_unix_test.go, since which bits a
// platform keeps is a platform promise rather than one every platform can be held to.
func TestLocalFSWriteFile(t *testing.T) {
	dir := t.TempDir()
	fsys, err := newLocalFS(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	wfs, ok := fsys.(types.WriteFileFS)
	if !ok {
		t.Fatal("localFS does not implement types.WriteFileFS")
	}

	// Nodes used by the cases that must fail, so a failure can be told apart
	// from a call that wrote through something that was already there.
	if err := osutil.MkdirAll(filepath.Join(dir, "dir")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.WriteFile(filepath.Join(dir, "dir", "child.txt"), []byte("child")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.WriteFile(filepath.Join(dir, "target.txt"), []byte("seed")); err != nil {
		t.Fatal(err)
	}

	t.Run("create_and_read_back", func(t *testing.T) {
		if err := wfs.WriteFile("created.txt", []byte("hello"), 0o644); err != nil {
			t.Fatalf("WriteFile(\"created.txt\") = %v, want nil", err)
		}
		got, err := fsys.ReadFile("created.txt")
		if err != nil {
			t.Fatalf("ReadFile(\"created.txt\") = %v, want nil", err)
		}
		if string(got) != "hello" {
			t.Errorf("ReadFile(\"created.txt\") = %q, want %q", got, "hello")
		}
	})

	t.Run("empty_data", func(t *testing.T) {
		if err := wfs.WriteFile("empty.txt", nil, 0o644); err != nil {
			t.Fatalf("WriteFile(\"empty.txt\") = %v, want nil", err)
		}
		info, err := fsys.Stat("empty.txt")
		if err != nil {
			t.Fatalf("Stat(\"empty.txt\") = %v, want nil", err)
		}
		if info.Size() != 0 {
			t.Errorf("Stat(\"empty.txt\").Size() = %d, want 0", info.Size())
		}
	})

	t.Run("inside_an_existing_subdirectory", func(t *testing.T) {
		if err := wfs.WriteFile("dir/new.txt", []byte("nested"), 0o644); err != nil {
			t.Fatalf("WriteFile(\"dir/new.txt\") = %v, want nil", err)
		}
		got, err := fsys.ReadFile("dir/new.txt")
		if err != nil {
			t.Fatalf("ReadFile(\"dir/new.txt\") = %v, want nil", err)
		}
		if string(got) != "nested" {
			t.Errorf("ReadFile(\"dir/new.txt\") = %q, want %q", got, "nested")
		}
	})

	t.Run("overwrite_truncates", func(t *testing.T) {
		if err := osutil.WriteFile(filepath.Join(dir, "long.txt"), []byte("longer than the new data")); err != nil {
			t.Fatal(err)
		}
		if err := wfs.WriteFile("long.txt", []byte("short"), 0o644); err != nil {
			t.Fatalf("WriteFile(\"long.txt\") = %v, want nil", err)
		}
		got, err := fsys.ReadFile("long.txt")
		if err != nil {
			t.Fatalf("ReadFile(\"long.txt\") = %v, want nil", err)
		}
		if string(got) != "short" {
			t.Errorf("ReadFile(\"long.txt\") = %q, want %q: what was there before must be truncated away", got, "short")
		}
		info, err := fsys.Stat("long.txt")
		if err != nil {
			t.Fatalf("Stat(\"long.txt\") = %v, want nil", err)
		}
		if info.Size() != int64(len("short")) {
			t.Errorf("Stat(\"long.txt\").Size() = %d, want %d", info.Size(), len("short"))
		}
	})

	t.Run("missing_parent", func(t *testing.T) {
		err := wfs.WriteFile("ghost/child.txt", []byte("x"), 0o644)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("WriteFile(\"ghost/child.txt\") = %v, want ErrNotExist", err)
		}
		var pathErr *fs.PathError
		if !errors.As(err, &pathErr) {
			t.Errorf("WriteFile(\"ghost/child.txt\") = %v, want a *fs.PathError", err)
		}
		// Parents are not created here, as with types.CreateFileFS.
		if _, err := fsys.Stat("ghost"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(\"ghost\") after the failed write = %v, want ErrNotExist: the parent must not be created", err)
		}
	})

	t.Run("name_conflicts", func(t *testing.T) {
		// Reaching a directory or going through a regular file has no io/fs
		// sentinel, and the platforms report it differently, so these cases
		// only require a *fs.PathError and that the tree is left as it was.
		for _, name := range []string{"dir", "target.txt/child.txt", "."} {
			err := wfs.WriteFile(name, []byte("nope"), 0o644)
			if err == nil {
				t.Errorf("WriteFile(%q) succeeded, want an error", name)
				continue
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("WriteFile(%q) = %v, want a *fs.PathError", name, err)
			}
		}
		got, err := fsys.ReadFile("target.txt")
		if err != nil || string(got) != "seed" {
			t.Errorf("target.txt = (%q, %v), want %q and nil", got, err, "seed")
		}
		info, err := fsys.Stat("dir")
		if err != nil {
			t.Fatalf("Stat(\"dir\") = %v, want nil", err)
		}
		if !info.IsDir() {
			t.Errorf("Stat(\"dir\").IsDir() = false, want true")
		}
		got, err = fsys.ReadFile("dir/child.txt")
		if err != nil || string(got) != "child" {
			t.Errorf("dir/child.txt = (%q, %v), want %q and nil", got, err, "child")
		}
	})

	t.Run("existing_file_keeps_the_mode_it_has", func(t *testing.T) {
		if err := wfs.WriteFile("mode.txt", []byte("a"), 0o600); err != nil {
			t.Fatalf("WriteFile(\"mode.txt\") = %v, want nil", err)
		}
		before, err := localFSPerm(fsys, "mode.txt")
		if err != nil {
			t.Fatal(err)
		}
		if err := wfs.WriteFile("mode.txt", []byte("bb"), 0o777); err != nil {
			t.Fatalf("WriteFile(\"mode.txt\", 0777) = %v, want nil", err)
		}
		after, err := localFSPerm(fsys, "mode.txt")
		if err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Errorf("mode after a rewrite = %v, want %v: only a file that is created takes mode", after, before)
		}
		got, err := fsys.ReadFile("mode.txt")
		if err != nil || string(got) != "bb" {
			t.Errorf("mode.txt = (%q, %v), want %q and nil", got, err, "bb")
		}
	})

	t.Run("invalid_paths", func(t *testing.T) {
		names := []string{"", "/abs.txt", "../escape.txt", "a/../../b", "dir/../target.txt", "target.txt/", "a//b"}
		for _, name := range names {
			err := wfs.WriteFile(name, []byte("nope"), 0o644)
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("WriteFile(%q) = %v, want ErrInvalid", name, err)
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("WriteFile(%q) = %v, want a *fs.PathError", name, err)
			} else if pathErr.Op != "writefile" {
				t.Errorf("WriteFile(%q) error Op = %q, want %q", name, pathErr.Op, "writefile")
			}
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("file created outside the root: %v", err)
		}
		if _, err := fsys.Stat("a"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(\"a\") after the refused calls = %v, want ErrNotExist: nothing must be created in the root", err)
		}
		if _, err := fsys.Stat("b"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(\"b\") after the refused calls = %v, want ErrNotExist: nothing must be created in the root", err)
		}
	})
}

// localFSPerm returns the permission bits localFS reports for name.
func localFSPerm(fsys fs.FS, name string) (fs.FileMode, error) {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), nil
}
