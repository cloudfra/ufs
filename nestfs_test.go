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
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestNewNestFS(t *testing.T) {
	fsys, err := newNestFS(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	if fsys == nil {
		t.Fatal("fsys is nil")
	}
}

func TestNewNestFSInvalid(t *testing.T) {
	_, err := newNestFS(t.Context(), "invalid://scheme")
	if err == nil {
		t.Fatal("newNestFS with invalid scheme should return an error")
	}
}

func TestMountMap(t *testing.T) {
	mm := makeMountMap("test")
	defer wantCloseError(t, mm)()
	mfs := makeMemFS("memory:///")
	afs := makeAngryFS(angryFSPrefix)
	nfs := mustNullFS(t)

	must(t, mm.put("mounts/null", makeNestFS(t.Context(), nfs)))
	must(t, mm.put("mounts/mem", makeNestFS(t.Context(), mfs)))
	must(t, mm.put("mounts/angry", makeNestFS(t.Context(), afs)))
	must(t, mm.put("null", makeNestFS(t.Context(), nfs)))
	must(t, mm.put("mem", makeNestFS(t.Context(), mfs)))
	must(t, mm.put("angry", makeNestFS(t.Context(), afs)))
	must(t, mm.put("mounts/level2/a/null", makeNestFS(t.Context(), nfs)))
	must(t, mm.put("mounts/level2/a/mem", makeNestFS(t.Context(), mfs)))
	must(t, mm.put("mounts/level2/angry", makeNestFS(t.Context(), afs)))
	if err := mm.put("mounts/level2/angry/angry", makeNestFS(t.Context(), afs)); err == nil {
		t.Fatal("'mounts/level2/angry/angry' should not be mountable because of 'mounts/level2/angry'")
	}
	if err := mm.put("mounts", makeNestFS(t.Context(), afs)); err == nil {
		t.Fatal("'mounts' should not be mountable because of 'mounts/null'")
	}

	testCases := []struct {
		input                   string
		wantDirectoryList       []string
		wantGetMatchesBySubPath []string
		wantMountPath           string
		wantMountSubPath        string
	}{
		{
			input:                   "",
			wantDirectoryList:       []string{"angry", "mem", "mounts", "null"},
			wantGetMatchesBySubPath: []string{"angry", "mem", "mounts/angry", "mounts/level2/a/mem", "mounts/level2/a/null", "mounts/level2/angry", "mounts/mem", "mounts/null", "null"},
			wantMountPath:           "",
			wantMountSubPath:        "",
		},
		{
			input:                   cwdPath,
			wantDirectoryList:       []string{"angry", "mem", "mounts", "null"},
			wantGetMatchesBySubPath: []string{"angry", "mem", "mounts/angry", "mounts/level2/a/mem", "mounts/level2/a/null", "mounts/level2/angry", "mounts/mem", "mounts/null", "null"},
			wantMountPath:           "",
			wantMountSubPath:        "",
		},
		{
			input:                   "mounts",
			wantDirectoryList:       []string{"angry", "level2", "mem", "null"},
			wantGetMatchesBySubPath: []string{"angry", "level2/a/mem", "level2/a/null", "level2/angry", "mem", "null"},
			wantMountPath:           "",
			wantMountSubPath:        "",
		},
		{
			input:                   "mounts/level2",
			wantDirectoryList:       []string{"a", "angry"},
			wantGetMatchesBySubPath: []string{"a/mem", "a/null", "angry"},
			wantMountPath:           "",
			wantMountSubPath:        "",
		},
		{
			input:                   "./mounts/level2",
			wantDirectoryList:       []string{"a", "angry"},
			wantGetMatchesBySubPath: []string{"a/mem", "a/null", "angry"},
			wantMountPath:           "",
			wantMountSubPath:        "",
		},
		{
			input:                   "mounts/level2/a",
			wantDirectoryList:       []string{"mem", "null"},
			wantGetMatchesBySubPath: []string{"mem", "null"},
			wantMountPath:           "",
			wantMountSubPath:        "",
		},
		{
			input:                   "mounts/level2/a/null",
			wantDirectoryList:       []string{},
			wantGetMatchesBySubPath: []string{""},
			wantMountPath:           "mounts/level2/a/null",
			wantMountSubPath:        cwdPath,
		},
		{
			input:                   "mounts/level2/a/null/more",
			wantDirectoryList:       []string{},
			wantGetMatchesBySubPath: []string{},
			wantMountPath:           "mounts/level2/a/null",
			wantMountSubPath:        "more",
		},
		{
			input:                   "mounts/level2/a/null/.",
			wantDirectoryList:       []string{},
			wantGetMatchesBySubPath: []string{""},
			wantMountPath:           "mounts/level2/a/null",
			wantMountSubPath:        cwdPath,
		},
		{
			input:                   "./mounts/level2/a/mem/./more/stuff",
			wantDirectoryList:       []string{},
			wantGetMatchesBySubPath: []string{},
			wantMountPath:           "mounts/level2/a/mem",
			wantMountSubPath:        "more/stuff",
		},
		{
			input:                   "./mounts/level2/a/null/.",
			wantDirectoryList:       []string{},
			wantGetMatchesBySubPath: []string{""},
			wantMountPath:           "mounts/level2/a/null",
			wantMountSubPath:        cwdPath,
		},
		{
			input:                   "mounts/level3",
			wantDirectoryList:       []string{},
			wantGetMatchesBySubPath: []string{},
			wantMountPath:           "",
			wantMountSubPath:        "",
		},
		{
			input:                   "mount",
			wantDirectoryList:       []string{},
			wantGetMatchesBySubPath: []string{},
			wantMountPath:           "",
			wantMountSubPath:        "",
		},
		{
			input:                   "does-not-exist",
			wantDirectoryList:       []string{},
			wantGetMatchesBySubPath: []string{},
			wantMountPath:           "",
			wantMountSubPath:        "",
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("getDirectoryList/%s", tc.input), func(t *testing.T) {
			got := mm.getDirectoryList(tc.input)
			if diff := cmp.Diff(got, tc.wantDirectoryList); diff != "" {
				t.Errorf("got: %q, want: %q, diff: %q", got, tc.wantDirectoryList, diff)
			}
		})
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("getMatchesBySubPath/%s", tc.input), func(t *testing.T) {
			matches := mm.getMatchesBySubPath(tc.input)
			got := toMapKeys(matches)
			if diff := cmp.Diff(got, tc.wantGetMatchesBySubPath); diff != "" {
				t.Errorf("got: %q, want: %q, diff: %q", got, tc.wantGetMatchesBySubPath, diff)
			}
		})
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("getClosestMount/%s", tc.input), func(t *testing.T) {
			gotMountPath, gotSubPath, gotFS, ok := mm.getClosestMount(tc.input)
			wantOk := tc.wantMountPath != ""
			if ok != wantOk {
				t.Errorf("getClosestMount(%q) ok got: %t, want: %t, fsys: %v", tc.input, ok, wantOk, gotFS)
			}
			if diff := cmp.Diff(gotMountPath, tc.wantMountPath); diff != "" {
				t.Errorf("got: %q, want: %q, diff: %q", gotSubPath, tc.wantMountSubPath, diff)
			}
			if diff := cmp.Diff(gotSubPath, tc.wantMountSubPath); diff != "" {
				t.Errorf("got: %q, want: %q, diff: %q", gotSubPath, tc.wantMountSubPath, diff)
			}
		})
	}
}

