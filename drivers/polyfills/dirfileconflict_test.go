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
	found, isDir := "", false
	if f.dirs[pathutil.CwdPath] {
		found, isDir = pathutil.CwdPath, true
	}
	// The prefixes of an [fs.ValidPath] name are its ancestor chain, so each
	// lookup consults one level without cutting the name into new strings.
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

// conflictOp stands in for the op constant a polyfill caller passes, such as
// createFileOp.
const conflictOp = "mkdir"

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
		isDir    bool
		wantErr  string
		wantIs   error
		wantDirs []string
	}{
		{
			name:     "a file may be replaced by a file",
			seed:     seedFileInDir,
			target:   "d/f.txt",
			isDir:    false,
			wantDirs: []string{"d"},
		},
		{
			name:     "a directory may be reused by a directory",
			seed:     seedDir,
			target:   "d",
			isDir:    true,
			wantDirs: []string{"d"},
		},
		{
			name:     "a directory refuses a file",
			seed:     seedDir,
			target:   "d",
			isDir:    false,
			wantErr:  `"d" is a directory`,
			wantIs:   fs.ErrInvalid,
			wantDirs: []string{"d"},
		},
		{
			name:     "a file refuses a directory",
			seed:     seedFileInDir,
			target:   "d/f.txt",
			isDir:    true,
			wantErr:  `"d/f.txt" is not a directory`,
			wantIs:   fs.ErrExist,
			wantDirs: []string{"d"},
		},
		{
			name:     "a file refuses a file below it",
			seed:     seedFileInDir,
			target:   "d/f.txt/g.txt",
			isDir:    false,
			wantErr:  `"d/f.txt" is not a directory`,
			wantIs:   fs.ErrExist,
			wantDirs: []string{"d"},
		},
		{
			name:     "a file refuses a directory below it",
			seed:     seedFileInDir,
			target:   "d/f.txt/sub",
			isDir:    true,
			wantErr:  `"d/f.txt" is not a directory`,
			wantIs:   fs.ErrExist,
			wantDirs: []string{"d"},
		},
		{
			name:     "a link to a directory may be reused by a directory",
			wrap:     withLinks,
			seed:     seedDir,
			target:   "link",
			isDir:    true,
			wantDirs: []string{"d"},
		},
		{
			name:     "a link to a directory refuses a file",
			wrap:     withLinks,
			seed:     seedDir,
			target:   "link",
			isDir:    false,
			wantErr:  `"link" is a directory`,
			wantIs:   fs.ErrInvalid,
			wantDirs: []string{"d"},
		},
		{
			name:     "a link to a file refuses what is below it",
			wrap:     withLinks,
			seed:     seedFileInDir,
			target:   "filelink/sub",
			isDir:    true,
			wantErr:  `"filelink" is not a directory`,
			wantIs:   fs.ErrExist,
			wantDirs: []string{"d"},
		},
		{
			name:     "a link to a directory holds what is below it",
			wrap:     withLinks,
			seed:     seedDir,
			target:   "link/sub",
			isDir:    true,
			wantDirs: []string{"d"},
		},
		{
			name:     "a file deep in the chain is found",
			seed:     seedDeepFile,
			target:   "a/b/c/d/e",
			isDir:    true,
			wantErr:  `"a/b/c" is not a directory`,
			wantIs:   fs.ErrExist,
			wantDirs: []string{"a", "a/b"},
		},
		{
			name:     "a missing name is accepted",
			seed:     seedDir,
			target:   "d/sub/f.txt",
			isDir:    false,
			wantDirs: []string{"d"},
		},
		{
			name:    "the root is a directory",
			seed:    seedRootFile,
			target:  ".",
			isDir:   false,
			wantErr: `"." is a directory`,
			wantIs:  fs.ErrInvalid,
		},
		{
			name: "a file system that cannot be queried is accepted",
			wrap: func(s *store) fs.FS {
				return brokenFS{s}
			},
			seed:     seedDir,
			target:   "d/f.txt",
			isDir:    false,
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
			err := CheckDirFileConflict(wrap(s), conflictOp, tc.target, tc.isDir)
			checkWantErr(t, "CheckDirFileConflict", err, tc.wantErr)
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("CheckDirFileConflict(%v, %q) = %v, want it to match %v",
					tc.isDir, tc.target, err, tc.wantIs)
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

func TestCheckDirFileConflictErrorNamesTheBlocker(t *testing.T) {
	t.Parallel()

	s := newStore()
	s.seedFile("d/f.txt", "alpha")

	// "open" is not derivable from isDir, so it shows the op is the caller's.
	err := CheckDirFileConflict(mkdirAllFS{s}, "open", "d/f.txt/sub/deep", true)
	if err == nil {
		t.Fatal("CheckDirFileConflict() = nil, want the blocking file reported")
	}
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("CheckDirFileConflict() = %v (%T), want an *fs.PathError", err, err)
	}
	if pathErr.Op != "open" || pathErr.Path != "d/f.txt/sub/deep" {
		t.Errorf("the error op and path = %q %q, want the caller's op and the requested name", pathErr.Op, pathErr.Path)
	}
	if !strings.Contains(pathErr.Err.Error(), `"d/f.txt"`) {
		t.Errorf("CheckDirFileConflict() = %v, want it to name the blocking file", err)
	}
	if strings.Contains(pathErr.Err.Error(), "sub/deep") {
		t.Errorf("CheckDirFileConflict() = %v, want the message to name only the blocker, not the target", err)
	}
}

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
			name:       "a missing name reports the file above it",
			seed:       seedDeepFile,
			target:     "a/b/c/d/e",
			wantParent: "a/b/c",
			wantStats:  3,
		},
		{
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
	isDirs := []bool{false, true}

	for _, seed := range seeds {
		for _, target := range targets {
			for _, isDir := range isDirs {
				name := seed.name + " as " + target + " " + kindName(isDir)
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					walked := newStore()
					seed.seed(walked)
					walkedErr := CheckDirFileConflict(mkdirAllFS{walked}, conflictOp, target, isDir)

					indexed := newStore()
					seed.seed(indexed)
					indexedErr := CheckDirFileConflict(indexedFS{indexed, &costs{}}, conflictOp, target, isDir)

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

func kindName(isDir bool) string {
	if isDir {
		return "as a directory"
	}
	return "as a file"
}

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
		isDir   bool
		wantErr string
		wantIs  error
	}{
		{
			name:    "a file blocks a directory below it",
			target:  "a.txt/sub/deep",
			isDir:   true,
			wantErr: `"a.txt" is not a directory`,
			wantIs:  fs.ErrExist,
		},
		{
			name:    "a file blocks a file below it",
			target:  "a.txt/sub/deep.txt",
			isDir:   false,
			wantErr: `"a.txt" is not a directory`,
			wantIs:  fs.ErrExist,
		},
		{
			name:   "a directory holds what is below it",
			target: "held/sub/deep.txt",
			isDir:  false,
		},
		{
			name:    "a file refuses a directory at itself",
			target:  "a.txt",
			isDir:   true,
			wantErr: `"a.txt" is not a directory`,
			wantIs:  fs.ErrExist,
		},
		{
			name:   "a file may be replaced by a file",
			target: "a.txt",
			isDir:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := CheckDirFileConflict(fsys, conflictOp, tc.target, tc.isDir)
			checkWantErr(t, "CheckDirFileConflict", err, tc.wantErr)
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("CheckDirFileConflict(%v, %q) = %v, want it to match %v",
					tc.isDir, tc.target, err, tc.wantIs)
			}
		})
	}
}

