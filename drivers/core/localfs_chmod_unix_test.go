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

// TestLocalFSChmodUnixBits covers the bits chmodfs.go says Unix keeps. A
// Chmod request is not run through the process umask, so the mode that comes
// back is the one that was asked for, and the setuid, setgid and sticky bits
// travel with the permission bits.
func TestLocalFSChmodUnixBits(t *testing.T) {
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

	t.Run("a_file_keeps_every_requested_bit", func(t *testing.T) {
		for _, mode := range []fs.FileMode{0o640, 0o444, 0o604, 0o755} {
			name := "bits-" + fmt.Sprintf("%03o", mode) + ".txt"
			if err := osutil.WriteFile(filepath.Join(dir, name), []byte("x")); err != nil {
				t.Fatal(err)
			}
			if err := cfs.Chmod(name, mode); err != nil {
				t.Fatalf("Chmod(%q, %v) = %v, want nil", name, mode, err)
			}
			got, err := localFSPerm(fsys, name)
			if err != nil {
				t.Fatalf("Stat(%q) = %v, want nil", name, err)
			}
			if got != mode.Perm() {
				t.Errorf("Chmod(%q, %v) left mode %v, want %v", name, mode, got, mode.Perm())
			}
		}
	})

	t.Run("a_directory_keeps_every_requested_bit", func(t *testing.T) {
		if err := osutil.MkdirAll(filepath.Join(dir, "sub")); err != nil {
			t.Fatal(err)
		}
		if err := cfs.Chmod("sub", 0o700); err != nil {
			t.Fatalf("Chmod(\"sub\", 0700) = %v, want nil", err)
		}
		got, err := localFSPerm(fsys, "sub")
		if err != nil {
			t.Fatal(err)
		}
		if got != 0o700 {
			t.Errorf("Chmod(\"sub\", 0700) left mode %v, want 0700", got)
		}
	})

	t.Run("the_setuid_and_setgid_bits_travel_with_the_rest", func(t *testing.T) {
		if err := osutil.WriteFile(filepath.Join(dir, "special.txt"), []byte("x")); err != nil {
			t.Fatal(err)
		}
		mode := fs.ModeSetuid | fs.ModeSetgid | 0o640
		if err := cfs.Chmod("special.txt", mode); err != nil {
			t.Fatalf("Chmod(\"special.txt\", %v) = %v, want nil", mode, err)
		}
		info, err := fsys.Stat("special.txt")
		if err != nil {
			t.Fatalf("Stat(\"special.txt\") = %v, want nil", err)
		}
		if info.Mode()&fs.ModeSetuid == 0 || info.Mode()&fs.ModeSetgid == 0 {
			t.Errorf("Chmod(\"special.txt\", %v) left mode %v, want the setuid and setgid bits kept", mode, info.Mode())
		}
		if got := info.Mode().Perm(); got != 0o640 {
			t.Errorf("Chmod(\"special.txt\", %v) left permission bits %v, want 0640", mode, got)
		}
	})
}
