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
	"errors"
	"io/fs"
	"slices"
	"testing"

	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
)

// markerPath is the name the checks below create through one file system and
// then look for in the next one handed out by the same helper.
const markerPath = "decorators-testing-marker.txt"

// TestInnerFSIsAnEmptyStore checks that InnerFS hands a suite a store of its
// own: nothing is in it to begin with, a write through it is listed, and a later
// call does not carry what an earlier call's store was given.
func TestInnerFSIsAnEmptyStore(t *testing.T) {
	t.Parallel()

	fsys := InnerFS(t)
	if names := entryNames(t, fsys, "."); len(names) != 0 {
		t.Errorf("InnerFS lists %q at %q, want no entries so a suite can create its own names", names, ".")
	}

	ufsdriversTesting.WriteFile(t, fsys, markerPath, "written through the inner file system")
	if names := entryNames(t, fsys, "."); !slices.Equal(names, []string{markerPath}) {
		t.Errorf("InnerFS lists %q after a write, want %q", names, markerPath)
	}

	assertStoreIsNew(t, InnerFS(t), markerPath)
}

// TestSeededInnerFSHoldsItsTree pins the tree a read-only suite is checked
// against: a file in a directory and a second one a level deeper, so a decorator
// that flattens or hides a level cannot pass by presenting both at the root.
func TestSeededInnerFSHoldsItsTree(t *testing.T) {
	t.Parallel()

	fsys := SeededInnerFS(t)
	for _, tc := range []struct {
		dir  string
		want []string
	}{
		{dir: ".", want: []string{"readconf"}},
		{dir: "readconf", want: []string{"alpha.txt", "nested"}},
		{dir: "readconf/nested", want: []string{"beta.txt"}},
	} {
		if names := entryNames(t, fsys, tc.dir); !slices.Equal(names, tc.want) {
			t.Errorf("SeededInnerFS lists %q at %q, want %q", names, tc.dir, tc.want)
		}
	}

	for _, tc := range []struct {
		name        string
		wantContent string
	}{
		{name: "readconf/alpha.txt", wantContent: "alpha"},
		{name: "readconf/nested/beta.txt", wantContent: "beta content"},
	} {
		content, err := fs.ReadFile(fsys, tc.name)
		if err != nil {
			t.Fatalf("fs.ReadFile(SeededInnerFS, %q) = %v, want the seeded content", tc.name, err)
		}
		if string(content) != tc.wantContent {
			t.Errorf("fs.ReadFile(SeededInnerFS, %q) = %q, want %q", tc.name, content, tc.wantContent)
		}
	}

	assertStoreIsNew(t, SeededInnerFS(t), markerPath)
}

// assertStoreIsNew checks that fsys holds none of what was written through the
// file system the helper created before it, which is what keeps a suite from
// finding a name a previous suite created.
func assertStoreIsNew(t *testing.T, fsys types.WriteFS, name string) {
	t.Helper()
	if _, err := fsys.Open(name); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the next file system from the same helper opened %q = %v, want an error wrapping %v, because a suite must get a store of its own", name, err, fs.ErrNotExist)
	}
}

func entryNames(tb testing.TB, fsys fs.FS, dir string) []string {
	tb.Helper()
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		tb.Fatalf("fs.ReadDir(%q) = %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names
}