func TestCheckDirFileConflictWalkTerminates(t *testing.T) {
	t.Parallel()

	done := make(chan error, 1)
	go func() {
		done <- CheckDirFileConflict(missingFS{}, conflictOp, "/a", true)
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

func BenchmarkCheckDirFileConflict(b *testing.B) {
	benchmarkCheckDirFileConflict(b, func(s *store) fs.FS { return mkdirFS{s} })
}

func BenchmarkCheckDirFileConflictClosestParent(b *testing.B) {
	benchmarkCheckDirFileConflict(b, func(s *store) fs.FS { return indexedFS{s, &costs{}} })
}

func benchmarkCheckDirFileConflict(b *testing.B, wrap func(*store) fs.FS) {
	b.Helper()
	b.ReportAllocs()

	names := []struct {
		name  string
		isDir bool
	}{
		{
			name:  "a.txt",
			isDir: false,
		},
		{
			name:  "a/b/c/d/e/f.txt",
			isDir: false,
		},
		{
			name:  "a/b/c/d/e",
			isDir: true,
		},
		{
			name:  "x/y/z/w/v/f.txt",
			isDir: false,
		},
	}
	for _, tc := range names {
		b.Run(tc.name, func(b *testing.B) {
			s := newStore()
			s.seedDir("a/b/c/d/e")
			fsys := wrap(s)
			for range b.N {
				if err := CheckDirFileConflict(fsys, conflictOp, tc.name, tc.isDir); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
