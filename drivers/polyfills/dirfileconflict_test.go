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

package polyfills

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// missingFS answers every query as missing, which leaves an absolute name's
// walk up with no parent chain reaching the root.
type missingFS struct{}

func (f missingFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (f missingFS) Stat(name string) (fs.FileInfo, error) {
	return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
}

// maxStatLinks caps how many links one stat of the fake below will follow, so a
// link cycle cannot hang a test.
const maxStatLinks = 8

// linkFS reports a symlink as the node it points at, which is what [fs.Stat] is
// documented to do for a file system that has links.
type linkFS struct {
	*store
	links map[string]string
}

func (f linkFS) Open(name string) (fs.File, error) {
	return f.open(name)
}

func (f linkFS) Stat(name string) (fs.FileInfo, error) {
	for hop := 0; hop < maxStatLinks; hop++ {
		target, ok := f.links[name]
		if !ok {
			break
		}
		name = target
	}
	if f.dirs[name] {
		return fakeInfo{name: path.Base(name), mode: fs.ModeDir | dirPerm}, nil
	}
	if data, ok := f.files[name]; ok {
		return fakeInfo{name: path.Base(name), size: int64(len(data)), mode: filePerm}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
}

// costs counts what one check pays for, which is how the tests tell the query
// walk apart from the optimized answer.
type costs struct {
	stats   int
	queries int
}

// countedFS counts the stats of the walk up, standing in for a file system that
// only answers [fs.Stat].
type countedFS struct {
	fs.FS
	*costs
}

func (f countedFS) Stat(name string) (fs.FileInfo, error) {
	f.stats++
	return fs.Stat(f.FS, name)
}

// indexedFS answers the closest-ancestor query from the node table it already
// holds, which is what a back end that resolves a path for its own operation
// reports: it pays no stat per level.
type indexedFS struct {
	*store
	*costs
}

var _ types.ClosestParentFS = indexedFS{}

func (f indexedFS) Open(name string) (fs.File, error) { return f.open(name) }

func (f indexedFS) ClosestParent(name string) (string, bool, error) {
	f.queries++
	if !fs.ValidPath(name) {
		return "", false, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	// The prefixes of an [fs.ValidPath] name are its ancestor chain, so each
	// lookup consults one level without cutting the name into new strings.
	found, isDir := "", false
	if f.dirs[pathutil.CwdPath] {
		found, isDir = pathutil.CwdPath, true
	}
	for i := 0; i <= len(name); i++ {
		if i < len(name) && name[i] != '/' {
			continue
		}
		prefix := name[:i]
		if f.dirs[prefix] {
			found, isDir = prefix, true
			continue
		}
		if _, ok := f.files[prefix]; ok {
			found, isDir = prefix, false
			break
		}
	}
	if found == "" {
		return "", false, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return found, isDir, nil
}

func TestCheckDirFileConflict(t *testing.T) {
	t.Parallel()

	seedFileInDir := func(s *store) {
		s.seedFile("d/f.txt", "alpha")
	}
	seedDir := func(s *store) {
		s.seedDir("d")
	}
	seedDeepFile := func(s *store) {
		s.seedFile("a/b/c", "data")
	}
	seedRootFile := func(s *store) {
		s.seedFile("a.txt", "alpha")
	}
	// withLinks reports link as the directory d and filelink as the file d/f.txt,
	// so a name can also be reached as a symlink over either seed.
	withLinks := func(s *store) fs.FS {
		return linkFS{
			store: s,
			links: map[string]string{
				"link":     "d",
				"filelink": "d/f.txt",
			},
		}
	}

	testCases := []struct {
		name     string
		wrap     func(*store) fs.FS
		seed     func(*store)
		target   string
		kind     NodeKind
		wantErr  string
		wantIs   error
		wantDirs []string
	}{
		{
			name:     "a file may be replaced by a file",
			seed:     seedFileInDir,
			target:   "d/f.txt",
			kind:     FileNode,
			wantDirs: []string{"d"},
		},
		{
			name:     "a directory may be reused by a directory",
			seed:     seedDir,
			target:   "d",
			kind:     DirNode,
			wantDirs: []string{"d"},
		},
		{
			// A directory cannot hold what the caller is about to write into it,
			// so the same check reports the conflict Create has to refuse.
			name:     "a directory refuses a file",
			seed:     seedDir,
			target:   "d",
			kind:     FileNode,
			wantErr:  `"d" is a directory`,
			wantIs:   fs.ErrInvalid,
			wantDirs: []string{"d"},
		},
		{
			name:     "a file refuses a directory",
			seed:     seedFileInDir,
			target:   "d/f.txt",
			kind:     DirNode,
			wantErr:  `"d/f.txt" is not a directory`,
			wantIs:   fs.ErrExist,
			wantDirs: []string{"d"},
		},
		{
			name:     "a file refuses a file below it",
			seed:     seedFileInDir,
			target:   "d/f.txt/g.txt",
			kind:     FileNode,
			wantErr:  `"d/f.txt" is not a directory`,
			wantIs:   fs.ErrExist,
			wantDirs: []string{"d"},
		},
		{
			// The reported shape: a.txt standing as a file leaves mkdir -p
			// a.txt/dir nowhere to put the directory it was asked for.
			name:     "a file refuses a directory below it",
			seed:     seedFileInDir,
			target:   "d/f.txt/sub",
			kind:     DirNode,
			wantErr:  `"d/f.txt" is not a directory`,
			wantIs:   fs.ErrExist,
			wantDirs: []string{"d"},
		},
		{
			// [fs.Stat] resolves a symlink, so the link is judged as the directory
			// it points at and the directory the operation places is already there.
			name:     "a link to a directory may be reused by a directory",
			wrap:     withLinks,
			seed:     seedDir,
			target:   "link",
			kind:     DirNode,
			wantDirs: []string{"d"},
		},
		{
			name:     "a link to a directory refuses a file",
			wrap:     withLinks,
			seed:     seedDir,
			target:   "link",
			kind:     FileNode,
			wantErr:  `"link" is a directory`,
			wantIs:   fs.ErrInvalid,
			wantDirs: []string{"d"},
		},
		{
			// The link is followed, so the file behind it is what blocks the name
			// below the link, and the error names the link the caller asked about.
			name:     "a link to a file refuses what is below it",
			wrap:     withLinks,
			seed:     seedFileInDir,
			target:   "filelink/sub",
			kind:     DirNode,
			wantErr:  `"filelink" is not a directory`,
			wantIs:   fs.ErrExist,
			wantDirs: []string{"d"},
		},
		{
			// The directory behind the link ends the walk up, so a link to a
			// directory never trips up a create below it.
			name:     "a link to a directory holds what is below it",
			wrap:     withLinks,
			seed:     seedDir,
			target:   "link/sub",
			kind:     DirNode,
			wantDirs: []string{"d"},
		},
		{
			// Several levels are missing between the target and the file, so the
			// blockage is only found by walking up past the missing levels: an
			// operation that creates levels would create them and reach the file.
			name:     "a file deep in the chain is found",
			seed:     seedDeepFile,
			target:   "a/b/c/d/e",
			kind:     DirNode,
			wantErr:  `"a/b/c" is not a directory`,
			wantIs:   fs.ErrExist,
			wantDirs: []string{"a", "a/b"},
		},
		{
			// A missing parent is for the file system to report, which is where
			// the difference between Mkdir and MkdirAll is decided.
			name:     "a missing name is accepted",
			seed:     seedDir,
			target:   "d/sub/f.txt",
			kind:     FileNode,
			wantDirs: []string{"d"},
		},
		{
			name:    "the root is a directory",
			seed:    seedRootFile,
			target:  ".",
			kind:    FileNode,
			wantErr: `"." is a directory`,
			wantIs:  fs.ErrInvalid,
		},
		{
			// Nothing is known about the tree, so nothing may be concluded from
			// it and the file system has to decide for itself.
			name: "a file system that cannot be queried is accepted",
			wrap: func(s *store) fs.FS {
				return brokenFS{s}
			},
			seed:     seedDir,
			target:   "d/f.txt",
			kind:     FileNode,
			wantDirs: []string{"d"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			if tc.seed != nil {
				tc.seed(s)
			}
			wrap := tc.wrap
			if wrap == nil {
				wrap = func(s *store) fs.FS {
					return mkdirAllFS{s}
				}
			}
			err := CheckDirFileConflict(wrap(s), tc.target, tc.kind)
			checkWantErr(t, "CheckDirFileConflict", err, tc.wantErr)
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("CheckDirFileConflict(%v, %q) = %v, want it to match %v",
					tc.kind, tc.target, err, tc.wantIs)
			}
			if dirs := createdDirs(s); !reflect.DeepEqual(dirs, tc.wantDirs) {
				t.Errorf("CheckDirFileConflict(%q) left dirs %v, want %v", tc.target, dirs, tc.wantDirs)
			}
			if calls := s.mkdirCalls; calls != nil {
				t.Errorf("CheckDirFileConflict(%q) called Mkdir %v, want nothing", tc.target, calls)
			}
		})
	}
}

// TestCheckDirFileConflictErrorNamesTheBlocker pins what a caller has to report
// to the user: the node that stands in the way, not only the name that was asked
// for. The caller wraps the error in its own [fs.PathError], so the check names
// the blocker and leaves the operation and path to the caller.
func TestCheckDirFileConflictErrorNamesTheBlocker(t *testing.T) {
	t.Parallel()

	s := newStore()
	s.seedFile("d/f.txt", "alpha")

	err := CheckDirFileConflict(mkdirAllFS{s}, "d/f.txt/sub/deep", DirNode)
	if err == nil {
		t.Fatal("CheckDirFileConflict() = nil, want the blocking file reported")
	}
	if !strings.Contains(err.Error(), `"d/f.txt"`) {
		t.Errorf("CheckDirFileConflict() = %v, want it to name the blocking file", err)
	}
	if strings.Contains(err.Error(), "sub/deep") {
		t.Errorf("CheckDirFileConflict() = %v, want only the blocker named, not the target", err)
	}
	wrapped := ufserrors.NewPathError("mkdir", "d/f.txt/sub/deep", err)
	var pathErr *fs.PathError
	if !errors.As(wrapped, &pathErr) {
		t.Fatalf("the wrapped error = %v (%T), want an *fs.PathError", wrapped, wrapped)
	}
	if pathErr.Op != "mkdir" || pathErr.Path != "d/f.txt/sub/deep" {
		t.Errorf("the wrapped error = %q %q, want mkdir and the requested name", pathErr.Op, pathErr.Path)
	}
}

// TestGetClosestParent checks the ancestor query the conflict check is built on,
// including the levels a real file system refuses instead of reporting missing.
func TestGetClosestParent(t *testing.T) {
	t.Parallel()

	seedFileInDir := func(s *store) { s.seedFile("d/f.txt", "alpha") }
	seedDeepFile := func(s *store) { s.seedFile("a/b/c", "data") }

	testCases := []struct {
		name       string
		wrap       func(*store) fs.FS
		indexed    bool
		seed       func(*store)
		target     string
		wantParent string
		wantIsDir  bool
		wantErr    string
		wantStats  int
		wantQuery  int
	}{
		{
			name:       "the name itself is the closest when it is a file",
			seed:       seedFileInDir,
			target:     "d/f.txt",
			wantParent: "d/f.txt",
			wantStats:  1,
		},
		{
			name:       "the name itself is the closest when it is a directory",
			seed:       seedFileInDir,
			target:     "d",
			wantParent: "d",
			wantIsDir:  true,
			wantStats:  1,
		},
		{
			name:       "a missing name reports the directory above it",
			seed:       seedFileInDir,
			target:     "d/sub/f.txt",
			wantParent: "d",
			wantIsDir:  true,
			wantStats:  3,
		},
		{
			// The missing levels are what the walk is for: the file that blocks
			// them is only two levels up.
			name:       "a missing name reports the file above it",
			seed:       seedDeepFile,
			target:     "a/b/c/d/e",
			wantParent: "a/b/c",
			wantStats:  3,
		},
		{
			// The store always holds its root, so a name with nothing above it
			// still reports a directory.
			name:       "nothing above the name reaches the root",
			target:     "nope/deep/name",
			wantParent: pathutil.CwdPath,
			wantIsDir:  true,
			wantStats:  4,
		},
		{
			name:      "a file system that refuses every level is an error",
			wrap:      func(s *store) fs.FS { return brokenFS{s} },
			seed:      seedFileInDir,
			target:    "d/f.txt",
			wantStats: 3,
			wantErr:   "permission denied",
		},
		{
			name:      "a file system that answers nothing is an error",
			wrap:      func(*store) fs.FS { return missingFS{} },
			target:    "a/b",
			wantStats: 3,
			wantErr:   "file does not exist",
		},
		{
			// The optimized answer is one call and no stat at all, and it reports
			// the same node the walk up finds.
			name:       "an indexed file system answers in one query",
			indexed:    true,
			seed:       seedDeepFile,
			target:     "a/b/c/d/e",
			wantParent: "a/b/c",
			wantQuery:  1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			if tc.seed != nil {
				tc.seed(s)
			}
			cost := &costs{}
			fsys := fs.FS(countedFS{mkdirAllFS{s}, cost})
			switch {
			case tc.indexed:
				fsys = indexedFS{s, cost}
			case tc.wrap != nil:
				fsys = countedFS{tc.wrap(s), cost}
			}
			parent, isDir, err := getClosestParent(fsys, tc.target)
			checkWantErr(t, "getClosestParent", err, tc.wantErr)
			if parent != tc.wantParent {
				t.Errorf("getClosestParent(%q) = %q, want %q", tc.target, parent, tc.wantParent)
			}
			if isDir != tc.wantIsDir {
				t.Errorf("getClosestParent(%q) isDir = %v, want %v", tc.target, isDir, tc.wantIsDir)
			}
			if cost.stats != tc.wantStats {
				t.Errorf("getClosestParent(%q) paid %v stats, want %v", tc.target, cost.stats, tc.wantStats)
			}
			if cost.queries != tc.wantQuery {
				t.Errorf("getClosestParent(%q) paid %v queries, want %v",
					tc.target, cost.queries, tc.wantQuery)
			}
		})
	}
}

// TestGetClosestParentAnswersTheSameEitherWay checks that a file system answering
// the closest-ancestor query itself is judged exactly as the same tree walked up
// a level at a time.
func TestGetClosestParentAnswersTheSameEitherWay(t *testing.T) {
	t.Parallel()

	seeds := []struct {
		name string
		seed func(*store)
	}{
		{"a file below a file", func(s *store) { s.seedFile("d/f.txt", "alpha") }},
		{"a deep file", func(s *store) { s.seedFile("a/b/c", "data") }},
		{"a directory", func(s *store) { s.seedDir("d") }},
	}
	targets := []string{"d/f.txt", "d/f.txt/g/h", "a/b/c/d/e", "d/sub", "nope/deep/name", "."}
	kinds := []NodeKind{FileNode, DirNode}

	for _, seed := range seeds {
		for _, target := range targets {
			for _, kind := range kinds {
				name := seed.name + " as " + target + " " + kindName(kind)
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					walked := newStore()
					seed.seed(walked)
					walkedErr := CheckDirFileConflict(mkdirAllFS{walked}, target, kind)

					indexed := newStore()
					seed.seed(indexed)
					indexedErr := CheckDirFileConflict(indexedFS{indexed, &costs{}}, target, kind)

					if (walkedErr == nil) != (indexedErr == nil) {
						t.Errorf("the two paths disagree: the walk up got %v, the indexed answer got %v",
							walkedErr, indexedErr)
					}
					if walkedErr != nil && walkedErr.Error() != indexedErr.Error() {
						t.Errorf("the two paths report differently: %q against %q", walkedErr, indexedErr)
					}
				})
			}
		}
	}
}

