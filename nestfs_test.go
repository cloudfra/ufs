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
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

func TestNewNestFSInvalid(t *testing.T) {
	_, err := newNestFS(t.Context(), "invalid://scheme")
	if err == nil {
		t.Fatal("newNestFS with invalid scheme should return an error")
	}
}

func TestMountMap(t *testing.T) {
	mm := makeMountMap("test")
	defer ufsTesting.WantCloseError(t, mm)()
	mfs := makeMemFS("memory:///")
	afs := makeAngryFS(angryFSPrefix)
	nfs := mustNullFS(t)

	ufsTesting.Must(t, mm.put("mounts/null", makeNestFS(t.Context(), nfs)))
	ufsTesting.Must(t, mm.put("mounts/mem", makeNestFS(t.Context(), mfs)))
	ufsTesting.Must(t, mm.put("mounts/angry", makeNestFS(t.Context(), afs)))
	ufsTesting.Must(t, mm.put("null", makeNestFS(t.Context(), nfs)))
	ufsTesting.Must(t, mm.put("mem", makeNestFS(t.Context(), mfs)))
	ufsTesting.Must(t, mm.put("angry", makeNestFS(t.Context(), afs)))
	ufsTesting.Must(t, mm.put("mounts/level2/a/null", makeNestFS(t.Context(), nfs)))
	ufsTesting.Must(t, mm.put("mounts/level2/a/mem", makeNestFS(t.Context(), mfs)))
	ufsTesting.Must(t, mm.put("mounts/level2/angry", makeNestFS(t.Context(), afs)))
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
			input:                   pathutil.CwdPath,
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
			wantMountSubPath:        pathutil.CwdPath,
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
			wantMountSubPath:        pathutil.CwdPath,
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
			wantMountSubPath:        pathutil.CwdPath,
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
			got := ufsTesting.ToMapKeys(matches)
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
	fsys, err := newNestFS(t.Context(), pathutil.CwdPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()
	ufsTesting.AssertContains(t, fsys, "testing/testassets/files/index.html", "testing/testassets/files/index.html")
	ufsTesting.AssertContains(t, fsys, "testing/testassets/archives/nested-testassets.zip.d/site.js", "testing/testassets/files/site.js")
	ufsTesting.AssertContains(t, fsys, "testing/testassets/archives/nested-testassets.zip.d/single-testassets.zip.d/index.html", "testing/testassets/files/index.html")
	ufsTesting.AssertContains(t, fsys, "testing/testassets/archives/nested-testassets.zip.d/testassets.7z.d/assets/four/4.txt", "testing/testassets/files/assets/four/4.txt")
	ufsTesting.AssertDir(t, fsys, "testing/testassets/archives/nested-testassets.zip.d", []string{
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
	ufsTesting.AssertDir(t, fsys, "testing/testassets/archives/nested-testassets.zip.d/testassets.7z.d", []string{
		"assets",
		"index.html",
		"site.js",
		"weird #.txt",
		"weird #1.txt",
		"weird$.txt",
	})
	ufsTesting.AssertDir(t, fsys, "testing/testassets/archives", []string{
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
	ufsTesting.AssertDir(t, fsys, "testing/testassets/archives/nodir-testassets.zip.d", []string{
		"1.txt",
		"2.txt",
		"onetwothree",
		"sixseven",
	})
	ufsTesting.AssertDir(t, fsys, "testing/testassets/archives/nodir-testassets.zip.d/onetwothree", []string{
		"1.txt",
		"2.txt",
		"3.txt",
	})
	ufsTesting.AssertDir(t, fsys, "testing/testassets/archives/nodir-testassets.zip.d/sixseven", []string{
		"6.txt",
		"7.txt",
	})
}

func TestNestedFS(t *testing.T) {
	fsys, err := New(t.Context(), "memory://?a=file:///&mounted/null=null://&mounted/angry=angry://")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.WantCloseError(t, fsys)()
	testCases := []struct {
		dir         string
		wantEntries []string
	}{
		{
			dir:         pathutil.CwdPath,
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
			got := ufsTesting.DirEntryListToNames(entries)
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
	defer ufsTesting.ValidateClose(t, fsys)()

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
	defer ufsTesting.ValidateClose(t, fsys)()

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
			input: pathutil.CwdPath,
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
	defer ufsTesting.ValidateClose(t, fsys)()

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

func TestNestReadDirFileRead(t *testing.T) {
	fsys, err := newNestFS(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	nfs := fsys.(*nestFS)
	if err := nfs.MkdirAll("readdir-test", fs.ModePerm); err != nil {
		t.Fatal(err)
	}

	f, err := nfs.Open("readdir-test")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, f)()

	buf := make([]byte, 16)
	n, err := f.Read(buf)
	if err == nil {
		t.Errorf("Read() expected to return an error, got nil")
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
	defer ufsTesting.ValidateClose(t, fsys)()

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
	defer ufsTesting.ValidateClose(t, fsys)()

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
	// archiveFS does not implement fs.GlobFS, triggering the globutil fallback in nestFS.
	afs := mustArchiveFS(t)
	nfs := makeNestFS(t.Context(), afs)
	defer ufsTesting.ValidateClose(t, nfs)()

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
	defer ufsTesting.ValidateClose(t, fsys)()

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
}

func TestNestFSValidPathClosed(t *testing.T) {
	fsys, err := newNestFS(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}

	// ReadDir, Open, Create, and MkdirAll on a closed FS are covered by the
	// shared Close conformance test in drivers/testing.
	nfs := fsys.(*nestFS)
	if _, err := nfs.Stat("foo.txt"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Stat on closed nestFS = %v, want fs.ErrClosed", err)
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
	data, err := osutil.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := osutil.WriteFile(destZip, data); err != nil {
		t.Fatal(err)
	}

	fsys, err := newNestFS(t.Context(), "file://"+tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	entries, err := fs.ReadDir(fsys, pathutil.CwdPath)
	if err != nil {
		t.Fatal(err)
	}
	got := ufsTesting.DirEntryListToNames(entries)
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
	if err := osutil.Remove(destZip); err != nil {
		if runtime.GOOS == "windows" {
			return
		}
		t.Fatal(err)
	}

	// The next directory listing should no longer show the .d entry.
	entries, err = fs.ReadDir(fsys, pathutil.CwdPath)
	if err != nil {
		t.Fatal(err)
	}
	got = ufsTesting.DirEntryListToNames(entries)
	if diff := cmp.Diff(got, []string{}); diff != "" {
		t.Errorf("after remove: still shows stale mount: %s", diff)
	}
}

// TestNestFSMountParentDirectory covers a directory that exists only because
// a file system is mounted below it: the base has no "a", but "a/b" is a
// mount. ReadDir already listed "a"; Stat, Lstat and Open must agree.
func TestNestFSMountParentDirectory(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory:?a%2Fb=memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	info, err := fsys.Stat("a")
	if err != nil {
		t.Fatalf("Stat(a) = %v, want nil", err)
	}
	if !info.IsDir() || info.Name() != "a" {
		t.Errorf("Stat(a) = {name: %q, dir: %t}, want {name: \"a\", dir: true}", info.Name(), info.IsDir())
	}
	if info, err := fsys.Lstat("a"); err != nil || !info.IsDir() {
		t.Errorf("Lstat(a) = %v, %v, want a directory", info, err)
	}

	f, err := fsys.Open("a")
	if err != nil {
		t.Fatalf("Open(a) = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(t, f)()
	dir, ok := f.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("Open(a) returned %T, want an fs.ReadDirFile", f)
	}
	entries, err := dir.ReadDir(-1)
	if err != nil {
		t.Fatalf("ReadDir() = %v, want nil", err)
	}
	if len(entries) != 1 || entries[0].Name() != "b" {
		t.Errorf("ReadDir() = %v, want [b]", entries)
	}
}

func TestNestFSMissingDirectory(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory:?a%2Fb=memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	for _, name := range []string{"missing", "a/missing", "a/b/missing"} {
		if _, err := fsys.ReadDir(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadDir(%q) = %v, want %v", name, err, fs.ErrNotExist)
		}
		if _, err := fsys.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) = %v, want %v", name, err, fs.ErrNotExist)
		}
	}
}

// TestIsMountedArchiveDir exercises all early-exit conditions of the method.
func TestIsMountedArchiveDir(t *testing.T) {
	dir := t.TempDir()

	// Create data.zip (virtual .d should be detected).
	zf, err := osutil.Create(filepath.Join(dir, "data.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}
	// Create conf.d as a real directory (base name "conf" is not an archive).
	if err := osutil.MkdirAll(filepath.Join(dir, "conf.d")); err != nil {
		t.Fatal(err)
	}

	lfs, err := newLocalFS(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	nfs := makeNestFS(t.Context(), lfs)
	t.Cleanup(func() {
		if err := nfs.Close(); err != nil {
			t.Errorf("nfs.Close() = %v", err)
		}
	})

	cases := []struct {
		name string
		want bool
	}{
		{"readme.txt", false},  // does not end with .d
		{"conf.d", false},      // ends with .d but "conf" is not a mountable archive name
		{"ghost.zip.d", false}, // archive name but ghost.zip does not exist (ErrNotExist)
		{"data.zip.d", true},   // archive exists and is not confirmed absent
	}
	for _, tc := range cases {
		if got := nfs.IsMountedArchiveDir(tc.name); got != tc.want {
			t.Errorf("IsMountedArchiveDir(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// writeTestZip writes a zip archive holding the single file hello.txt to name
// in fsys.
func writeTestZip(t *testing.T, fsys WriteFS, name string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create(name)
	if err != nil {
		t.Fatalf("Create(%q) = %v, want nil", name, err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestNestFSArchiveInMountOfMountedFS verifies that an archive is readable
// when it lives in a mount of a file system that is itself mounted: the base
// of the inner file system is on the host, the archive is not.
func TestNestFSArchiveInMountOfMountedFS(t *testing.T) {
	inner, err := New(t.Context(), "file://"+filepath.ToSlash(t.TempDir())+"?sub=memory:")
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}
	writeTestZip(t, inner, "sub/a.zip")

	outer, err := NewFSBuilder("memory:").MountFS("m", inner).Build(t.Context())
	if err != nil {
		t.Fatalf("Build() = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(t, outer)()

	entries, err := fs.ReadDir(outer, "m/sub/a.zip.d")
	if err != nil {
		t.Fatalf("ReadDir(m/sub/a.zip.d) = %v, want nil", err)
	}
	if diff := cmp.Diff([]string{"hello.txt"}, ufsTesting.DirEntryListToNames(entries)); diff != "" {
		t.Errorf("ReadDir(m/sub/a.zip.d) mismatch (-want +got):\n%s", diff)
	}
}

// TestNestFSAbsPathOutsideHost verifies that AbsPath fails for names that a
// local base does not serve from the host: names in a mount and names inside
// an archive.
func TestNestFSAbsPathOutsideHost(t *testing.T) {
	fsys, err := New(t.Context(), "file://"+filepath.ToSlash(t.TempDir())+"?sub=memory:")
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()
	writeTestZip(t, fsys, "a.zip")

	if _, err := AbsPath(fsys, "a.zip"); err != nil {
		t.Errorf("AbsPath(a.zip) = %v, want nil", err)
	}
	for _, name := range []string{"sub/file.txt", "a.zip.d/hello.txt", "missing.zip.d/x"} {
		if got, err := AbsPath(fsys, name); err == nil {
			t.Errorf("AbsPath(%q) = %q, want error", name, got)
		}
	}
}

// TestNestFSDotDNextToFile verifies that a ".d" name is an archive directory
// only when the name it extends is an archive.
func TestNestFSDotDNextToFile(t *testing.T) {
	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()
	ufsTesting.Must(t, fsys.MkdirAll("conf.d", fs.ModePerm))
	for _, name := range []string{"conf", "conf.d/x"} {
		f, err := fsys.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) = %v, want nil", name, err)
		}
		ufsTesting.Must(t, f.Close())
	}
	entries, err := fs.ReadDir(fsys, "conf.d")
	if err != nil {
		t.Fatalf("ReadDir(conf.d) = %v, want nil", err)
	}
	if diff := cmp.Diff([]string{"x"}, ufsTesting.DirEntryListToNames(entries)); diff != "" {
		t.Errorf("ReadDir(conf.d) mismatch (-want +got):\n%s", diff)
	}
}

// TestNestFSWithoutArchiveDriver verifies that archives are plain files when
// no archive driver is registered. It is not parallel: it swaps the global
// archive driver.
func TestNestFSWithoutArchiveDriver(t *testing.T) {
	driver := globalArchiveDriver.Swap(nil)
	defer globalArchiveDriver.Store(driver)

	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()
	writeTestZip(t, fsys, "a.zip")

	if _, err := fsys.Stat("a.zip.d"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(a.zip.d) = %v, want fs.ErrNotExist", err)
	}
	if _, err := fsys.Open("a.zip.d/hello.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open(a.zip.d/hello.txt) = %v, want fs.ErrNotExist", err)
	}
	entries, err := fs.ReadDir(fsys, pathutil.CwdPath)
	if err != nil {
		t.Fatalf("ReadDir(.) = %v, want nil", err)
	}
	if diff := cmp.Diff([]string{"a.zip"}, ufsTesting.DirEntryListToNames(entries)); diff != "" {
		t.Errorf("ReadDir(.) mismatch (-want +got):\n%s", diff)
	}
	if nfs, ok := fsys.(MountedArchiveDirFS); !ok || nfs.IsMountedArchiveDir("a.zip.d") {
		t.Errorf("IsMountedArchiveDir(a.zip.d) = true, want false without an archive driver")
	}
	// The name is free for an ordinary directory.
	if err := fsys.MkdirAll("a.zip.d", fs.ModePerm); err != nil {
		t.Errorf("MkdirAll(a.zip.d) = %v, want nil", err)
	}
}
