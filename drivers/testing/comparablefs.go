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

package testing

import (
	"io/fs"
	"slices"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func comparableFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("comparableFS", func(t *testing.T) {
		requireType[types.ComparableFS](t, args)
		t.Parallel()
		createFS := requireFSTypeFunc[types.ComparableFS](t, createFSFunc)
		isSameSelf(t, createFS)
		isSameIndependent(t, args, createFS)
	})
}

// isSameSelf asserts a file system reports that it holds its own data, which is
// what allows a file system to copy or rename a file inside itself.
func isSameSelf(t *testing.T, createFSFunc func(t *testing.T) types.ComparableFS) {
	t.Run("isSameSelf", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		if !fsys.IsSame(fsys) {
			t.Errorf("%T.IsSame(itself) = false, want true, because a file system always holds its own data", fsys)
		}
	})
}

// isSameIndependent checks what a file system reports about a second file
// system built the same way against what the two actually hold, so that a
// file system cannot claim one set of data while storing separately, which
// would move data into the wrong file system, nor claim separate data while
// storing together, which would hide a file system that can copy natively.
func isSameIndependent(t *testing.T, args Args, createFSFunc func(t *testing.T) types.ComparableFS) {
	t.Run("isSameIndependent", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))
		otherFS := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, otherFS))

		same := fsys.IsSame(otherFS)
		if otherReport := otherFS.IsSame(fsys); otherReport != same {
			t.Errorf("%T.IsSame(%T) = %t and %T.IsSame(%T) = %t, want one answer for the pair",
				fsys, otherFS, same, otherFS, fsys, otherReport)
		}

		if args.ReadOnly {
			if same {
				assertSameEntries(t, fsys, otherFS)
			}
			return
		}
		requireType[types.CreateFileFS](t, args)

		const markerPath = "comparable-marker.txt"
		WriteFile(t, fsys.(types.CreateFileFS), markerPath, "written by the first file system")
		_, err := fs.Stat(otherFS, markerPath)
		switch {
		case same && err != nil:
			t.Errorf("fs.Stat(%T, %q) = %v after %T wrote that path, want the write to be seen because IsSame reported true", otherFS, markerPath, err, fsys)
		case !same && err == nil:
			t.Errorf("fs.Stat(%T, %q) = nil after %T wrote that path, want the write to stay unseen because IsSame reported false", otherFS, markerPath, fsys)
		}
	})
}

// assertSameEntries asserts that two file systems reporting one set of data
// list the same entries at their root.
func assertSameEntries(t *testing.T, fsys, otherFS types.ComparableFS) {
	t.Helper()
	names, err := entryNames(t, fsys)
	if err != nil {
		t.Skipf("fs.ReadDir(%T, %q) = %v, so the entries the two file systems claim to share cannot be compared", fsys, ".", err)
	}
	otherNames, err := entryNames(t, otherFS)
	if err != nil {
		t.Skipf("fs.ReadDir(%T, %q) = %v, so the entries the two file systems claim to share cannot be compared", otherFS, ".", err)
	}
	if !slices.Equal(names, otherNames) {
		t.Errorf("%T lists %q and %T lists %q, want the same entries because IsSame reported true", fsys, names, otherFS, otherNames)
	}
}

func entryNames(tb testing.TB, fsys fs.FS) ([]string, error) {
	tb.Helper()
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names, nil
}