func kindName(kind NodeKind) string {
	if kind == DirNode {
		return "as a directory"
	}
	return "as a file"
}

// TestCheckDirFileConflictUnderAFileOnRealFS checks the conflict a real file
// system reports as a refusal rather than as fs.ErrNotExist: a name below a
// regular file is ENOTDIR, which the walk up has to step over to reach the file
// that blocks it.
func TestCheckDirFileConflictUnderAFileOnRealFS(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "held"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	fsys := os.DirFS(dir)

	testCases := []struct {
		name    string
		target  string
		kind    NodeKind
		wantErr string
		wantIs  error
	}{
		{
			name:    "a file blocks a directory below it",
			target:  "a.txt/sub/deep",
			kind:    DirNode,
			wantErr: `"a.txt" is not a directory`,
			wantIs:  fs.ErrExist,
		},
		{
			name:    "a file blocks a file below it",
			target:  "a.txt/sub/deep.txt",
			kind:    FileNode,
			wantErr: `"a.txt" is not a directory`,
			wantIs:  fs.ErrExist,
		},
		{
			name:   "a directory holds what is below it",
			target: "held/sub/deep.txt",
			kind:   FileNode,
		},
		{
			name:    "a file refuses a directory at itself",
			target:  "a.txt",
			kind:    DirNode,
			wantErr: `"a.txt" is not a directory`,
			wantIs:  fs.ErrExist,
		},
		{
			name:   "a file may be replaced by a file",
			target: "a.txt",
			kind:   FileNode,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := CheckDirFileConflict(fsys, tc.target, tc.kind)
			checkWantErr(t, "CheckDirFileConflict", err, tc.wantErr)
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("CheckDirFileConflict(%v, %q) = %v, want it to match %v",
					tc.kind, tc.target, err, tc.wantIs)
			}
		})
	}
}

