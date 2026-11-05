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

package yamlfs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
)

const document = `
name: ufs
version: 1.20
modes: 0755
nothing:
"1": numeric
tags:
  - fs
  - yaml
server:
  host: localhost
  port: 8080
  features: [cache, metrics]
empty: {}
`

func newTestFS(t *testing.T, name string, data []byte) types.WriteFS {
	t.Helper()
	fsys, err := New(name, data)
	if err != nil {
		t.Fatalf("New(%q) = %v, want nil", name, err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	return fsys
}

func TestDriverTest(t *testing.T) {
	ufsdriversTesting.ReadFS(t, func(t *testing.T) types.ReadFS {
		return newTestFS(t, "document.yaml", []byte(document))
	})
}

func TestTestFS(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))
	want := []string{
		"empty",
		"modes",
		"name",
		"nothing",
		"server/features/0",
		"server/host",
		"tags/1",
		"version",
	}
	if err := fstest.TestFS(fsys, want...); err != nil {
		t.Errorf("fstest.TestFS() = %v, want nil", err)
	}
}

func TestWalkDir(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	var got []string
	err := fs.WalkDir(fsys, ".", func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		got = append(got, path)
		return nil
	})
	if err != nil {
		t.Fatalf("fs.WalkDir() = %v, want nil", err)
	}
	want := []string{
		".",
		"1",
		"empty",
		"modes",
		"name",
		"nothing",
		"server",
		"server/features",
		"server/features/0",
		"server/features/1",
		"server/host",
		"server/port",
		"tags",
		"tags/0",
		"tags/1",
		"version",
	}
	if !slices.Equal(got, want) {
		t.Errorf("fs.WalkDir() visited %q, want %q", got, want)
	}
}

