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
	"maps"
	"path"
	"reflect"
	"slices"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
)

// writeCall records one native WriteFile call.
type writeCall struct {
	name string
	data string
	mode fs.FileMode
}

// writeFile stores the whole content in one step and records that it was asked,
// the way a native WriteFileFS would. A name that is missing gets its
// permission bits from mode; a name that is already there keeps the ones it has,
// which is what the interface documents for a whole-file write.
func (s *store) writeFile(name string, data []byte, mode fs.FileMode) error {
	s.writeCalls = append(s.writeCalls, writeCall{name: name, data: string(data), mode: mode})
	if !fs.ValidPath(name) || s.dirs[name] {
		return &fs.PathError{Op: "writefile", Path: name, Err: fs.ErrInvalid}
	}
	if _, ok := s.files[name]; !ok {
		if !s.dirs[path.Dir(name)] {
			return &fs.PathError{Op: "writefile", Path: name, Err: fs.ErrNotExist}
		}
		s.perms[name] = mode
	}
	s.files[name] = append([]byte(nil), data...)
	return nil
}

// writeFileFS implements [types.WriteFileFS] and nothing else but the write, so
// a test can tell the whole-file write a file system does itself from one the
// standin writes through a handle.
type writeFileFS struct{ *store }

func (f writeFileFS) Open(name string) (fs.File, error) { return f.open(name) }

func (f writeFileFS) WriteFile(name string, data []byte, mode fs.FileMode) error {
	return f.writeFile(name, data, mode)
}

// writeEveryFS implements [types.WriteFileFS] along with both methods an
// emulation is built from, so the native whole-file write has to win over them.
type writeEveryFS struct{ *store }

func (f writeEveryFS) Open(name string) (fs.File, error) { return f.open(name) }

func (f writeEveryFS) Create(name string) (types.File, error) { return f.create(name) }

func (f writeEveryFS) OpenFile(name string, flag int, perm fs.FileMode) (types.File, error) {
	return f.openFile(name, flag, perm)
}

func (f writeEveryFS) WriteFile(name string, data []byte, mode fs.FileMode) error {
	return f.writeFile(name, data, mode)
}

// writePerm is a mode a whole-file write asks for that neither the mode a seeded
// file already holds nor the perm the store's Create asks for can be mistaken
// for.
const writePerm = fs.FileMode(0o600)

