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
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// localFSChtimesOld is the time TestLocalFSChtimes asks for. It is a whole
// second in the far past: the seeded file carries the time the seeding ran, and
// [types.ChtimesFS] leaves a back end free to round to the second.
var localFSChtimesOld = time.Date(2001, time.February, 3, 4, 5, 6, 0, time.UTC)

// TestLocalFSChtimes covers localFS.Chtimes, the method that makes it a
// types.ChtimesFS. The conformance suite already asks a local file system what
// it asks every file system, including whether both times arrive; this file
// pins the two answers only a local file system can give. A failed change is
// reported under the name of the syscall the platform ran, which is not the ufs
// verb, while a name that reaches outside the root is refused by the driver's
// own check before any syscall runs and so names the ufs verb. How precisely the
// stored times keep a fraction of a second is left alone, because that belongs to
// the file system under the directory rather than to localFS.
func TestLocalFSChtimes(t *testing.T) {
	dir := t.TempDir()
	fsys, err := newLocalFS(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	cfs, ok := fsys.(types.ChtimesFS)
	if !ok {
		t.Fatal("localFS does not implement types.ChtimesFS")
	}

	if err := osutil.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello")); err != nil {
		t.Fatal(err)
	}

	t.Run("missing_name", func(t *testing.T) {
		err := cfs.Chtimes("ghost.txt", time.Time{}, localFSChtimesOld)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Chtimes(\"ghost.txt\") = %v, want ErrNotExist", err)
		}
		var pathErr *fs.PathError
		if !errors.As(err, &pathErr) {
			t.Errorf("Chtimes(\"ghost.txt\") = %v, want a *fs.PathError", err)
		} else if pathErr.Op != "chtimesat" {
			t.Errorf("Chtimes(\"ghost.txt\") error Op = %q, want %q: the syscall is what failed", pathErr.Op, "chtimesat")
		}
	})

	t.Run("invalid_paths", func(t *testing.T) {
		before, err := os.Stat(filepath.Join(dir, "notes.txt"))
		if err != nil {
			t.Fatal(err)
		}
		names := []string{"", "/abs.txt", "../escape.txt", "a/../../b", "dir/../notes.txt", "notes.txt/", "a//b"}
		for _, name := range names {
			err := cfs.Chtimes(name, time.Time{}, localFSChtimesOld)
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Chtimes(%q) = %v, want ErrInvalid", name, err)
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("Chtimes(%q) = %v, want a *fs.PathError", name, err)
			} else if pathErr.Op != "chtimes" {
				t.Errorf("Chtimes(%q) error Op = %q, want %q", name, pathErr.Op, "chtimes")
			}
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("file changed outside the root: %v", err)
		}
		// A refused name must not change the times of anything that is there.
		after, err := os.Stat(filepath.Join(dir, "notes.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if !after.ModTime().Equal(before.ModTime()) {
			t.Errorf("mtime of notes.txt after the refused calls = %s, want %s", after.ModTime(), before.ModTime())
		}
	})
}
