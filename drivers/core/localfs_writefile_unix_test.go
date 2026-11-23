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
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestLocalFSWriteFileUnixMode covers the mode argument of localFS.WriteFile on
// Unix, the platform drivers/types/writefilefs.go states that promise in. A
// created file may lose only the bits the process umask withholds, so the
// request bounds the result from above and still has to leave the file readable
// and writable for its owner. A rewrite must leave the bits the file already
// has alone, whatever they are.
func TestLocalFSWriteFileUnixMode(t *testing.T) {
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

	t.Run("created_file_stays_inside_the_requested_mode", func(t *testing.T) {
		for _, mode := range []fs.FileMode{0o600, 0o640, 0o444} {
			name := "created-" + fmt.Sprintf("%03o", mode) + ".txt"
			if err := wfs.WriteFile(name, []byte("x"), mode); err != nil {
				t.Fatalf("WriteFile(%q, %v) = %v, want nil", name, mode, err)
			}
			got, err := localFSPerm(fsys, name)
			if err != nil {
				t.Fatalf("Stat(%q) = %v, want nil", name, err)
			}
			if extra := got &^ mode.Perm(); extra != 0 {
				t.Errorf("WriteFile(%q, %v) created the file with bits %v that were not requested", name, mode, extra)
			}
			if mode&0o600 == 0o600 && got&0o600 != 0o600 {
				t.Errorf("WriteFile(%q, %v) created the file with mode %v, want the owner read and write bits", name, mode, got)
			}
		}
	})

	t.Run("rewrite_neither_widens_nor_narrows_the_existing_bits", func(t *testing.T) {
		if err := osutil.WriteFile(filepath.Join(dir, "keep.txt"), []byte("old")); err != nil {
			t.Fatal(err)
		}
		before, err := localFSPerm(fsys, "keep.txt")
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []fs.FileMode{0o777, 0o600} {
			if err := wfs.WriteFile("keep.txt", []byte("new"), mode); err != nil {
				t.Fatalf("WriteFile(\"keep.txt\", %v) = %v, want nil", mode, err)
			}
			got, err := localFSPerm(fsys, "keep.txt")
			if err != nil {
				t.Fatal(err)
			}
			if got != before {
				t.Errorf("mode after rewriting with mode %v = %v, want %v: the file was already there", mode, got, before)
			}
		}
		content, err := fsys.ReadFile("keep.txt")
		if err != nil || string(content) != "new" {
			t.Errorf("keep.txt = (%q, %v), want %q and nil", content, err, "new")
		}
	})
}
