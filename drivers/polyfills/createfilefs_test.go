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
	"io"
	"io/fs"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
)

// unqueryableOpenFileFS implements [types.OpenFileFS] but cannot be queried:
// every open fails with something other than [fs.ErrNotExist], so the stat that
// refuses a directory learns nothing and the file system has to decide for
// itself when the file is opened.
type unqueryableOpenFileFS struct {
	*store
}

func (f unqueryableOpenFileFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
}

func (f unqueryableOpenFileFS) OpenFile(name string, flag int, perm fs.FileMode) (types.File, error) {
	return f.openFile(name, flag, perm)
}

// seedDirD is a directory the file system already holds, so a name inside it can
// be created without the standin making a parent for it.
func seedDirD(s *store) {
	s.seedDir("d")
}

// seedFileInD is a regular file in a directory, so a name below it has a
// directory for its parent and a file for its ancestor.
func seedFileInD(s *store) {
	s.seedFile("d/f.txt", "alpha")
}

func TestCreate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		wrap  func(*store) fs.FS
		seed  func(*store)
		file  string
		write string
		// wantCloses counts the handles the standin closed itself. Querying a
		// name that is already there costs one open and one close, which is the
		// price of refusing a directory before anything is written to it.
		wantCloses    int
		wantErr       string
		wantFiles     map[string]string
		wantPerms     map[string]fs.FileMode
		wantDirs      []string
		wantMkdirAlls []string
		wantOpens     int
	}{
		{
			// A file system that can create files knows better than a standin what
			// mode to give a new one, so its own method must win even when the
			// emulation is available too. The perm of the native arm is the zero
			// the fake store asks for, not the createPerm the emulation would
			// have handed to OpenFile, which is what tells the two apart.
			name: "native CreateFileFS wins over the emulation",
			wrap: func(s *store) fs.FS {
				return createOpenFS{s}
			},
			file:      "b.txt",
			write:     "beta",
			wantFiles: map[string]string{"b.txt": "beta"},
			wantPerms: map[string]fs.FileMode{"b.txt": 0},
			wantOpens: 1,
		},
		{
			name: "emulated from OpenFileFS",
			wrap: func(s *store) fs.FS {
				return openFileFS{s}
			},
			file:      "b.txt",
			write:     "beta",
			wantFiles: map[string]string{"b.txt": "beta"},
			wantPerms: map[string]fs.FileMode{"b.txt": createPerm},
			wantOpens: 1,
		},
		{
			// Replacing a file means its old content is gone, which is what the
			// truncate flag is for. The mode the file already had is left alone,
			// since new content says nothing about who may read it.
			name: "an existing file is replaced by the emulation",
			wrap: func(s *store) fs.FS {
				return openFileFS{s}
			},
			seed:       seedAlpha,
			file:       "a.txt",
			write:      "beta",
			wantCloses: 1,
			wantFiles:  map[string]string{"a.txt": "beta"},
			wantPerms:  map[string]fs.FileMode{"a.txt": filePerm},
			wantOpens:  1,
		},
		{
			name: "an existing file is replaced by the native method",
			wrap: func(s *store) fs.FS {
				return createFS{s}
			},
			seed:      seedAlpha,
			file:      "a.txt",
			write:     "beta",
			wantFiles: map[string]string{"a.txt": "beta"},
			wantPerms: map[string]fs.FileMode{"a.txt": filePerm},
			wantOpens: 1,
		},
		{
			name: "a file below a directory that is there",
			wrap: func(s *store) fs.FS {
				return openFileFS{s}
			},
			seed:      seedDirD,
			file:      "d/b.txt",
			write:     "beta",
			wantFiles: map[string]string{"d/b.txt": "beta"},
			wantPerms: map[string]fs.FileMode{"d/b.txt": createPerm},
			wantDirs:  []string{"d"},
			wantOpens: 1,
		},
		{
			// Create is documented not to make parents, so the emulation must not
			// reach for MkdirAll either: the file system reports the missing
			// directory itself and nothing is created for it.
			name: "a missing parent is not created",
			wrap: func(s *store) fs.FS {
				return openFileMkdirAllFS{s}
			},
			file:          "d/b.txt",
			wantErr:       "file does not exist",
			wantFiles:     map[string]string{},
			wantMkdirAlls: nil,
		},
		{
			name: "a missing parent on the native path",
			wrap: func(s *store) fs.FS {
				return createFS{s}
			},
			file:      "d/b.txt",
			wantErr:   "file does not exist",
			wantFiles: map[string]string{},
		},
		{
			// A directory cannot hold what the caller is about to write into it,
			// so it is refused rather than handed back as a handle that fails on
			// the first write.
			name: "a directory is refused by the emulation",
			wrap: func(s *store) fs.FS {
				return openFileFS{s}
			},
			seed:      seedDirD,
			file:      "d",
			wantErr:   "is a directory",
			wantFiles: map[string]string{},
			wantDirs:  []string{"d"},
		},
		{
			// The file system's own method is asked even for a name the standin
			// would refuse, because it knows its own tree better than a standin
			// does, and whatever it reports is what the caller gets.
			name: "a directory is refused by the native method",
			wrap: func(s *store) fs.FS {
				return createFS{s}
			},
			seed:      seedDirD,
			file:      "d",
			wantErr:   "invalid argument",
			wantFiles: map[string]string{},
			wantDirs:  []string{"d"},
		},
		{
			// The name stands where a directory has to be for the file to exist,
			// so the emulation refuses it instead of asking for a file that can
			// never be opened.
			name: "a file below a file is refused by the emulation",
			wrap: func(s *store) fs.FS {
				return openFileFS{s}
			},
			seed:      seedFileInD,
			file:      "d/f.txt/g.txt",
			wantErr:   `"d/f.txt" is not a directory`,
			wantFiles: map[string]string{"d/f.txt": "alpha"},
			wantDirs:  []string{"d"},
		},
		{
			// A native create asks its own back end, which reports the missing
			// directory in its own words: the standin does not second-guess it.
			name: "a file below a file is left to the native method",
			wrap: func(s *store) fs.FS {
				return createFS{s}
			},
			seed:      seedFileInD,
			file:      "d/f.txt/g.txt",
			wantErr:   "file does not exist",
			wantFiles: map[string]string{"d/f.txt": "alpha"},
			wantDirs:  []string{"d"},
		},
		{
			name: "the root is refused",
			wrap: func(s *store) fs.FS {
				return openFileFS{s}
			},
			seed:      seedAlpha,
			file:      pathutil.CwdPath,
			wantErr:   "the root is a directory",
			wantFiles: map[string]string{"a.txt": "alpha"},
		},
		{
			name: "an invalid path is refused",
			wrap: func(s *store) fs.FS {
				return openFileFS{s}
			},
			seed:      seedAlpha,
			file:      "../escape",
			wantErr:   "is not a valid path",
			wantFiles: map[string]string{"a.txt": "alpha"},
		},
		{
			name: "no write interface at all",
			wrap: func(s *store) fs.FS {
				return plainFS{s}
			},
			seed:      seedAlpha,
			file:      "b.txt",
			wantErr:   `does not support "create"`,
			wantFiles: map[string]string{"a.txt": "alpha"},
		},
		{
			// A batch interface writes whole files at once, so it cannot hand back
			// a writable handle and is no use here.
			name: "BatchCreateFS alone is not enough",
			wrap: func(s *store) fs.FS {
				return batchFS{s}
			},
			seed:      seedAlpha,
			file:      "b.txt",
			wantErr:   `does not support "create"`,
			wantFiles: map[string]string{"a.txt": "alpha"},
		},
		{
			// A stat that fails for a reason other than a missing name leaves the
			// name unknown, which is not a reason to refuse the create.
			name: "a name that cannot be queried is created anyway",
			wrap: func(s *store) fs.FS {
				return unqueryableOpenFileFS{s}
			},
			file:      "b.txt",
			write:     "beta",
			wantFiles: map[string]string{"b.txt": "beta"},
			wantPerms: map[string]fs.FileMode{"b.txt": createPerm},
			wantOpens: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			if tc.seed != nil {
				tc.seed(s)
			}
			f, err := Create(tc.wrap(s), tc.file)
			checkWantErr(t, "Create", err, tc.wantErr)
			if err == nil && f == nil {
				t.Fatalf("Create(%q) = a nil handle and no error, want a handle", tc.file)
			}
			if f != nil {
				// The handle the caller was handed must still be open, so the
				// only close allowed is the one the stat of an existing name
				// needed for itself.
				if s.closeCalls != tc.wantCloses {
					t.Errorf("Create(%q) closed %d handles, want %d", tc.file, s.closeCalls, tc.wantCloses)
				}
				if tc.write != "" {
					if _, err := f.WriteString(tc.write); err != nil {
						t.Fatalf("WriteString(%q) = %v, want nil", tc.write, err)
					}
				}
				if err := f.Close(); err != nil {
					t.Errorf("Close() = %v, want nil", err)
				}
			}

			if len(s.files) != len(tc.wantFiles) {
				t.Errorf("Create(%q) left %d files (%v), want %d (%v)", tc.file,
					len(s.files), slices.Sorted(maps.Keys(s.files)),
					len(tc.wantFiles), slices.Sorted(maps.Keys(tc.wantFiles)))
			}
			for name, want := range tc.wantFiles {
				if got := string(s.files[name]); got != want {
					t.Errorf("Create(%q) left %q in %q, want %q", tc.file, got, name, want)
				}
			}
			for name, want := range tc.wantPerms {
				if got := s.perms[name]; got != want {
					t.Errorf("Create(%q) perm for %q = %v, want %v", tc.file, name, got, want)
				}
			}
			if dirs := createdDirs(s); !reflect.DeepEqual(dirs, tc.wantDirs) {
				t.Errorf("Create(%q) left directories %v, want %v", tc.file, dirs, tc.wantDirs)
			}
			if !reflect.DeepEqual(s.mkdirAllCalls, tc.wantMkdirAlls) {
				t.Errorf("Create(%q) called MkdirAll %v, want %v", tc.file, s.mkdirAllCalls, tc.wantMkdirAlls)
			}
			if s.openCalls != tc.wantOpens {
				t.Errorf("Create(%q) opened %d handles, want %d", tc.file, s.openCalls, tc.wantOpens)
			}
		})
	}
}

