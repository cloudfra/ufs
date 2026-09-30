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

package overlay

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/cloudfra/ufs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/internal/ufserrors"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func newMem(t *testing.T, name string) ufs.FS {
	t.Helper()
	fsys, err := ufs.New(t.Context(), name)
	if err != nil {
		t.Fatalf("New(%q) = %v", name, err)
	}
	return fsys
}

func newMemLayers(t *testing.T, n int) []ufs.FS {
	t.Helper()
	layers := make([]ufs.FS, n)
	for i := range layers {
		layers[i] = newMem(t, fmt.Sprintf("memory://layer%d", i))
	}
	return layers
}

func mustNew(t *testing.T, layers ...ufs.FS) *FS {
	t.Helper()
	o, err := New(layers...)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	t.Cleanup(ufsTesting.ValidateClose(t, o))
	return o
}

func writeFile(t *testing.T, fsys ufs.WriteFS, name, content string) {
	t.Helper()
	if dir := parentDir(name); dir != "" {
		if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
			t.Fatalf("MkdirAll(%q) = %v", dir, err)
		}
	}
	ufsdriversTesting.WriteFile(t, fsys, name, content)
}

func parentDir(name string) string {
	i := strings.LastIndex(name, "/")
	if i < 0 {
		return ""
	}
	return name[:i]
}

func readDirNames(t *testing.T, fsys fs.ReadDirFS, name string) []string {
	t.Helper()
	entries, err := fsys.ReadDir(name)
	if err != nil {
		t.Fatalf("ReadDir(%q) = %v", name, err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
		if e.IsDir() {
			names[i] += "/"
		}
	}
	return names
}

func assertContent(t *testing.T, fsys ufs.ReadFS, name, want string) {
	t.Helper()
	got, err := fsys.ReadFile(name)
	if err != nil || string(got) != want {
		t.Errorf("ReadFile(%q) = (%q, %v), want (%q, nil)", name, got, err, want)
	}
}

func assertNotExist(t *testing.T, fsys ufs.ReadFS, name string) {
	t.Helper()
	if _, err := fsys.Stat(name); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(%q) = %v, want fs.ErrNotExist", name, err)
	}
	if _, err := fsys.ReadFile(name); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile(%q) = %v, want fs.ErrNotExist", name, err)
	}
	if _, err := fsys.Open(name); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open(%q) = %v, want fs.ErrNotExist", name, err)
	}
	if _, err := fsys.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Lstat(%q) = %v, want fs.ErrNotExist", name, err)
	}
}

func TestOverlayDriver(t *testing.T) {
	for _, n := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("%d layers", n), func(t *testing.T) {
			ufsdriversTesting.WriteFS(t, func(t *testing.T) ufs.WriteFS {
				o, err := New(newMemLayers(t, n)...)
				if err != nil {
					t.Fatal(err)
				}
				return o
			})
		})
	}
}

func TestNewRequiresLayers(t *testing.T) {
	if _, err := New(); err == nil {
		t.Error("New() = nil error, want an error")
	}
}

func TestShadowing(t *testing.T) {
	layers := newMemLayers(t, 3)
	writeFile(t, layers[0], "a", "top")
	writeFile(t, layers[1], "a", "middle")
	writeFile(t, layers[1], "b", "middle")
	writeFile(t, layers[2], "b", "bottom")
	writeFile(t, layers[2], "c", "bottom")
	o := mustNew(t, layers...)

	assertContent(t, o, "a", "top")
	assertContent(t, o, "b", "middle")
	assertContent(t, o, "c", "bottom")

	f, err := o.Open("c")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	if err != nil || string(data) != "bottom" {
		t.Errorf("Open(c) read = (%q, %v), want (%q, nil)", data, err, "bottom")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := o.Stat("b"); err != nil || info.Size() != int64(len("middle")) {
		t.Errorf("Stat(b) = (%v, %v), want size %d", info, err, len("middle"))
	}
	if got, want := readDirNames(t, o, "."), []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("ReadDir(.) = %v, want %v", got, want)
	}
	if o.Layer(2) != layers[2] {
		t.Error("Layer(2) is not the third layer")
	}
}