func TestReadFile(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	for _, tc := range []struct {
		path string
		want string
	}{
		{"name", "ufs"},
		{"version", "1.20"},
		{"modes", "0755"},
		{"nothing", ""},
		{"1", "numeric"},
		{"tags/0", "fs"},
		{"tags/1", "yaml"},
		{"server/host", "localhost"},
		{"server/port", "8080"},
		{"server/features/0", "cache"},
		{"server/features/1", "metrics"},
	} {
		got, err := fsys.ReadFile(tc.path)
		if err != nil {
			t.Errorf("ReadFile(%q) = %v, want nil", tc.path, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("ReadFile(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestReadJSON(t *testing.T) {
	t.Parallel()
	data := []byte(`{"a":{"b":[1,{"c":"x"}]},"d":"e"}`)
	fsys := newTestFS(t, "document.json", data)

	for _, tc := range []struct {
		path string
		want string
	}{
		{"a/b/0", "1"},
		{"a/b/1/c", "x"},
		{"d", "e"},
	} {
		got, err := fsys.ReadFile(tc.path)
		if err != nil {
			t.Errorf("ReadFile(%q) = %v, want nil", tc.path, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("ReadFile(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestReadDirNames(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	for _, tc := range []struct {
		path string
		want []string
	}{
		{".", []string{"1", "empty", "modes", "name", "nothing", "server", "tags", "version"}},
		{"tags", []string{"0", "1"}},
		{"empty", nil},
	} {
		entries, err := fsys.ReadDir(tc.path)
		if err != nil {
			t.Errorf("ReadDir(%q) = %v, want nil", tc.path, err)
			continue
		}
		got := make([]string, 0, len(entries))
		for _, entry := range entries {
			got = append(got, entry.Name())
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("ReadDir(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestModes(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	dir, err := fsys.Stat("server")
	if err != nil {
		t.Fatalf("Stat(server) = %v, want nil", err)
	}
	if !dir.IsDir() {
		t.Errorf("Stat(server).IsDir() = false, want true")
	}
	if want := fs.ModeDir | fs.FileMode(0o555); dir.Mode() != want {
		t.Errorf("Stat(server).Mode() = %v, want %v", dir.Mode(), want)
	}
	if dir.ModTime() != (time.Time{}) {
		t.Errorf("Stat(server).ModTime() = %v, want the zero time", dir.ModTime())
	}

	file, err := fsys.Stat("name")
	if err != nil {
		t.Fatalf("Stat(name) = %v, want nil", err)
	}
	if file.Mode() != fs.FileMode(0o444) {
		t.Errorf("Stat(name).Mode() = %v, want %v", file.Mode(), fs.FileMode(0o444))
	}
	if file.Size() != int64(len("ufs")) {
		t.Errorf("Stat(name).Size() = %d, want %d", file.Size(), len("ufs"))
	}

	empty, err := fsys.Stat("nothing")
	if err != nil {
		t.Fatalf("Stat(nothing) = %v, want nil", err)
	}
	if empty.Size() != 0 {
		t.Errorf("Stat(nothing).Size() = %d, want 0", empty.Size())
	}
}

func TestMergeKeys(t *testing.T) {
	t.Parallel()
	data := []byte(`
one: &one
  shared: from-one
  picked: from-one
two: &two
  shared: from-two
  added: from-two
merged:
  <<: [*one, *two]
  picked: own
`)
	fsys := newTestFS(t, "merged.yaml", data)

	for _, tc := range []struct {
		path string
		want string
	}{
		{"merged/picked", "own"},
		{"merged/shared", "from-one"},
		{"merged/added", "from-two"},
	} {
		got, err := fsys.ReadFile(tc.path)
		if err != nil {
			t.Errorf("ReadFile(%q) = %v, want nil", tc.path, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("ReadFile(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
	if _, err := fsys.Stat("merged/<<"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(merged/<<) = %v, want fs.ErrNotExist", err)
	}
}

func TestAliases(t *testing.T) {
	t.Parallel()
	data := []byte(`
anchor: &anchor
  name: shared
copy: *anchor
`)
	fsys := newTestFS(t, "aliases.yaml", data)

	for _, path := range []string{"anchor/name", "copy/name"} {
		got, err := fsys.ReadFile(path)
		if err != nil {
			t.Errorf("ReadFile(%q) = %v, want nil", path, err)
			continue
		}
		if string(got) != "shared" {
			t.Errorf("ReadFile(%q) = %q, want %q", path, got, "shared")
		}
	}
}

// fanOut returns a document of levels plus one mappings, each of which aliases
// the mapping before it three times, so its tree grows faster than the document.
func fanOut(levels int) string {
	document := "level0: &level0\n  name: zero\n"
	for level := 1; level <= levels; level++ {
		name := "level" + strconv.Itoa(level)
		previous := "*level" + strconv.Itoa(level-1)
		document += name + ": &" + name + "\n  a0: " + previous +
			"\n  a1: " + previous + "\n  a2: " + previous + "\n"
	}
	return document
}

// budgetDocument returns a document that holds a mapping of keys keys and
// aliases that mapping aliases times, so the directory slots its aliases add
// pass the entry budget.
func budgetDocument(keys, aliases int) string {
	document := "base: &base\n"
	for i := range keys {
		document += "  k" + strconv.Itoa(i) + ": v\n"
	}
	for i := range aliases {
		document += "copy" + strconv.Itoa(i) + ": *base\n"
	}
	return document
}

func TestAliasFanOut(t *testing.T) {
	t.Parallel()
	// Ten levels of three aliases each would add about 3**10 entries if an
	// alias built its anchor again, so the entry budget only accepts this
	// document while aliases share the entries their anchor contributes.
	const levels = 10
	fsys := newTestFS(t, "fan-out.yaml", []byte(fanOut(levels)))

	deep := "level" + strconv.Itoa(levels)
	for range levels {
		deep += "/a1"
	}
	got, err := fsys.ReadFile(deep + "/name")
	if err != nil {
		t.Fatalf("ReadFile(%q) = %v, want nil", deep+"/name", err)
	}
	if string(got) != "zero" {
		t.Errorf("ReadFile(%q) = %q, want %q", deep+"/name", got, "zero")
	}

	for _, tc := range []struct {
		path string
		want []string
	}{
		{"level1/a0", []string{"name"}},
		{"level2/a0", []string{"a0", "a1", "a2"}},
		{"level10/a2/a2/a2", []string{"a0", "a1", "a2"}},
	} {
		entries, err := fsys.ReadDir(tc.path)
		if err != nil {
			t.Errorf("ReadDir(%q) = %v, want nil", tc.path, err)
			continue
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		if !slices.Equal(names, tc.want) {
			t.Errorf("ReadDir(%q) = %q, want %q", tc.path, names, tc.want)
		}
	}
}

// mergeDocument returns a mapping that merges a shared anchor of keys entries
// parents times, so each parent adds the anchor's entries to its own directory.
func mergeDocument(keys, parents int) string {
	document := "base: &base\n"
	for i := range keys {
		document += "  k" + strconv.Itoa(i) + ": v\n"
	}
	document += "root:\n"
	for i := range parents {
		document += "  p" + strconv.Itoa(i) + ":\n    <<: *base\n"
	}
	return document
}

// TestEntryBudget pins what the budget charges at its boundary, so that a
// document reports how much it needs before it is refused.
func TestEntryBudget(t *testing.T) {
	t.Parallel()
	// A plain entry costs itself plus a slot in the directory listing it, and a
	// directory pays for its own entry as well, so the shape budgetDocument
	// builds accepts (maxEntrySlots - 3) / 2 entries and refuses one more.
	const plainCeiling = (maxEntrySlots - 3) / 2
	budgetErr := fmt.Sprintf("more than %d entries and directory slots", maxEntrySlots)
	for _, tc := range []struct {
		name    string
		data    string
		wantErr bool
	}{
		{"a plain document at the ceiling", budgetDocument(plainCeiling, 0), false},
		{"a plain document past the ceiling", budgetDocument(plainCeiling+1, 0), true},
		{"merges inside the budget", mergeDocument(200, 300), false},
		{"merges past the budget", mergeDocument(200, 325), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys, err := New("budget.yaml", []byte(tc.data))
			if tc.wantErr {
				if err == nil {
					if closeErr := fsys.Close(); closeErr != nil {
						t.Errorf("Close() = %v", closeErr)
					}
					t.Fatalf("New() = a file system, want an error mentioning %q", budgetErr)
				}
				if !strings.Contains(err.Error(), budgetErr) {
					t.Errorf("New() = %v, want an error mentioning %q", err, budgetErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("New() = %v, want nil", err)
			}
			if err := fsys.Close(); err != nil {
				t.Errorf("Close() = %v", err)
			}
		})
	}
}

func TestBinaryValue(t *testing.T) {
	t.Parallel()
	data := []byte("icon: !!binary >-\n  R0lG\n  ODdh\n")
	fsys := newTestFS(t, "binary.yaml", data)

	got, err := fsys.ReadFile("icon")
	if err != nil {
		t.Fatalf("ReadFile(icon) = %v, want nil", err)
	}
	if string(got) != "GIF87a" {
		t.Errorf("ReadFile(icon) = %q, want %q", got, "GIF87a")
	}
	if info, err := fsys.Stat("icon"); err != nil {
		t.Errorf("Stat(icon) = %v, want nil", err)
	} else if info.Size() != int64(len("GIF87a")) {
		t.Errorf("Stat(icon).Size() = %d, want %d", info.Size(), len("GIF87a"))
	}
}

func TestEmptyDocument(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		data string
	}{
		{"no content", ""},
		{"only a comment", "# nothing here\n"},
	} {
		fsys := newTestFS(t, tc.name, []byte(tc.data))
		entries, err := fsys.ReadDir(".")
		if err != nil {
			t.Errorf("ReadDir(.) with %s = %v, want nil", tc.name, err)
			continue
		}
		if len(entries) != 0 {
			t.Errorf("ReadDir(.) with %s = %q, want no entries", tc.name, entries)
		}
		if info, err := fsys.Stat("."); err != nil {
			t.Errorf("Stat(.) with %s = %v, want nil", tc.name, err)
		} else if !info.IsDir() {
			t.Errorf("Stat(.) with %s is not a directory", tc.name)
		}
	}
}

func TestNewErrors(t *testing.T) {
	t.Parallel()
	nested := strings.Repeat("- ", maxDepth+1) + "deep\n"
	for _, tc := range []struct {
		name    string
		data    string
		wantErr string
	}{
		{
			name:    "a top-level scalar",
			data:    "just text\n",
			wantErr: "cannot be a file system",
		},
		{
			name:    "two documents",
			data:    "a: 1\n---\nb: 2\n",
			wantErr: "more than one YAML document",
		},
		{
			name:    "a key defined twice",
			data:    "a: 1\na: 2\n",
			wantErr: `mapping key "a" is defined twice`,
		},
		{
			name:    "a key holding a path separator",
			data:    "a/b: 1\n",
			wantErr: "cannot hold a path separator",
		},
		{
			name:    "an empty key",
			data:    `"": 1` + "\n",
			wantErr: "empty key",
		},
		{
			name:    "a key naming the current directory",
			data:    `".": 1` + "\n",
			wantErr: "cannot name an entry",
		},
		{
			name:    "a key naming the parent directory",
			data:    "\"..\": 1\n",
			wantErr: "cannot name an entry",
		},
		{
			name:    "a key that is not a scalar",
			data:    "? [a, b]\n: 1\n",
			wantErr: "is not a scalar",
		},
		{
			name:    "a merge of a scalar",
			data:    "base: &base 1\nmerged:\n  <<: *base\n",
			wantErr: "cannot be merged",
		},
		{
			name:    "a document that does not parse",
			data:    "a: [1,\n",
			wantErr: "cannot parse document",
		},
		{
			name:    "nests deeper than the limit",
			data:    nested,
			wantErr: fmt.Sprintf("nests deeper than %d levels", maxDepth),
		},
		{
			name:    "needs more entries than the budget",
			data:    budgetDocument(maxEntrySlots/100, 200),
			wantErr: fmt.Sprintf("more than %d entries and directory slots", maxEntrySlots),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys, err := New("bad.yaml", []byte(tc.data))
			if err == nil {
				if closeErr := fsys.Close(); closeErr != nil {
					t.Errorf("Close() = %v", closeErr)
				}
				t.Fatalf("New() = a file system, want an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("New() = %v, want an error mentioning %q", err, tc.wantErr)
			}
			if fsys != nil {
				t.Errorf("New() returned a file system with an error, want nil")
			}
		})
	}
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	for _, tc := range []struct {
		name string
		op   func(path string) error
	}{
		{"Open", func(path string) error {
			_, err := fsys.Open(path)
			return err
		}},
		{"Stat", func(path string) error {
			_, err := fsys.Stat(path)
			return err
		}},
		{"Lstat", func(path string) error {
			_, err := fsys.Lstat(path)
			return err
		}},
		{"ReadFile", func(path string) error {
			_, err := fsys.ReadFile(path)
			return err
		}},
		{"ReadDir", func(path string) error {
			_, err := fsys.ReadDir(path)
			return err
		}},
		{"ReadLink", func(path string) error {
			_, err := fsys.ReadLink(path)
			return err
		}},
	} {
		err := tc.op("missing/file")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s(missing/file) = %v, want fs.ErrNotExist", tc.name, err)
		}
		var pathErr *fs.PathError
		if !errors.As(err, &pathErr) {
			t.Errorf("%s(missing/file) = %v, want a *fs.PathError", tc.name, err)
		} else if pathErr.Path != "missing/file" {
			t.Errorf("%s(missing/file) path = %q, want %q", tc.name, pathErr.Path, "missing/file")
		}
	}
}

func TestReadDirOnFile(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	if _, err := fsys.ReadDir("name"); !errors.Is(err, errNotDir) {
		t.Errorf("ReadDir(name) = %v, want %v", err, errNotDir)
	}
	if _, err := fsys.ReadFile("server"); !errors.Is(err, errIsDir) {
		t.Errorf("ReadFile(server) = %v, want %v", err, errIsDir)
	}
}

func TestReadLink(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	if _, err := fsys.ReadLink("name"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("ReadLink(name) = %v, want fs.ErrInvalid", err)
	}
}

func TestOpenFile(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	f, err := fsys.Open("name")
	if err != nil {
		t.Fatalf("Open(name) = %v, want nil", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	}()
	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Open(name).Stat() = %v, want nil", err)
	}
	if info.Name() != "name" {
		t.Errorf("Open(name).Stat().Name() = %q, want %q", info.Name(), "name")
	}
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll() = %v, want nil", err)
	}
	if string(got) != "ufs" {
		t.Errorf("ReadAll() = %q, want %q", got, "ufs")
	}
}

func TestOpenDir(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	f, err := fsys.Open("tags")
	if err != nil {
		t.Fatalf("Open(tags) = %v, want nil", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	}()

	if _, err := f.Read(make([]byte, 4)); !errors.Is(err, errIsDir) {
		t.Errorf("Open(tags).Read() = %v, want %v", err, errIsDir)
	}
	dir, ok := f.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("Open(tags) = %T, want an fs.ReadDirFile", f)
	}
	for _, want := range []string{"0", "1"} {
		batch, err := dir.ReadDir(1)
		if err != nil {
			t.Fatalf("ReadDir(1) = %v, want nil", err)
		}
		if len(batch) != 1 || batch[0].Name() != want {
			t.Errorf("ReadDir(1) = %q, want one entry named %q", batch, want)
		}
	}
	if _, err := dir.ReadDir(1); !errors.Is(err, io.EOF) {
		t.Errorf("ReadDir(1) past the end = %v, want io.EOF", err)
	}
	if batch, err := dir.ReadDir(-1); err != nil || len(batch) != 0 {
		t.Errorf("ReadDir(-1) past the end = (%q, %v), want no entries and nil", batch, err)
	}
}

func TestReadDirReturnsACopy(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	entries, err := fsys.ReadDir("tags")
	if err != nil {
		t.Fatalf("ReadDir(tags) = %v, want nil", err)
	}
	clear(entries)
	again, err := fsys.ReadDir("tags")
	if err != nil {
		t.Fatalf("ReadDir(tags) = %v, want nil", err)
	}
	got := make([]string, 0, len(again))
	for _, entry := range again {
		got = append(got, entry.Name())
	}
	if want := []string{"0", "1"}; !slices.Equal(got, want) {
		t.Errorf("ReadDir(tags) = %q after the first slice was cleared, want %q", got, want)
	}
}

func TestReadFileReturnsACopy(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	first, err := fsys.ReadFile("name")
	if err != nil {
		t.Fatalf("ReadFile(name) = %v, want nil", err)
	}
	first[0] = 'x'
	second, err := fsys.ReadFile("name")
	if err != nil {
		t.Fatalf("ReadFile(name) = %v, want nil", err)
	}
	if string(second) != "ufs" {
		t.Errorf("ReadFile(name) = %q after the first slice was changed, want %q", second, "ufs")
	}
}

func TestReadOnly(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	for _, tc := range []struct {
		name string
		op   func() error
	}{
		{"Create", func() error {
			_, err := fsys.Create("new")
			return err
		}},
		{"MkdirAll", func() error {
			return fsys.MkdirAll("new", fs.ModePerm)
		}},
		{"Remove", func() error {
			return fsys.Remove("name")
		}},
		{"RemoveAll", func() error {
			return fsys.RemoveAll("server")
		}},
	} {
		err := tc.op()
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("%s() = %v, want fs.ErrPermission", tc.name, err)
		}
	}
}

func TestRenameRefused(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	renamer, ok := fsys.(types.RenameFileFS)
	if !ok {
		t.Fatalf("%T does not implement types.RenameFileFS", fsys)
	}
	err := renamer.Rename("name", "other")
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Rename(name, other) = %v, want fs.ErrPermission", err)
	}
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) {
		t.Errorf("Rename(name, other) = %v, want a *os.LinkError", err)
	}
}

func TestURI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		wantURI string
	}{
		{"", "yaml:///?ro=true"},
		{"document.yaml", "yaml:///document.yaml?ro=true"},
	} {
		fsys := newTestFS(t, tc.name, []byte(document))
		u, err := fsys.URI()
		if err != nil {
			t.Errorf("New(%q).URI() = %v, want nil", tc.name, err)
			continue
		}
		if got := u.String(); got != tc.wantURI {
			t.Errorf("New(%q).URI() = %q, want %q", tc.name, got, tc.wantURI)
		}
		if got := fsys.String(); !strings.HasPrefix(got, "yamlFS(") {
			t.Errorf("New(%q).String() = %q, want a yamlFS(...) wrapper", tc.name, got)
		}
		info := fsys.DeviceInfo()
		if _, ok := info["."]; !ok {
			t.Errorf("New(%q).DeviceInfo() = %q, want an entry for %q", tc.name, info, ".")
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	t.Parallel()
	fsys := newTestFS(t, "document.yaml", []byte(document))

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 32 {
				got, err := fsys.ReadFile("server/host")
				if err != nil {
					t.Errorf("ReadFile(server/host) = %v, want nil", err)
					return
				}
				if string(got) != "localhost" {
					t.Errorf("ReadFile(server/host) = %q, want %q", got, "localhost")
					return
				}
				if _, err := fsys.ReadDir("."); err != nil {
					t.Errorf("ReadDir(.) = %v, want nil", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