func TestNestFSFull(t *testing.T) {
	fsys, err := newNestFS(t.Context(), cwdPath)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()
	assertContains(t, fsys, "testing/testassets/files/index.html", "testing/testassets/files/index.html")
	assertContains(t, fsys, "testing/testassets/archives/nested-testassets.zip.d/site.js", "testing/testassets/files/site.js")
	assertContains(t, fsys, "testing/testassets/archives/nested-testassets.zip.d/single-testassets.zip.d/index.html", "testing/testassets/files/index.html")
	assertContains(t, fsys, "testing/testassets/archives/nested-testassets.zip.d/testassets.7z.d/assets/four/4.txt", "testing/testassets/files/assets/four/4.txt")
	assertDir(t, fsys, "testing/testassets/archives/nested-testassets.zip.d", []string{
		"assets",
		"index.html",
		"single-testassets.zip",
		"single-testassets.zip.d",
		"site.js",
		"testassets.7z",
		"testassets.7z.d",
		"weird #.txt",
		"weird #1.txt",
		"weird$.txt",
	})
	assertDir(t, fsys, "testing/testassets/archives/nested-testassets.zip.d/testassets.7z.d", []string{
		"assets",
		"index.html",
		"site.js",
		"weird #.txt",
		"weird #1.txt",
		"weird$.txt",
	})
	assertDir(t, fsys, "testing/testassets/archives", []string{
		"nested-testassets.zip",
		"nested-testassets.zip.d",
		"nodir-deep-testassets.zip",
		"nodir-deep-testassets.zip.d",
		"nodir-testassets.zip",
		"nodir-testassets.zip.d",
		"single-testassets.zip",
		"single-testassets.zip.d",
		"testassets.7z",
		"testassets.7z.d",
		"testassets.tar",
		"testassets.tar.bz2",
		"testassets.tar.bz2.d",
		"testassets.tar.d",
		"testassets.tar.gz",
		"testassets.tar.gz.d",
		"testassets.tar.lz4",
		"testassets.tar.lz4.d",
		"testassets.tar.xz",
		"testassets.tar.xz.d",
	})
	assertDir(t, fsys, "testing/testassets/archives/nodir-testassets.zip.d", []string{
		"1.txt",
		"2.txt",
		"onetwothree",
		"sixseven",
	})
	assertDir(t, fsys, "testing/testassets/archives/nodir-testassets.zip.d/onetwothree", []string{
		"1.txt",
		"2.txt",
		"3.txt",
	})
	assertDir(t, fsys, "testing/testassets/archives/nodir-testassets.zip.d/sixseven", []string{
		"6.txt",
		"7.txt",
	})
}