func TestReadDirMerge(t *testing.T) {
	layers := newMemLayers(t, 3)
	writeFile(t, layers[0], "d/top", "")
	writeFile(t, layers[1], "d/middle", "")
	writeFile(t, layers[1], "d/sub/x", "")
	writeFile(t, layers[2], "d/bottom", "")
	writeFile(t, layers[2], "d/top", "")
	o := mustNew(t, layers...)

	want := []string{"bottom", "middle", "sub/", "top"}
	if got := readDirNames(t, o, "d"); !slices.Equal(got, want) {
		t.Errorf("ReadDir(d) = %v, want %v", got, want)
	}

	f, err := o.Open("d")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	dir, ok := f.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("Open(d) = %T, want an fs.ReadDirFile", f)
	}
	var paged []string
	for {
		entries, err := dir.ReadDir(3)
		for _, e := range entries {
			paged = append(paged, e.Name())
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"bottom", "middle", "sub", "top"}; !slices.Equal(paged, want) {
		t.Errorf("paged ReadDir(d) = %v, want %v", paged, want)
	}
	if _, err := f.Read(make([]byte, 1)); err == nil {
		t.Error("Read on a directory = nil, want an error")
	}
	if info, err := f.Stat(); err != nil || !info.IsDir() {
		t.Errorf("Stat() = (%v, %v), want a directory", info, err)
	}
}

func TestFileDirConflicts(t *testing.T) {
	layers := newMemLayers(t, 2)
	writeFile(t, layers[0], "x", "file on top")
	writeFile(t, layers[1], "x/child", "hidden by the file above")
	writeFile(t, layers[1], "y", "file below")
	writeFile(t, layers[0], "z/child", "dir on top")
	writeFile(t, layers[1], "z", "file below a dir")
	o := mustNew(t, layers...)

	if info, err := o.Stat("x"); err != nil || info.IsDir() {
		t.Errorf("Stat(x) = (%v, %v), want the top layer's file", info, err)
	}
	if _, err := o.ReadDir("x"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("ReadDir(x) = %v, want fs.ErrInvalid", err)
	}
	if info, err := o.Stat("z"); err != nil || !info.IsDir() {
		t.Errorf("Stat(z) = (%v, %v), want the top layer's directory", info, err)
	}
	if got, want := readDirNames(t, o, "z"), []string{"child"}; !slices.Equal(got, want) {
		t.Errorf("ReadDir(z) = %v, want %v", got, want)
	}

	// Writes through the overlay can't contradict the merged view.
	if _, err := o.Create("y/child"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Create(y/child) under a lower file = %v, want fs.ErrExist", err)
	}
	if err := o.MkdirAll("y", fs.ModePerm); !errors.Is(err, fs.ErrExist) {
		t.Errorf("MkdirAll(y) over a lower file = %v, want fs.ErrExist", err)
	}
	if err := o.MkdirAll("y/sub", fs.ModePerm); !errors.Is(err, fs.ErrExist) {
		t.Errorf("MkdirAll(y/sub) under a lower file = %v, want fs.ErrExist", err)
	}
	writeFile(t, layers[1], "lowerdir/f", "")
	if _, err := o.Create("lowerdir"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Create(lowerdir) over a lower directory = %v, want fs.ErrInvalid", err)
	}
	if _, err := layers[0].Stat("y"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("layer 0 Stat(y) = %v, want fs.ErrNotExist after rejected writes", err)
	}
}

func TestCreateUnderLowerDirectory(t *testing.T) {
	layers := newMemLayers(t, 2)
	writeFile(t, layers[1], "a/b/old", "old")
	o := mustNew(t, layers...)

	writeFile(t, o, "a/b/new", "new")
	if got, want := readDirNames(t, o, "a/b"), []string{"new", "old"}; !slices.Equal(got, want) {
		t.Errorf("ReadDir(a/b) = %v, want %v", got, want)
	}
	assertContent(t, layers[0], "a/b/new", "new")
	if _, err := layers[1].Stat("a/b/new"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("lower layer Stat(a/b/new) = %v, want fs.ErrNotExist", err)
	}
}

func TestRemoveHidesLowerLayers(t *testing.T) {
	layers := newMemLayers(t, 3)
	writeFile(t, layers[0], "both", "top")
	writeFile(t, layers[2], "both", "bottom")
	writeFile(t, layers[1], "lower", "middle")
	writeFile(t, layers[0], "upper", "top")
	o := mustNew(t, layers...)

	for _, name := range []string{"both", "lower", "upper"} {
		if err := o.Remove(name); err != nil {
			t.Fatalf("Remove(%q) = %v", name, err)
		}
		assertNotExist(t, o, name)
		if !o.Hidden(name) {
			t.Errorf("Hidden(%q) = false after Remove", name)
		}
	}
	if got := readDirNames(t, o, "."); len(got) != 0 {
		t.Errorf("ReadDir(.) = %v, want empty", got)
	}
	// The lower layers are untouched; the owner applies the removal.
	assertContent(t, layers[2], "both", "bottom")
	assertContent(t, layers[1], "lower", "middle")

	if err := o.Remove("lower"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Remove(lower) again = %v, want fs.ErrNotExist", err)
	}
	var pe *fs.PathError
	if err := o.Remove("missing"); !errors.As(err, &pe) || pe.Op != "remove" || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Remove(missing) = %v, want a remove *fs.PathError wrapping fs.ErrNotExist", err)
	}
	if err := o.Remove("."); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Remove(.) = %v, want fs.ErrPermission", err)
	}
}