func TestWriteFile(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		wrap  func(*store) fs.FS
		seed  func(*store)
		file  string
		data  string
		mode  fs.FileMode
		short string
		fail  string
		// wantWrites counts the calls of a native WriteFileFS, and wantOpens and
		// wantCloses the handles the emulation took, so a test can tell which arm
		// answered as well as what it answered.
		wantWrites int
		wantOpens  int
		wantCloses int
		wantErr    string
		wantFiles  map[string]string
		wantPerms  map[string]fs.FileMode
		wantDirs   []string
	}{
		{
			// A file system that stores a completed file knows better than a standin
			// how to do it, so its own method wins even though both emulations are
			// available. The mode recorded with the call is the one the request
			// carried, which the arm built from Create could never have carried.
			name:       "native WriteFileFS wins over the emulation",
			wrap:       func(s *store) fs.FS { return writeEveryFS{s} },
			file:       "b.txt",
			data:       "beta",
			mode:       writePerm,
			wantWrites: 1,
			wantFiles:  map[string]string{"b.txt": "beta"},
			wantPerms:  map[string]fs.FileMode{"b.txt": writePerm},
		},
		{
			// A refusal of a name by a file system that writes whole files itself is
			// its own answer: the emulation does not get a second try at it.
			name:       "a directory is refused by the native method",
			wrap:       func(s *store) fs.FS { return writeEveryFS{s} },
			seed:       seedDirD,
			file:       "d",
			data:       "beta",
			mode:       writePerm,
			wantWrites: 1,
			wantErr:    "invalid argument",
			wantFiles:  map[string]string{},
			wantDirs:   []string{"d"},
		},
		{
			name:       "emulated from OpenFileFS",
			wrap:       func(s *store) fs.FS { return openFileFS{s} },
			file:       "b.txt",
			data:       "beta",
			mode:       writePerm,
			wantOpens:  1,
			wantCloses: 1,
			wantFiles:  map[string]string{"b.txt": "beta"},
			wantPerms:  map[string]fs.FileMode{"b.txt": writePerm},
		},
		{
			// OpenFile carries the mode a new file is asked for and Create does not,
			// so it is the method the emulation has to reach first: the perm the file
			// comes out with can only have come from OpenFile.
			name:       "emulated from OpenFileFS when Create is available too",
			wrap:       func(s *store) fs.FS { return createOpenFS{s} },
			file:       "b.txt",
			data:       "beta",
			mode:       writePerm,
			wantOpens:  1,
			wantCloses: 1,
			wantFiles:  map[string]string{"b.txt": "beta"},
			wantPerms:  map[string]fs.FileMode{"b.txt": writePerm},
		},
		{
			// Create has no permission argument, so the mode cannot reach a file made
			// this way: the store's Create asks for perm 0, which is what tells this
			// arm from the one above.
			name:       "emulated from CreateFileFS, which cannot carry the mode",
			wrap:       func(s *store) fs.FS { return createFS{s} },
			file:       "b.txt",
			data:       "beta",
			mode:       writePerm,
			wantOpens:  1,
			wantCloses: 1,
			wantFiles:  map[string]string{"b.txt": "beta"},
			wantPerms:  map[string]fs.FileMode{"b.txt": 0},
		},
		{
			// What the write writes is the whole content, not what is added to what
			// is already there, which is what the truncate in the flags is for. The
			// mode the file already had is left alone, since new content says nothing
			// about who may read it.
			name:       "an existing file is replaced",
			wrap:       func(s *store) fs.FS { return openFileFS{s} },
			seed:       seedAlpha,
			file:       "a.txt",
			data:       "beta",
			mode:       writePerm,
			wantOpens:  1,
			wantCloses: 1,
			wantFiles:  map[string]string{"a.txt": "beta"},
			wantPerms:  map[string]fs.FileMode{"a.txt": filePerm},
		},
		{
			// No bytes at all is still a file the caller named.
			name:       "no content still makes a file",
			wrap:       func(s *store) fs.FS { return openFileFS{s} },
			file:       "empty.txt",
			mode:       writePerm,
			wantOpens:  1,
			wantCloses: 1,
			wantFiles:  map[string]string{"empty.txt": ""},
			wantPerms:  map[string]fs.FileMode{"empty.txt": writePerm},
		},
		{
			name:       "a file below a directory that is there",
			wrap:       func(s *store) fs.FS { return openFileFS{s} },
			seed:       seedDirD,
			file:       "d/b.txt",
			data:       "beta",
			mode:       writePerm,
			wantOpens:  1,
			wantCloses: 1,
			wantFiles:  map[string]string{"d/b.txt": "beta"},
			wantPerms:  map[string]fs.FileMode{"d/b.txt": writePerm},
			wantDirs:   []string{"d"},
		},
		{
			// The emulation does not build the levels above a name, whatever the file
			// system it stands in for does about them: what comes back is what that
			// file system says.
			name:      "a missing parent is the file system's to answer",
			wrap:      func(s *store) fs.FS { return openFileFS{s} },
			file:      "d/b.txt",
			data:      "beta",
			mode:      writePerm,
			wantErr:   "file does not exist",
			wantFiles: map[string]string{},
		},
		{
			name:      "a name that holds a directory is refused",
			wrap:      func(s *store) fs.FS { return openFileFS{s} },
			seed:      seedDirD,
			file:      "d",
			data:      "beta",
			mode:      writePerm,
			wantErr:   "invalid argument",
			wantFiles: map[string]string{},
			wantDirs:  []string{"d"},
		},
		{
			name:      "a name that holds a directory is refused through Create",
			wrap:      func(s *store) fs.FS { return createFS{s} },
			seed:      seedDirD,
			file:      "d",
			data:      "beta",
			mode:      writePerm,
			wantErr:   "invalid argument",
			wantFiles: map[string]string{},
			wantDirs:  []string{"d"},
		},
		{
			// The root is a directory, so there is no file to write: the refusal
			// comes before the file system is asked, which is why a file system with
			// no way to write a file at all reports the name rather than the
			// missing capability.
			name:      "the root is refused before the file system is asked",
			wrap:      func(s *store) fs.FS { return plainFS{s} },
			file:      pathutil.CwdPath,
			data:      "beta",
			mode:      writePerm,
			wantErr:   "the root is a directory",
			wantFiles: map[string]string{},
		},
		{
			name:      "a file system with nothing to write a file with",
			wrap:      func(s *store) fs.FS { return plainFS{s} },
			file:      "b.txt",
			data:      "beta",
			mode:      writePerm,
			wantErr:   `does not support "writefile"`,
			wantFiles: map[string]string{},
		},
		{
			// A handle that took some of the content and no more is a broken handle:
			// the caller asked for all of data to be stored, so the write counts as
			// failed, and the handle is closed all the same.
			name:       "a handle that writes short fails the write",
			wrap:       func(s *store) fs.FS { return openFileFS{s} },
			file:       "b.txt",
			data:       "beta",
			mode:       writePerm,
			short:      "b.txt",
			wantOpens:  1,
			wantCloses: 1,
			wantErr:    "wrote 2 of 4 bytes",
			wantFiles:  map[string]string{"b.txt": "be"},
			wantPerms:  map[string]fs.FileMode{"b.txt": writePerm},
		},
		{
			// A write that fails outright reaches the caller as the file system
			// reported it, and the handle it failed through is closed.
			name:       "a handle that cannot write fails the write",
			wrap:       func(s *store) fs.FS { return openFileFS{s} },
			file:       "b.txt",
			data:       "beta",
			mode:       writePerm,
			fail:       "b.txt",
			wantOpens:  1,
			wantCloses: 1,
			wantErr:    "no space left on device",
			wantFiles:  map[string]string{"b.txt": ""},
			wantPerms:  map[string]fs.FileMode{"b.txt": writePerm},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			if tc.seed != nil {
				tc.seed(s)
			}
			s.shortWriteOn = tc.short
			s.writeErrOn = tc.fail

			err := WriteFile(tc.wrap(s), tc.file, []byte(tc.data), tc.mode)
			checkWantErr(t, "WriteFile", err, tc.wantErr)

			if len(s.writeCalls) != tc.wantWrites {
				t.Errorf("WriteFile called the native method %d times, want %d", len(s.writeCalls), tc.wantWrites)
			}
			if tc.wantWrites == 1 {
				got := s.writeCalls[0]
				if got.name != tc.file || got.data != tc.data || got.mode != tc.mode {
					t.Errorf("native WriteFile was called with %+v, want {%s %s %v}", got, tc.file, tc.data, tc.mode)
				}
			}
			if len(s.files) != len(tc.wantFiles) {
				t.Errorf("WriteFile(%q) left %d files (%v), want %d (%v)", tc.file,
					len(s.files), slices.Sorted(maps.Keys(s.files)),
					len(tc.wantFiles), slices.Sorted(maps.Keys(tc.wantFiles)))
			}
			for name, want := range tc.wantFiles {
				if got := string(s.files[name]); got != want {
					t.Errorf("WriteFile(%q) left %q in %q, want %q", tc.file, got, name, want)
				}
			}
			for name, want := range tc.wantPerms {
				if got := s.perms[name]; got != want {
					t.Errorf("WriteFile(%q) perm for %q = %v, want %v", tc.file, name, got, want)
				}
			}
			if dirs := createdDirs(s); !reflect.DeepEqual(dirs, tc.wantDirs) {
				t.Errorf("WriteFile(%q) left directories %v, want %v", tc.file, dirs, tc.wantDirs)
			}
			if s.openCalls != tc.wantOpens {
				t.Errorf("WriteFile(%q) opened %d handles, want %d", tc.file, s.openCalls, tc.wantOpens)
			}
			if s.closeCalls != tc.wantCloses {
				t.Errorf("WriteFile(%q) closed %d handles, want %d", tc.file, s.closeCalls, tc.wantCloses)
			}
		})
	}
}

// TestWriteFileNativeFailure checks that what a file system's own whole-file
// write reports reaches the caller unchanged, since the reason a write failed is
// the reason the caller has to answer.
func TestWriteFileNativeFailure(t *testing.T) {
	t.Parallel()

	s := newStore()
	if err := WriteFile(writeFileFS{s}, "a.txt", []byte("alpha"), filePerm); err != nil {
		t.Fatal(err)
	}
	if got := string(s.files["a.txt"]); got != "alpha" {
		t.Fatalf("WriteFile left %q in a.txt, want %q", got, "alpha")
	}

	// The store's own WriteFile refuses the name of a file it holds no parent
	// directory for, which is the answer the caller has to see.
	if err := WriteFile(writeFileFS{s}, "d/b.txt", []byte("beta"), writePerm); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("WriteFile below a missing directory = %v, want an error wrapping fs.ErrNotExist", err)
	}
}
