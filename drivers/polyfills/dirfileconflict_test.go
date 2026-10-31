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
	"reflect"
	"strings"
	"testing"
	"time"
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
			kind:     WantFile,
			wantDirs: []string{"d"},
		},
		{
			name:     "a directory may be reused by a directory",
			seed:     seedDir,
			target:   "d",
			kind:     WantDir,
			wantDirs: []string{"d"},
		},
		{
			// A directory cannot hold what the caller is about to write into it,
			// so the same check reports the conflict Create has to refuse.
			name:     "a directory refuses a file",
			seed:     seedDir,
			target:   "d",
			kind:     WantFile,
			wantErr:  `"d" is a directory`,
			wantIs:   fs.ErrInvalid,
			wantDirs: []string{"d"},
		},
		{
			name:     "a file refuses a directory",
			seed:     seedFileInDir,
			target:   "d/f.txt",
			kind:     WantDir,
			wantErr:  `"d/f.txt" is not a directory`,
			wantIs:   fs.ErrExist,
			wantDirs: []string{"d"},
		},
		{
			name:     "a file refuses a file below it",
			seed:     seedFileInDir,
			target:   "d/f.txt/g.txt",
			kind:     WantFile,
			wantErr:  `"d/f.txt" is not a directory`,
			wantIs:   fs.ErrExist,
			wantDirs: []string{"d"},
		},
		{
			name:     "a file refuses a directory below it",
			seed:     seedFileInDir,
			target:   "d/f.txt/sub",
			kind:     WantDir,
			wantErr:  `"d/f.txt" is not a directory`,
			wantIs:   fs.ErrExist,
			wantDirs: []string{"d"},
		},
		{
			// Several levels are missing between the target and the file, so the
			// blockage is only found by walking up past the missing levels: an
			// operation that creates levels would create them and reach the file.
			name:     "a file deep in the chain is found",
			seed:     seedDeepFile,
			target:   "a/b/c/d/e",
			kind:     WantDir,
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
			kind:     WantFile,
			wantDirs: []string{"d"},
		},
		{
			name:    "the root is a directory",
			seed:    seedRootFile,
			target:  ".",
			kind:    WantFile,
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
			kind:     WantFile,
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
			err := CheckDirFileConflict(wrap(s), "mkdir", tc.target, tc.kind)
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

// TestCheckDirFileConflictErrorShape pins the error shape the write interfaces
// document, so a caller can find the refused name without parsing the message.
func TestCheckDirFileConflictErrorShape(t *testing.T) {
	t.Parallel()

	s := newStore()
	s.seedFile("d/f.txt", "alpha")

	err := CheckDirFileConflict(mkdirAllFS{s}, "mkdir", "d/f.txt/sub", WantDir)
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("CheckDirFileConflict() = %v (%T), want an *fs.PathError", err, err)
	}
	if pathErr.Op != "mkdir" {
		t.Errorf("CheckDirFileConflict() Op = %q, want %q", pathErr.Op, "mkdir")
	}
	if pathErr.Path != "d/f.txt/sub" {
		t.Errorf("CheckDirFileConflict() Path = %q, want %q", pathErr.Path, "d/f.txt/sub")
	}
	if !strings.Contains(pathErr.Err.Error(), `"d/f.txt" is not a directory`) {
		t.Errorf("CheckDirFileConflict() inner error = %q, want it to name the blocking file", pathErr.Err)
	}
}

// TestCheckDirFileConflictWalkTerminates checks that the walk up stops at a name
// that has no parent above it, which an absolute name reaches and an
// [fs.ValidPath] name never does.
func TestCheckDirFileConflictWalkTerminates(t *testing.T) {
	t.Parallel()

	done := make(chan error, 1)
	go func() {
		done <- CheckDirFileConflict(missingFS{}, "mkdir", "/a", WantDir)
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
// has to run it itself, which is one stat for the name and one for each level
// walked up from it.
func BenchmarkCheckDirFileConflict(b *testing.B) {
	b.ReportAllocs()

	names := []struct {
		name string
		kind NodeKind
	}{
		{
			name: "a.txt",
			kind: WantFile,
		},
		{
			name: "a/b/c/d/e/f.txt",
			kind: WantFile,
		},
		{
			name: "a/b/c/d/e",
			kind: WantDir,
		},
		{
			// Nothing above the name exists, so every level gets a stat.
			name: "x/y/z/w/v/f.txt",
			kind: WantFile,
		},
	}
	for _, tc := range names {
		b.Run(tc.name, func(b *testing.B) {
			s := newStore()
			s.seedDir("a/b/c/d/e")
			fsys := mkdirFS{s}
			for range b.N {
				if err := CheckDirFileConflict(fsys, "create", tc.name, tc.kind); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