// TestCheckDirFileConflictWalkTerminates checks that the walk up stops at a name
// that has no parent above it, which an absolute name reaches and an
// [fs.ValidPath] name never does.
func TestCheckDirFileConflictWalkTerminates(t *testing.T) {
	t.Parallel()

	done := make(chan error, 1)
	go func() {
		done <- CheckDirFileConflict(missingFS{}, "/a", DirNode)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf(`CheckDirFileConflict("/a") = %v, want nil`, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal(`CheckDirFileConflict("/a") did not return, so the walk up never stopped`)
	}
}

// BenchmarkCheckDirFileConflict measures what the check costs a file system that
// only answers [fs.Stat], which is one stat for the name and one for each level
// walked up from it.
func BenchmarkCheckDirFileConflict(b *testing.B) {
	benchmarkCheckDirFileConflict(b, func(s *store) fs.FS { return mkdirFS{s} })
}

// BenchmarkCheckDirFileConflictClosestParent measures the same check against a
// file system answering the closest-ancestor query itself, which is one call no
// matter how many levels the name has.
func BenchmarkCheckDirFileConflictClosestParent(b *testing.B) {
	benchmarkCheckDirFileConflict(b, func(s *store) fs.FS { return indexedFS{s, &costs{}} })
}

func benchmarkCheckDirFileConflict(b *testing.B, wrap func(*store) fs.FS) {
	b.Helper()
	b.ReportAllocs()

	names := []struct {
		name string
		kind NodeKind
	}{
		{
			name: "a.txt",
			kind: FileNode,
		},
		{
			name: "a/b/c/d/e/f.txt",
			kind: FileNode,
		},
		{
			name: "a/b/c/d/e",
			kind: DirNode,
		},
		{
			// Nothing above the name exists, so every level gets queried.
			name: "x/y/z/w/v/f.txt",
			kind: FileNode,
		},
	}
	for _, tc := range names {
		b.Run(tc.name, func(b *testing.B) {
			s := newStore()
			s.seedDir("a/b/c/d/e")
			fsys := wrap(s)
			for range b.N {
				if err := CheckDirFileConflict(fsys, tc.name, tc.kind); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