// TestCreateHandle checks that the handle Create hands back is a whole
// [types.File], since that handle is all the caller has to write through.
func TestCreateHandle(t *testing.T) {
	t.Parallel()

	for name, wrap := range map[string]func(*store) fs.FS{
		"CreateFileFS": func(s *store) fs.FS {
			return createFS{s}
		},
		"OpenFileFS": func(s *store) fs.FS {
			return openFileFS{s}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			f, err := Create(wrap(s), "a.txt")
			if err != nil {
				t.Fatalf("Create() = %v, want nil", err)
			}
			if _, err := f.Write([]byte("beta")); err != nil {
				t.Fatalf("Write() = %v, want nil", err)
			}
			info, err := f.Stat()
			if err != nil {
				t.Fatalf("Stat() = %v, want nil", err)
			}
			if info.Name() != "a.txt" {
				t.Errorf("Stat().Name() = %q, want %q", info.Name(), "a.txt")
			}
			if info.Size() != int64(len("beta")) {
				t.Errorf("Stat().Size() = %d, want %d", info.Size(), len("beta"))
			}
			head := make([]byte, 4)
			if n, err := f.ReadAt(head, 0); err != nil {
				t.Errorf("ReadAt() = %d, %v, want the content that was written", n, err)
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				t.Errorf("Seek() = %v, want nil", err)
			}
			if err := f.Close(); err != nil {
				t.Errorf("Close() = %v, want nil", err)
			}
			if got := string(s.files["a.txt"]); got != "beta" {
				t.Errorf("Create() left %q in a.txt, want %q", got, "beta")
			}
		})
	}
}