func TestRemoveNonEmptyMergedDirectory(t *testing.T) {
	layers := newMemLayers(t, 2)
	if err := layers[0].MkdirAll("d", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	writeFile(t, layers[1], "d/lower", "")
	o := mustNew(t, layers...)

	if err := o.Remove("d"); !errors.Is(err, ufserrors.ErrDirNotEmpty) {
		t.Errorf("Remove(d) = %v, want ErrDirNotEmpty", err)
	}
	if o.Hidden("d") {
		t.Error("Hidden(d) = true after a failed Remove")
	}
	if err := o.Remove("d/lower"); err != nil {
		t.Fatalf("Remove(d/lower) = %v", err)
	}
	if err := o.Remove("d"); err != nil {
		t.Errorf("Remove(d) once empty = %v", err)
	}
	assertNotExist(t, o, "d")
}

func TestRemoveAllHidesSubtree(t *testing.T) {
	layers := newMemLayers(t, 2)
	writeFile(t, layers[1], "d/a", "")
	writeFile(t, layers[1], "d/sub/b", "")
	writeFile(t, layers[1], "keep", "")
	writeFile(t, layers[0], "d/top", "")
	o := mustNew(t, layers...)

	if err := o.RemoveAll("d"); err != nil {
		t.Fatalf("RemoveAll(d) = %v", err)
	}
	for _, name := range []string{"d", "d/a", "d/sub", "d/sub/b", "d/top"} {
		assertNotExist(t, o, name)
	}
	if _, err := o.ReadDir("d"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadDir(d) = %v, want fs.ErrNotExist", err)
	}
	if got, want := readDirNames(t, o, "."), []string{"keep"}; !slices.Equal(got, want) {
		t.Errorf("ReadDir(.) = %v, want %v", got, want)
	}
	matches, err := fs.Glob(o, "d/*")
	if err != nil || len(matches) != 0 {
		t.Errorf("Glob(d/*) = (%v, %v), want no matches", matches, err)
	}
	if err := o.RemoveAll("missing"); err != nil {
		t.Errorf("RemoveAll(missing) = %v, want nil", err)
	}

	// Recreating d in layer 0 must not bring back its old lower contents.
	writeFile(t, o, "d/new", "new")
	if got, want := readDirNames(t, o, "d"), []string{"new"}; !slices.Equal(got, want) {
		t.Errorf("ReadDir(d) after recreate = %v, want %v", got, want)
	}
	assertNotExist(t, o, "d/a")
	assertContent(t, o, "d/new", "new")
	matches, err = fs.Glob(o, "d/*")
	if err != nil || !slices.Equal(matches, []string{"d/new"}) {
		t.Errorf("Glob(d/*) = (%v, %v), want [d/new]", matches, err)
	}
}

func TestRemoveAllRoot(t *testing.T) {
	layers := newMemLayers(t, 2)
	writeFile(t, layers[0], "a", "")
	writeFile(t, layers[1], "b/c", "")
	o := mustNew(t, layers...)

	if err := o.RemoveAll("."); err != nil {
		t.Fatalf("RemoveAll(.) = %v", err)
	}
	if got := readDirNames(t, o, "."); len(got) != 0 {
		t.Errorf("ReadDir(.) = %v, want empty", got)
	}
	if info, err := o.Stat("."); err != nil || !info.IsDir() {
		t.Errorf("Stat(.) = (%v, %v), want the root directory", info, err)
	}
	writeFile(t, o, "new", "")
	if got, want := readDirNames(t, o, "."), []string{"new"}; !slices.Equal(got, want) {
		t.Errorf("ReadDir(.) = %v, want %v", got, want)
	}
}

func TestReadDirHidesChild(t *testing.T) {
	layers := newMemLayers(t, 2)
	writeFile(t, layers[1], "d/x", "")
	writeFile(t, layers[1], "d/y", "")
	o := mustNew(t, layers...)

	if err := o.Remove("d/x"); err != nil {
		t.Fatal(err)
	}
	if got, want := readDirNames(t, o, "d"), []string{"y"}; !slices.Equal(got, want) {
		t.Errorf("ReadDir(d) = %v, want %v", got, want)
	}
	f, err := o.Open("d")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := f.(fs.ReadDirFile).ReadDir(-1)
	if err != nil || len(entries) != 1 || entries[0].Name() != "y" {
		t.Errorf("Open(d).ReadDir(-1) = (%v, %v), want [y]", entries, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTombstones(t *testing.T) {
	layers := newMemLayers(t, 2)
	writeFile(t, layers[1], "a", "")
	writeFile(t, layers[1], "b", "")
	o := mustNew(t, layers...)

	if got := o.Tombstones(); got != nil {
		t.Errorf("Tombstones() = %v, want nil", got)
	}
	if err := o.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if err := o.RemoveAll("b"); err != nil {
		t.Fatal(err)
	}
	snapshot := o.Tombstones()
	if len(snapshot) != 2 {
		t.Fatalf("Tombstones() = %v, want 2 entries", snapshot)
	}

	// b is removed again after the snapshot, so clearing the snapshot must
	// keep b's newer tombstone.
	writeFile(t, o, "b", "new")
	if err := o.RemoveAll("b"); err != nil {
		t.Fatal(err)
	}
	o.ClearTombstones(snapshot)
	if o.Hidden("a") {
		t.Error("Hidden(a) = true after ClearTombstones")
	}
	if !o.Hidden("b") {
		t.Error("Hidden(b) = false, want the newer tombstone kept")
	}
	assertNotExist(t, o, "b")
	// With a's tombstone cleared, the lower layer's copy is visible again:
	// clearing is only correct once the owner has removed it there.
	assertContent(t, o, "a", "")
}

// failingFS fails every mutation on its embedded FS with err.
type failingFS struct {
	ufs.FS
	err error
}

func (f *failingFS) Remove(string) error    { return f.err }
func (f *failingFS) RemoveAll(string) error { return f.err }

func TestFailedRemoveDropsTombstone(t *testing.T) {
	lower := newMem(t, "memory://lower")
	writeFile(t, lower, "f", "")
	boom := errors.New("boom")
	top := &failingFS{FS: newMem(t, "memory://top"), err: boom}
	o := mustNew(t, top, lower)

	if err := o.Remove("f"); !errors.Is(err, boom) {
		t.Errorf("Remove(f) = %v, want %v", err, boom)
	}
	if err := o.RemoveAll("f"); !errors.Is(err, boom) {
		t.Errorf("RemoveAll(f) = %v, want %v", err, boom)
	}
	if o.Hidden("f") {
		t.Error("Hidden(f) = true after failed removes")
	}
	assertContent(t, o, "f", "")
}

func TestSingleLayerPassesThrough(t *testing.T) {
	layer := newMem(t, "memory://only")
	writeFile(t, layer, "d/f", "x")
	o := mustNew(t, layer)
	if err := o.Remove("d/f"); err != nil {
		t.Fatal(err)
	}
	if o.Hidden("d/f") {
		t.Error("Hidden(d/f) = true; a single layer needs no tombstones")
	}
	if err := o.RemoveAll("d"); err != nil {
		t.Fatal(err)
	}
	assertNotExist(t, o, "d")
}

func TestStringAndURI(t *testing.T) {
	o := mustNew(t, newMemLayers(t, 2)...)
	if got, want := o.String(), "overlay("+o.Layer(0).String()+", "+o.Layer(1).String()+")"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	u, err := o.URI()
	if err != nil || u.String() != "memory://layer0" {
		t.Errorf("URI() = (%v, %v), want memory://layer0", u, err)
	}
	if got := o.GetDeviceInfo(); len(got) == 0 {
		t.Error("GetDeviceInfo() is empty, want layer 0's device")
	}
}

func TestReadLink(t *testing.T) {
	layers := newMemLayers(t, 2)
	writeFile(t, layers[1], "f", "")
	o := mustNew(t, layers...)
	// memFS has no symlinks, so the lower layer's ReadLink answers with
	// fs.ErrInvalid rather than the top layer's fs.ErrNotExist.
	if _, err := o.ReadLink("f"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("ReadLink(f) = %v, want fs.ErrInvalid from the lower layer", err)
	}
	if _, err := o.ReadLink("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadLink(missing) = %v, want fs.ErrNotExist", err)
	}
}