func TestNestedFS(t *testing.T) {
	fsys, err := New(t.Context(), "memory://?a=file:///&mounted/null=null://&mounted/angry=angry://")
	if err != nil {
		t.Fatal(err)
	}
	defer wantCloseError(t, fsys)()
	testCases := []struct {
		dir         string
		wantEntries []string
	}{
		{
			dir:         cwdPath,
			wantEntries: []string{"a", "mounted"},
		},
		{
			dir:         "mounted",
			wantEntries: []string{"angry", "null"},
		},
		{
			dir:         "mounted/null",
			wantEntries: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.dir, func(t *testing.T) {
			entries, err := fs.ReadDir(fsys, tc.dir)
			if err != nil {
				t.Fatal(err)
			}
			got := dirEntryListToNames(entries)
			if diff := cmp.Diff(got, tc.wantEntries); diff != "" {
				t.Errorf("ReadDir(.), got: %v want: %v, diff: %s", got, tc.wantEntries, diff)
			}
		})
	}
}

func TestNestFSReadDir(t *testing.T) {
	fsys, err := newNestFS(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	nfs := fsys.(*nestFS)
	if err := nfs.MkdirAll("subdir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("subdir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	entries, err := nfs.ReadDir("subdir")
	if err != nil {
		t.Fatalf("ReadDir(subdir) = %v, want nil", err)
	}
	if len(entries) != 1 {
		t.Errorf("ReadDir(subdir) returned %d entries, want 1", len(entries))
	}
	if entries[0].Name() != "file.txt" {
		t.Errorf("ReadDir entry name = %q, want %q", entries[0].Name(), "file.txt")
	}
}

func TestNestFSReadDirOnFile(t *testing.T) {
	fsys, err := newNestFS(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	nfs := fsys.(*nestFS)
	f, err := fsys.Create("regular.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = nfs.ReadDir("regular.txt")
	if err == nil {
		t.Error("ReadDir on a regular file should return an error")
	}
}

func TestGetPotentialArchives(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		input string
		want  []string
	}{
		{
			input: "",
			want:  []string{},
		},
		{
			input: cwdPath,
			want:  []string{},
		},
		{
			input: "testing/testassets/files/index.html",
			want:  []string{},
		},
		{
			input: "testing/testassets/archives/nested-testassets.zip.d/site.js",
			want:  []string{"testing/testassets/archives/nested-testassets.zip.d"},
		},
		{
			input: "testing/testassets/archives/nested-testassets.zip.d/single-testassets.zip.d/index.html",
			want:  []string{"testing/testassets/archives/nested-testassets.zip.d", "testing/testassets/archives/nested-testassets.zip.d/single-testassets.zip.d"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			got := getPotentialArchives(tc.input)
			if diff := cmp.Diff(got, tc.want); diff != "" {
				t.Errorf("got: %q, want: %q, diff: %q", got, tc.want, diff)
			}
		})
	}
}

func TestNestFSStat(t *testing.T) {
	fsys, err := newNestFS(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	nfs := fsys.(*nestFS)
	wf, err := fsys.Create("statme.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(wf, "hello"); err != nil {
		t.Fatalf("failed to write to statme.txt: %v", err)
	}
	if err := wf.Close(); err != nil {
		t.Fatalf("failed to close statme.txt: %v", err)
	}

	info, err := nfs.Stat("statme.txt")
	if err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}
	if info.Name() != "statme.txt" {
		t.Errorf("Stat().Name() = %q, want %q", info.Name(), "statme.txt")
	}
	if info.IsDir() {
		t.Error("Stat().IsDir() = true, want false")
	}
	if info.Size() != 5 {
		t.Errorf("Stat().Size() = %d, want 5", info.Size())
	}
}

func TestNestFS(t *testing.T) {
	testFileSystem(t, newNestFS, "memory://")
}

func TestNestReadDirFileRead(t *testing.T) {
	fsys, err := newNestFS(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	nfs := fsys.(*nestFS)
	if err := nfs.MkdirAll("readdir-test", fs.ModePerm); err != nil {
		t.Fatal(err)
	}

	f, err := nfs.Open("readdir-test")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, f)()

	buf := make([]byte, 16)
	n, err := f.Read(buf)
	if err != nil {
		t.Logf("Read() on directory = %v (either an error or n==0 is acceptable)", err)
	}
	if n != 0 {
		t.Errorf("Read() on directory returned %d bytes, want 0", n)
	}
}

func TestNestFSCloseAngryFS(t *testing.T) {
	nfs := makeNestFS(t.Context(), makeAngryFS(angryFSPrefix))
	err := nfs.Close()
	if err == nil {
		t.Fatal("Close() of nestFS wrapping angryFS = nil, want error")
	}
}

func TestNestFSCloseMountError(t *testing.T) {
	// A nestFS whose mount itself fails to close should propagate the error.
	outer := makeNestFS(t.Context(), makeNullFS("null://"))
	angryMount := makeNestFS(t.Context(), makeAngryFS(angryFSPrefix))
	if err := outer.addMount("angry", angryMount); err != nil {
		t.Fatal(err)
	}
	err := outer.Close()
	if err == nil {
		t.Fatal("Close() of nestFS with angry mount = nil, want error")
	}
}

func TestMountMapCloseError(t *testing.T) {
	mm := makeMountMap("test")
	angryNFS := makeNestFS(t.Context(), makeAngryFS(angryFSPrefix))
	if err := mm.put("angry", angryNFS); err != nil {
		t.Fatal(err)
	}
	err := mm.Close()
	if err == nil {
		t.Fatal("mountMap.Close() with angry mount = nil, want error")
	}
}

func TestMountMapConcurrentAccess(t *testing.T) {
	mm := makeMountMap("test")
	mfs := makeMemFS("memory:///")
	nfs := makeNestFS(t.Context(), mfs)

	const workers = 20
	var wg sync.WaitGroup
	wg.Add(workers * 2)
	for i := range workers {
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("mount%d", i)
			if err := mm.put(name, nfs); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			mm.getMount("mount0")
			mm.getDirectoryList("")
			mm.getMatchesBySubPath("")
			mm.getClosestMount("mount0")
		}()
	}
	wg.Wait()
}

func TestNestFSRemove(t *testing.T) {
	fsys, err := newNestFS(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	f, err := fsys.Create("remove_me.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	t.Run("file_exists", func(t *testing.T) {
		if err := fsys.Remove("remove_me.txt"); err != nil {
			t.Fatalf("Remove() = %v, want nil", err)
		}
		if _, err := fsys.Stat("remove_me.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("after Remove, Stat = %v, want ErrNotExist", err)
		}
	})

	t.Run("not_exist", func(t *testing.T) {
		if err := fsys.Remove("ghost.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Remove(nonexistent) = %v, want ErrNotExist", err)
		}
	})
}

func TestNestFSRemoveAll(t *testing.T) {
	fsys, err := newNestFS(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	nfs := fsys.(*nestFS)
	if err := nfs.MkdirAll("sub/dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	child, err := nfs.Create("sub/dir/leaf.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Close(); err != nil {
		t.Errorf("failed to close child.txt: %v", err)
	}
	keep, err := nfs.Create("keep.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := keep.Close(); err != nil {
		t.Errorf("failed to close keep.txt: %v", err)
	}

	t.Run("subtree", func(t *testing.T) {
		if err := fsys.RemoveAll("sub"); err != nil {
			t.Fatalf("RemoveAll('sub') = %v, want nil", err)
		}
		if _, err := fsys.Stat("sub"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("after RemoveAll('sub'), Stat = %v, want ErrNotExist", err)
		}
		if _, err := fsys.Stat("keep.txt"); err != nil {
			t.Errorf("RemoveAll('sub') unexpectedly removed keep.txt: %v", err)
		}
	})

	t.Run("not_exist_is_noop", func(t *testing.T) {
		if err := fsys.RemoveAll("ghost"); err != nil {
			t.Errorf("RemoveAll(nonexistent) = %v, want nil", err)
		}
	})
}

func TestNestFSGlobFallback(t *testing.T) {
	// archiveFS does not implement fs.GlobFS, triggering the globFS fallback in nestFS.
	afs := mustArchiveFS(t)
	nfs := makeNestFS(t.Context(), afs)
	defer validateClose(t, nfs)()

	matches, err := nfs.Glob("*.html")
	if err != nil {
		t.Fatalf("Glob() = %v, want nil", err)
	}
	if len(matches) == 0 {
		t.Error("Glob(*.html) = 0 matches, want at least one")
	}
}

func TestNestFSOperations(t *testing.T) {
	fsys, err := newNestFS(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	nfs := fsys.(*nestFS)
	if err := nfs.MkdirAll("sub/dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	f, err := nfs.Create("sub/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := io.WriteString(f, "hello"); err != nil {
		t.Fatal(err)
	} else if n != len("hello") {
		t.Fatalf("WriteString() = %d, want %d", n, len("hello"))
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	t.Run("ReadFile", func(t *testing.T) {
		data, err := nfs.ReadFile("sub/file.txt")
		if err != nil {
			t.Fatalf("ReadFile() = %v, want nil", err)
		}
		if string(data) != "hello" {
			t.Errorf("ReadFile() = %q, want %q", data, "hello")
		}
	})

	t.Run("ReadLink", func(t *testing.T) {
		// memFS has no symlinks, so ReadLink should return ErrInvalid
		_, err := nfs.ReadLink("sub/file.txt")
		if err == nil {
			t.Fatal("ReadLink() = nil error, want error (no symlinks)")
		}
	})

	t.Run("Lstat", func(t *testing.T) {
		info, err := nfs.Lstat("sub/file.txt")
		if err != nil {
			t.Fatalf("Lstat() = %v, want nil", err)
		}
		if info.Name() != "file.txt" {
			t.Errorf("Lstat().Name() = %q, want %q", info.Name(), "file.txt")
		}
	})

	t.Run("ReadDir", func(t *testing.T) {
		entries, err := nfs.ReadDir("sub")
		if err != nil {
			t.Fatalf("ReadDir() = %v, want nil", err)
		}
		if len(entries) != 2 { // dir + file.txt
			t.Errorf("ReadDir() = %d entries, want 2", len(entries))
		}
	})

	t.Run("Create_and_MkdirAll_in_subpath", func(t *testing.T) {
		if err := nfs.MkdirAll("new/path", fs.ModePerm); err != nil {
			t.Errorf("MkdirAll() = %v, want nil", err)
		}
		f, err := nfs.Create("new/path/file.txt")
		if err != nil {
			t.Fatalf("Create() = %v, want nil", err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestNestFSValidPathClosed(t *testing.T) {
	fsys, err := newNestFS(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}

	nfs := fsys.(*nestFS)
	if _, err := nfs.ReadDir(cwdPath); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("ReadDir on closed nestFS = %v, want fs.ErrClosed", err)
	}
	if _, err := nfs.Stat("foo.txt"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Stat on closed nestFS = %v, want fs.ErrClosed", err)
	}
	if _, err := nfs.Open("foo.txt"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Open on closed nestFS = %v, want fs.ErrClosed", err)
	}
	if _, err := nfs.Create("foo.txt"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Create on closed nestFS = %v, want fs.ErrClosed", err)
	}
	if err := nfs.MkdirAll("foo", fs.ModePerm); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("MkdirAll on closed nestFS = %v, want fs.ErrClosed", err)
	}
	if _, err := nfs.ReadFile("foo.txt"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("ReadFile on closed nestFS = %v, want fs.ErrClosed", err)
	}
	if _, err := nfs.ReadLink("foo.txt"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("ReadLink on closed nestFS = %v, want fs.ErrClosed", err)
	}
	if _, err := nfs.Lstat("foo.txt"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Lstat on closed nestFS = %v, want fs.ErrClosed", err)
	}
}

func TestNestFSStaleArchiveMountPruned(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := createZipFromDir(t, testAssetsFilesDir)

	destZip := tmpDir + "/testassets.zip"
	data, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destZip, data, 0o644); err != nil {
		t.Fatal(err)
	}

	fsys, err := newNestFS(t.Context(), "file://"+tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	entries, err := fs.ReadDir(fsys, cwdPath)
	if err != nil {
		t.Fatal(err)
	}
	got := dirEntryListToNames(entries)
	if diff := cmp.Diff(got, []string{"testassets.zip", "testassets.zip.d"}); diff != "" {
		t.Fatalf("before remove: %s", diff)
	}

	// Read a file inside the archive to ensure the mount is live.
	if _, err := fs.ReadFile(fsys, "testassets.zip.d/index.html"); err != nil {
		t.Fatalf("read through archive mount: %v", err)
	}

	// Remove the archive file. On Windows this mount now holds the archive
	// file open for its entire lifetime (see newArchiveFSFromLocalFS), so the
	// OS refuses to delete a file that's actively mounted; the caller must
	// close the mount first. Unix allows unlinking a file with open handles,
	// so the rest of this test (stale-mount pruning) only applies there.
	if err := os.Remove(destZip); err != nil {
		if runtime.GOOS == "windows" {
			return
		}
		t.Fatal(err)
	}

	// The next directory listing should no longer show the .d entry.
	entries, err = fs.ReadDir(fsys, cwdPath)
	if err != nil {
		t.Fatal(err)
	}
	got = dirEntryListToNames(entries)
	if diff := cmp.Diff(got, []string{}); diff != "" {
		t.Errorf("after remove: still shows stale mount: %s", diff)
	}
}

// ---------------------------------------------------------------------------
// Polyfill test stubs
//
// Each stub implements a different subset of the File interface so tests can
// verify that wrapReadOnlyFSFile and wrapFSFile delegate or fill in exactly
// the right methods.
// ---------------------------------------------------------------------------

// testBareFile implements only fs.File (Stat, Read, Close).
// No Seek, ReadAt, Write, or WriteString.
type testBareFile struct {
	r    *bytes.Reader
	name string
}

func newTestBareFile(name, content string) *testBareFile {
	return &testBareFile{r: bytes.NewReader([]byte(content)), name: name}
}

func (f *testBareFile) Stat() (fs.FileInfo, error) {
	return &fsInfo{name: f.name, size: f.r.Size()}, nil
}
func (f *testBareFile) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *testBareFile) Close() error               { return nil }

// testWriterFile adds io.Writer to testBareFile. No Seek, ReadAt, or WriteString.
type testWriterFile struct {
	*testBareFile
	written []byte
}

func newTestWriterFile(name, content string) *testWriterFile {
	return &testWriterFile{testBareFile: newTestBareFile(name, content)}
}

func (f *testWriterFile) Write(p []byte) (int, error) {
	f.written = append(f.written, p...)
	return len(p), nil
}

// testStringWriterFile adds io.StringWriter to testWriterFile.
type testStringWriterFile struct {
	*testWriterFile
	stringWriterCalled bool
}

func newTestStringWriterFile(name, content string) *testStringWriterFile {
	return &testStringWriterFile{testWriterFile: newTestWriterFile(name, content)}
}

func (f *testStringWriterFile) WriteString(s string) (int, error) {
	f.stringWriterCalled = true
	return f.Write([]byte(s))
}

// testSeekerFile implements fs.File + io.Seeker + io.ReaderAt (all via bytes.Reader).
// No Write or WriteString.
type testSeekerFile struct {
	r    *bytes.Reader
	name string
}

func newTestSeekerFile(name, content string) *testSeekerFile {
	return &testSeekerFile{r: bytes.NewReader([]byte(content)), name: name}
}

func (f *testSeekerFile) Stat() (fs.FileInfo, error) {
	return &fsInfo{name: f.name, size: f.r.Size()}, nil
}
func (f *testSeekerFile) Read(p []byte) (int, error)                { return f.r.Read(p) }
func (f *testSeekerFile) Close() error                              { return nil }
func (f *testSeekerFile) Seek(off int64, whence int) (int64, error) { return f.r.Seek(off, whence) }
func (f *testSeekerFile) ReadAt(p []byte, off int64) (int, error)   { return f.r.ReadAt(p, off) }

// ---------------------------------------------------------------------------
// Tests for wrapReadOnlyFSFile
// ---------------------------------------------------------------------------

func TestWrapReadOnlyFSFile(t *testing.T) {
	t.Run("fast_path_when_already_satisfies_File", func(t *testing.T) {
		base := newNullFile("test.txt")
		got, err := wrapReadOnlyFSFile(base)
		if err != nil {
			t.Fatal(err)
		}
		if got != File(base) {
			t.Error("expected same value; file already satisfies File so no wrapper should be created")
		}
	})

	t.Run("read_through_buffer", func(t *testing.T) {
		const content = "hello world"
		wrapped, err := wrapReadOnlyFSFile(newTestBareFile("t.txt", content))
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		got, err := io.ReadAll(wrapped)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != content {
			t.Errorf("Read = %q, want %q", got, content)
		}
	})

	t.Run("seek_to_start_after_partial_read", func(t *testing.T) {
		const content = "hello world"
		wrapped, err := wrapReadOnlyFSFile(newTestBareFile("t.txt", content))
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		// Consume first 5 bytes.
		if _, err := io.ReadFull(wrapped, make([]byte, 5)); err != nil {
			t.Fatal(err)
		}
		pos, err := wrapped.Seek(0, io.SeekStart)
		if err != nil {
			t.Fatalf("Seek(0, SeekStart) = %v", err)
		}
		if pos != 0 {
			t.Errorf("Seek returned %d, want 0", pos)
		}
		got, err := io.ReadAll(wrapped)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != content {
			t.Errorf("Read after Seek = %q, want %q", got, content)
		}
	})

	t.Run("seek_current_and_end", func(t *testing.T) {
		const content = "abcdefghij" // 10 bytes
		wrapped, err := wrapReadOnlyFSFile(newTestBareFile("t.txt", content))
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		if _, err := wrapped.Seek(3, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		pos, err := wrapped.Seek(2, io.SeekCurrent)
		if err != nil {
			t.Fatalf("Seek(2, SeekCurrent) = %v", err)
		}
		if pos != 5 {
			t.Errorf("Seek(+2 from 3) = %d, want 5", pos)
		}
		pos, err = wrapped.Seek(-3, io.SeekEnd)
		if err != nil {
			t.Fatalf("Seek(-3, SeekEnd) = %v", err)
		}
		if pos != 7 {
			t.Errorf("Seek(-3 from end) = %d, want 7", pos)
		}
		buf := make([]byte, 3)
		if _, err := io.ReadFull(wrapped, buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != "hij" {
			t.Errorf("Read after Seek(-3, SeekEnd) = %q, want %q", buf, "hij")
		}
	})

	t.Run("readat_does_not_affect_read_position", func(t *testing.T) {
		const content = "hello world"
		wrapped, err := wrapReadOnlyFSFile(newTestBareFile("t.txt", content))
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		buf := make([]byte, 5)
		n, err := wrapped.ReadAt(buf, 6)
		if err != nil || n != 5 || string(buf) != "world" {
			t.Fatalf("ReadAt(5, 6) = %q %v; want %q nil", buf[:n], err, "world")
		}
		// Read position is still at 0.
		first5 := make([]byte, 5)
		if _, err := io.ReadFull(wrapped, first5); err != nil {
			t.Fatal(err)
		}
		if string(first5) != "hello" {
			t.Errorf("Read after ReadAt = %q, want %q", first5, "hello")
		}
	})

	t.Run("write_always_errors", func(t *testing.T) {
		wrapped, err := wrapReadOnlyFSFile(newTestBareFile("t.txt", "content"))
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		if _, err := wrapped.Write([]byte("x")); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Write() = %v, want fs.ErrInvalid", err)
		}
		if _, err := wrapped.WriteString("x"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("WriteString() = %v, want fs.ErrInvalid", err)
		}
	})

	t.Run("write_always_errors_even_when_underlying_supports_write", func(t *testing.T) {
		f := newTestWriterFile("t.txt", "content")
		wrapped, err := wrapReadOnlyFSFile(f)
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		if _, err := wrapped.Write([]byte("x")); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Write() = %v, want fs.ErrInvalid", err)
		}
		if _, err := wrapped.WriteString("x"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("WriteString() = %v, want fs.ErrInvalid", err)
		}
		if len(f.written) > 0 {
			t.Errorf("underlying writer received %d bytes, want 0", len(f.written))
		}
	})

	t.Run("stat_delegates_to_underlying", func(t *testing.T) {
		wrapped, err := wrapReadOnlyFSFile(newTestBareFile("myfile.txt", "hello"))
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		info, err := wrapped.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if info.Name() != "myfile.txt" {
			t.Errorf("Stat().Name() = %q, want %q", info.Name(), "myfile.txt")
		}
	})

	t.Run("close_releases_buffer", func(t *testing.T) {
		got, err := wrapReadOnlyFSFile(newTestBareFile("t.txt", "content"))
		if err != nil {
			t.Fatal(err)
		}
		nf := got.(*nestFile)
		if nf.buf == nil {
			t.Fatal("expected non-nil buffer before close")
		}
		if err := got.Close(); err != nil {
			t.Fatal(err)
		}
		if nf.buf != nil {
			t.Error("expected nil buffer after close")
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for wrapFSFile
// ---------------------------------------------------------------------------

func TestWrapFSFile(t *testing.T) {
	t.Run("fast_path_when_already_satisfies_File", func(t *testing.T) {
		base := newNullFile("test.txt")
		got, err := wrapFSFile(base)
		if err != nil {
			t.Fatal(err)
		}
		if got != File(base) {
			t.Error("expected same value; file already satisfies File so no wrapper should be created")
		}
	})

	t.Run("no_buffer_when_seek_and_readat_present", func(t *testing.T) {
		f := newTestSeekerFile("t.txt", "hello world")
		wrapped, err := wrapFSFile(f)
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		nf := wrapped.(*nestFile)
		if nf.buf != nil {
			t.Error("expected nil buffer when underlying provides both Seek and ReadAt")
		}
		// Verify native Seek and ReadAt still work through delegation.
		if _, err := wrapped.Seek(6, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 5)
		if _, err := io.ReadFull(wrapped, buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != "world" {
			t.Errorf("Read after Seek(6) = %q, want %q", buf, "world")
		}
	})

	t.Run("write_errors_without_underlying_writer", func(t *testing.T) {
		wrapped, err := wrapFSFile(newTestBareFile("t.txt", "content"))
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		if _, err := wrapped.Write([]byte("x")); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Write() = %v, want fs.ErrInvalid", err)
		}
		if _, err := wrapped.WriteString("x"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("WriteString() = %v, want fs.ErrInvalid", err)
		}
	})

	t.Run("write_delegates_to_underlying_writer", func(t *testing.T) {
		f := newTestWriterFile("t.txt", "")
		wrapped, err := wrapFSFile(f)
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		if _, err := wrapped.Write([]byte("hello")); err != nil {
			t.Errorf("Write() = %v, want nil", err)
		}
		if string(f.written) != "hello" {
			t.Errorf("underlying received %q, want %q", f.written, "hello")
		}
	})

	t.Run("writestring_derived_from_write_when_no_stringwriter", func(t *testing.T) {
		f := newTestWriterFile("t.txt", "")
		wrapped, err := wrapFSFile(f)
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		if _, err := wrapped.WriteString("world"); err != nil {
			t.Errorf("WriteString() = %v, want nil", err)
		}
		if string(f.written) != "world" {
			t.Errorf("underlying received %q, want %q", f.written, "world")
		}
	})

	t.Run("writestring_delegates_to_underlying_stringwriter", func(t *testing.T) {
		f := newTestStringWriterFile("t.txt", "")
		wrapped, err := wrapFSFile(f)
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		if _, err := wrapped.WriteString("world"); err != nil {
			t.Errorf("WriteString() = %v, want nil", err)
		}
		if !f.stringWriterCalled {
			t.Error("expected underlying WriteString to be called directly, not derived from Write")
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for wrapFile (unified wrapper)
// ---------------------------------------------------------------------------

func TestWrapFile(t *testing.T) {
	t.Run("fast_path_when_already_satisfies_File", func(t *testing.T) {
		base := newNullFile("test.txt")
		for _, readOnly := range []bool{true, false} {
			got, err := wrapFile(base, readOnly, bufferMemory)
			if err != nil {
				t.Fatal(err)
			}
			if got != File(base) {
				t.Errorf("readOnly=%t: expected same value; file already satisfies File", readOnly)
			}
		}
	})

	t.Run("readonly_write_errors", func(t *testing.T) {
		wrapped, err := wrapFile(newTestBareFile("t.txt", "content"), true, bufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		if _, err := wrapped.Write([]byte("x")); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Write() = %v, want fs.ErrInvalid", err)
		}
		if _, err := wrapped.WriteString("x"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("WriteString() = %v, want fs.ErrInvalid", err)
		}
	})

	t.Run("readwrite_delegates_write", func(t *testing.T) {
		f := newTestWriterFile("t.txt", "")
		wrapped, err := wrapFile(f, false, bufferMemory)
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		if _, err := wrapped.Write([]byte("hello")); err != nil {
			t.Errorf("Write() = %v, want nil", err)
		}
		if string(f.written) != "hello" {
			t.Errorf("underlying received %q, want %q", f.written, "hello")
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for the buffer polyfill behavior (Seek / ReadAt / Read consistency)
// ---------------------------------------------------------------------------

func testPolyfillBuffering(t *testing.T, mode bufferMode) {
	t.Helper()

	t.Run("multiple_seeks_are_consistent", func(t *testing.T) {
		const content = "abcdefghij"
		wrapped, err := wrapFile(newTestBareFile("t.txt", content), false, mode)
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		for _, tc := range []struct {
			offset int64
			want   string
		}{
			{0, "abcde"},
			{5, "fghij"},
			{3, "defgh"},
			{0, "abcde"},
		} {
			if _, err := wrapped.Seek(tc.offset, io.SeekStart); err != nil {
				t.Fatalf("Seek(%d) = %v", tc.offset, err)
			}
			buf := make([]byte, 5)
			if _, err := io.ReadFull(wrapped, buf); err != nil {
				t.Fatalf("ReadFull after Seek(%d) = %v", tc.offset, err)
			}
			if string(buf) != tc.want {
				t.Errorf("Seek(%d) then Read = %q, want %q", tc.offset, buf, tc.want)
			}
		}
	})

	t.Run("readat_at_various_offsets", func(t *testing.T) {
		const content = "abcdefghij"
		wrapped, err := wrapFile(newTestBareFile("t.txt", content), false, mode)
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		for _, tc := range []struct {
			off  int64
			n    int
			want string
		}{
			{0, 3, "abc"},
			{7, 3, "hij"},
			{4, 4, "efgh"},
		} {
			buf := make([]byte, tc.n)
			n, err := wrapped.ReadAt(buf, tc.off)
			if err != nil || n != tc.n || string(buf) != tc.want {
				t.Errorf("ReadAt(%d, %d) = %q %v; want %q nil", tc.n, tc.off, buf[:n], err, tc.want)
			}
		}
	})

	t.Run("readat_does_not_affect_read_position", func(t *testing.T) {
		const content = "hello world"
		wrapped, err := wrapFile(newTestBareFile("t.txt", content), true, mode)
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		buf := make([]byte, 5)
		n, err := wrapped.ReadAt(buf, 6)
		if err != nil || n != 5 || string(buf) != "world" {
			t.Fatalf("ReadAt(5, 6) = %q %v; want %q nil", buf[:n], err, "world")
		}
		first5 := make([]byte, 5)
		if _, err := io.ReadFull(wrapped, first5); err != nil {
			t.Fatal(err)
		}
		if string(first5) != "hello" {
			t.Errorf("Read after ReadAt = %q, want %q", first5, "hello")
		}
	})

	t.Run("seek_current_and_end", func(t *testing.T) {
		const content = "abcdefghij" // 10 bytes
		wrapped, err := wrapFile(newTestBareFile("t.txt", content), true, mode)
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		if _, err := wrapped.Seek(3, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		pos, err := wrapped.Seek(2, io.SeekCurrent)
		if err != nil {
			t.Fatalf("Seek(2, SeekCurrent) = %v", err)
		}
		if pos != 5 {
			t.Errorf("Seek(+2 from 3) = %d, want 5", pos)
		}
		pos, err = wrapped.Seek(-3, io.SeekEnd)
		if err != nil {
			t.Fatalf("Seek(-3, SeekEnd) = %v", err)
		}
		if pos != 7 {
			t.Errorf("Seek(-3 from end) = %d, want 7", pos)
		}
		buf := make([]byte, 3)
		if _, err := io.ReadFull(wrapped, buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != "hij" {
			t.Errorf("Read after Seek(-3, SeekEnd) = %q, want %q", buf, "hij")
		}
	})
}

func TestNestFilePolyfillBuffering(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		t.Run("buffer_created_when_seek_missing", func(t *testing.T) {
			wrapped, err := wrapFile(newTestBareFile("t.txt", "hello"), false, bufferMemory)
			if err != nil {
				t.Fatal(err)
			}
			defer validateClose(t, wrapped)()

			nf := wrapped.(*nestFile)
			if nf.buf == nil {
				t.Error("expected non-nil buf when underlying lacks Seek")
			}
			if nf.tmpFile != nil {
				t.Error("expected nil tmpFile in memory mode")
			}
		})

		testPolyfillBuffering(t, bufferMemory)
	})

	t.Run("disk", func(t *testing.T) {
		t.Run("tmpfile_created_when_seek_missing", func(t *testing.T) {
			wrapped, err := wrapFile(newTestBareFile("t.txt", "hello"), false, bufferDisk)
			if err != nil {
				t.Fatal(err)
			}
			defer validateClose(t, wrapped)()

			nf := wrapped.(*nestFile)
			if nf.tmpFile == nil {
				t.Error("expected non-nil tmpFile when underlying lacks Seek")
			}
			if nf.buf != nil {
				t.Error("expected nil buf in disk mode")
			}
		})

		t.Run("tmpfile_cleaned_up_on_close", func(t *testing.T) {
			wrapped, err := wrapFile(newTestBareFile("t.txt", "hello"), false, bufferDisk)
			if err != nil {
				t.Fatal(err)
			}
			nf := wrapped.(*nestFile)
			tmpName := nf.tmpFile.Name()
			if err := wrapped.Close(); err != nil {
				t.Fatal(err)
			}
			if nf.tmpFile != nil {
				t.Error("expected nil tmpFile after close")
			}
			if _, err := os.Stat(tmpName); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("temp file %q still exists after close", tmpName)
			}
		})

		testPolyfillBuffering(t, bufferDisk)
	})

	t.Run("no_buffer_when_seek_and_readat_present", func(t *testing.T) {
		f := newTestSeekerFile("t.txt", "hello world")
		wrapped, err := wrapFile(f, false, bufferDisk)
		if err != nil {
			t.Fatal(err)
		}
		defer validateClose(t, wrapped)()

		nf := wrapped.(*nestFile)
		if nf.buf != nil {
			t.Error("expected nil buf when underlying provides both Seek and ReadAt")
		}
		if nf.tmpFile != nil {
			t.Error("expected nil tmpFile when underlying provides both Seek and ReadAt")
		}
	})
}
