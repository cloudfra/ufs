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

package ops

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/google/go-cmp/cmp"
)

func TestRemoveAll(t *testing.T) {
	fsys := setupRemoveFS(t)

	if err := RemoveAll(fsys, "dir"); err != nil {
		t.Fatalf("RemoveAll('dir') = %v, want nil", err)
	}
	files, err := ListFiles(fsys, pathutil.CwdPath)
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
	files, err := ListFiles(fsys, pathutil.CwdPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("after RemoveAll('.'), expected empty FS, got: %v", files)
	}
}

func TestRemoveAllFallback(t *testing.T) {
	inner, err := newMemFS("memory://test")
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
	fsys, err := newAngryFS("angry://")
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveAll(fsys, "dir"); err == nil {
		t.Error("RemoveAll on angry FS succeeded, want error")
	}
}

func TestRemoveAllNull(t *testing.T) {
	fsys, err := ufs.New(t.Context(), "null://")
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveAll(fsys, "dir"); err != nil {
		t.Errorf("RemoveAll on nullFS = %v, want nil", err)
	}
}
