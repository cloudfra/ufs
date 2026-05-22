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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestNewNestFS(t *testing.T) {
	fsys, err := newNestFS("memory://")
	if err != nil {
		t.Fatal(err)
	}
	if fsys == nil {
		t.Fatal("fsys is nil")
	}
}

func TestNewNestFSInvalid(t *testing.T) {
	_, err := newNestFS("invalid://scheme")
	if err == nil {
		t.Fatal("newNestFS with invalid scheme should return an error")
	}
}

func TestMountMap(t *testing.T) {
	mm := makeMountMap("test")
	mfs := makeMemFS("memory:///")
	afs := makeAngryFS(angryFSPrefix)
	nfs := mustNullFS(t)

	must(t, mm.put("mounts/null", makeNestFS(nfs)))
	must(t, mm.put("mounts/mem", makeNestFS(mfs)))
	must(t, mm.put("mounts/angry", makeNestFS(afs)))
	must(t, mm.put("null", makeNestFS(nfs)))
	must(t, mm.put("mem", makeNestFS(mfs)))
	must(t, mm.put("angry", makeNestFS(afs)))
	must(t, mm.put("mounts/level2/a/null", makeNestFS(nfs)))
	must(t, mm.put("mounts/level2/a/mem", makeNestFS(mfs)))
	must(t, mm.put("mounts/level2/angry", makeNestFS(afs)))
	if err := mm.put("mounts/level2/angry/angry", makeNestFS(afs)); err == nil {
		t.Fatal("'mounts/level2/angry/angry' should not be mountable because of 'mounts/level2/angry'")
	}
	if err := mm.put("mounts", makeNestFS(afs)); err == nil {
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
		t.Run(fmt.Sprintf("getMount/%s", tc.input), func(t *testing.T) {
			gotMountPath, gotSubPath, gotFS, ok := mm.getMountX(tc.input)
			wantOk := tc.wantMountPath != ""
			if ok != wantOk {
				t.Errorf("getMount(%q) ok got: %t, want: %t, fsys: %v", tc.input, ok, wantOk, gotFS)
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
	fsys, err := newNestFS(cwdPath)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, fsys, "testing/testassets/files/index.html", "testing/testassets/files/index.html")
	assertContains(t, fsys, "testing/testassets/archives/nested-testassets.zip.d/site.js", "testing/testassets/files/site.js")
	assertContains(t, fsys, "testing/testassets/archives/nested-testassets.zip.d/single-testassets.zip.d/index.html", "testing/testassets/files/index.html")
	assertDir(t, fsys, "testing/testassets/archives", []string{
		"nested-testassets.zip",
		"nested-testassets.zip.d",
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
}

func TestNestedFS(t *testing.T) {
	fsys, err := New("memory://?a=file:///&mounted/null=null://&mounted/angry=angry://")
	if err != nil {
		t.Fatal(err)
	}
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
	fsys, err := newNestFS("memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	nfs := fsys.(*nestFS)
	if err := nfs.MkdirAll("subdir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("subdir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

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
	fsys, err := newNestFS("memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	nfs := fsys.(*nestFS)
	f, err := fsys.Create("regular.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

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
	fsys, err := newNestFS("memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	nfs := fsys.(*nestFS)
	wf, err := fsys.Create("statme.txt")
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(wf, "hello")
	wf.Close()

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
	fsys, err := newNestFS("memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	nfs := fsys.(*nestFS)
	if err := nfs.MkdirAll("readdir-test", fs.ModePerm); err != nil {
		t.Fatal(err)
	}

	f, err := nfs.Open("readdir-test")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	buf := make([]byte, 16)
	n, _ := f.Read(buf)
	if n != 0 {
		t.Errorf("Read() on directory returned %d bytes, want 0", n)
	}
}

func TestNestFSCloseAngryFS(t *testing.T) {
	nfs := makeNestFS(makeAngryFS(angryFSPrefix))
	err := nfs.Close()
	if err == nil {
		t.Fatal("Close() of nestFS wrapping angryFS = nil, want error")
	}
}

func TestNestFSCloseMountError(t *testing.T) {
	// A nestFS whose mount itself fails to close should propagate the error.
	outer := makeNestFS(makeNullFS("null://"))
	angryMount := makeNestFS(makeAngryFS(angryFSPrefix))
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
	angryNFS := makeNestFS(makeAngryFS(angryFSPrefix))
	if err := mm.put("angry", angryNFS); err != nil {
		t.Fatal(err)
	}
	err := mm.Close()
	if err == nil {
		t.Fatal("mountMap.Close() with angry mount = nil, want error")
	}
}

func TestNestFSGlobFallback(t *testing.T) {
	// archiveFS does not implement fs.GlobFS, triggering the globFS fallback in nestFS.
	afs := mustArchiveFS(t)
	nfs := makeNestFS(afs)
	defer nfs.Close()

	matches, err := nfs.Glob("*.html")
	if err != nil {
		t.Fatalf("Glob() = %v, want nil", err)
	}
	if len(matches) == 0 {
		t.Error("Glob(*.html) = 0 matches, want at least one")
	}
}

func TestNestFSOperations(t *testing.T) {
	fsys, err := newNestFS("memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	nfs := fsys.(*nestFS)
	if err := nfs.MkdirAll("sub/dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	f, err := nfs.Create("sub/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(f, "hello")
	f.Close()

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
		f.Close()
	})
}

// TestNestFSOpenArchiveFileIsSeekable verifies that files opened from inside
// a tar archive through nestFS implement io.ReadSeeker (needed for Range requests).
func TestNestFSOpenArchiveFileIsSeekable(t *testing.T) {
	fsys, err := newNestFS(cwdPath)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()
	nfs := fsys.(*nestFS)

	// testassets.tar is a plain tar (no compression) — its entries are sequential streams.
	f, err := nfs.Open("testing/testassets/archives/testassets.tar.d/index.html")
	if err != nil {
		t.Fatalf("Open() = %v, want nil", err)
	}
	defer f.Close()

	rs, ok := f.(io.ReadSeeker)
	if !ok {
		t.Fatal("file from tar archive does not implement io.ReadSeeker, want ReadSeeker for Range request support")
	}

	// Read a few bytes to advance the position.
	buf := make([]byte, 4)
	if _, err := rs.Read(buf); err != nil {
		t.Fatalf("Read() = %v", err)
	}

	// Seek back to beginning.
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("Seek(0, SeekStart) = %v", err)
	}

	// Full read from position 0 must return non-empty content.
	data, err := io.ReadAll(rs)
	if err != nil {
		t.Fatalf("ReadAll() after seek = %v", err)
	}
	if len(data) == 0 {
		t.Error("ReadAll() returned 0 bytes after seek, want non-empty")
	}

	// Also verify io.ReaderAt for ServeContent compatibility.
	ra, ok := f.(io.ReaderAt)
	if !ok {
		t.Fatal("file from tar archive does not implement io.ReaderAt")
	}
	p := make([]byte, 4)
	if _, err := ra.ReadAt(p, 0); err != nil {
		t.Fatalf("ReadAt(0) = %v", err)
	}
}

func TestNestFSValidPathClosed(t *testing.T) {
	fsys, err := newNestFS("memory://")
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
