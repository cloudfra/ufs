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
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/google/go-cmp/cmp"
)

// --- Remove ---

func setupRemoveFS(t *testing.T) WriteFS {
	t.Helper()
	fsys, err := newMemFS(t.Context(), "memory://test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "dir/b.txt"} {
		f, err := fsys.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return fsys
}

func TestRemove(t *testing.T) {
	fsys := setupRemoveFS(t)

	if err := Remove(fsys, "a.txt"); err != nil {
		t.Fatalf("Remove('a.txt') = %v, want nil", err)
	}
	if _, err := fsys.Stat("a.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("after Remove, Stat('a.txt') = %v, want ErrNotExist", err)
	}
}

func TestRemoveNotExist(t *testing.T) {
	fsys := setupRemoveFS(t)

	err := Remove(fsys, "ghost.txt")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Remove(nonexistent) = %v, want ErrNotExist", err)
	}
}

func TestRemoveNonEmptyDir(t *testing.T) {
	fsys := setupRemoveFS(t)

	err := Remove(fsys, "dir")
	if err == nil {
		t.Error("Remove(non-empty dir) succeeded, want error")
	}
}

// noRemoverFS wraps an fs.FS without exposing the Remover interface, allowing
// tests to exercise the ErrPermission fallback path in Remove/RemoveAll.
type noRemoverFS struct{ fs.FS }

func TestRemoveFallback(t *testing.T) {
	inner, err := newMemFS(t.Context(), "memory://test")
	if err != nil {
		t.Errorf("newMemFS returned an error, %s", err)
	}

	defer func() {
		if err := inner.Close(); err != nil {
			t.Errorf("failed to close inner FS: %v", err)
		}
	}()

	if err := Remove(&noRemoverFS{inner}, "any.txt"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Remove on non-Remover FS = %v, want ErrPermission", err)
	}
}

func TestRemoveAngry(t *testing.T) {
	fsys := makeAngryFS(angryFSPrefix)
	if err := Remove(fsys, "file.txt"); err == nil {
		t.Error("Remove on angry FS succeeded, want error")
	}
}

func TestRemoveNull(t *testing.T) {
	fsys := mustNullFS(t)
	if err := Remove(fsys, "file.txt"); err != nil {
		t.Errorf("Remove on nullFS = %v, want nil", err)
	}
}

// --- RemoveAll ---

func TestRemoveAll(t *testing.T) {
	fsys := setupRemoveFS(t)

	if err := RemoveAll(fsys, "dir"); err != nil {
		t.Fatalf("RemoveAll('dir') = %v, want nil", err)
	}
	files, err := listFilesForTest(fsys)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.txt"}
	if diff := cmp.Diff(want, files); diff != "" {
		t.Errorf("after RemoveAll('dir') files mismatch (-want +got):\n%s", diff)
	}
}

func TestRemoveAllNotExist(t *testing.T) {
	fsys := setupRemoveFS(t)

	// RemoveAll on a non-existent path must succeed (no-op).
	if err := RemoveAll(fsys, "ghost"); err != nil {
		t.Errorf("RemoveAll(nonexistent) = %v, want nil", err)
	}
}

func TestRemoveAllRoot(t *testing.T) {
	fsys := setupRemoveFS(t)

	if err := RemoveAll(fsys, pathutil.CwdPath); err != nil {
		t.Fatalf("RemoveAll('.') = %v, want nil", err)
	}
	files, err := listFilesForTest(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("after RemoveAll('.'), expected empty FS, got: %v", files)
	}
}

func TestRemoveAllFallback(t *testing.T) {
	inner, err := newMemFS(t.Context(), "memory://test")
	if err != nil {
		t.Errorf("newMemFS returned an error, %s", err)
	}
	defer func() {
		if err := inner.Close(); err != nil {
			t.Errorf("failed to close inner FS: %v", err)
		}
	}()

	if err := RemoveAll(&noRemoverFS{inner}, "dir"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("RemoveAll on non-Remover FS = %v, want ErrPermission", err)
	}
}

func TestRemoveAllAngry(t *testing.T) {
	fsys := makeAngryFS(angryFSPrefix)
	if err := RemoveAll(fsys, "dir"); err == nil {
		t.Error("RemoveAll on angry FS succeeded, want error")
	}
}

func TestRemoveAllNull(t *testing.T) {
	fsys := mustNullFS(t)
	if err := RemoveAll(fsys, "dir"); err != nil {
		t.Errorf("RemoveAll on nullFS = %v, want nil", err)
	}
}

// listFilesForTest returns the regular files of fsys in lexical order.
func listFilesForTest(fsys fs.FS) ([]string, error) {
	var files []string
	err := fs.WalkDir(fsys, pathutil.CwdPath, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, name)
		}
		return nil
	})
	return files, err
}
