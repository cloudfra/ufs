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
	"io/fs"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
)

// copyCall records one native CopyFile call.
type copyCall struct {
	srcPath string
	dstPath string
}

// copy moves the content the way a native CopyFileFS would and records that it
// was asked, so a test can tell the native path from the emulation by the handle
// counters, which stay at zero.
func (s *store) copy(srcPath, dstPath string) error {
	s.copyCalls = append(s.copyCalls, copyCall{srcPath: srcPath, dstPath: dstPath})
	data, ok := s.files[srcPath]
	if !ok {
		return &fs.PathError{Op: "copyfile", Path: srcPath, Err: fs.ErrNotExist}
	}
	if _, ok := s.files[dstPath]; ok {
		return &fs.PathError{Op: "copyfile", Path: dstPath, Err: fs.ErrExist}
	}
	s.files[dstPath] = append([]byte(nil), data...)
	s.perms[dstPath] = s.perms[srcPath]
	return nil
}

// copyFS implements [types.CopyFileFS] and nothing else.
type copyFS struct {
	*store
}

func (f copyFS) Open(name string) (fs.File, error) {
	return f.open(name)
}

func (f copyFS) CopyFile(srcPath, dstPath string) error {
	return f.copy(srcPath, dstPath)
}

// copyCreateFS implements [types.CopyFileFS] and [types.CreateFileFS], so the
// native method must win over the emulation.
type copyCreateFS struct {
	*store
}

func (f copyCreateFS) Open(name string) (fs.File, error) {
	return f.open(name)
}

func (f copyCreateFS) Create(name string) (types.File, error) {
	return f.create(name)
}

func (f copyCreateFS) CopyFile(srcPath, dstPath string) error {
	return f.copy(srcPath, dstPath)
}

// createOpenFS implements both [types.CreateFileFS] and [types.OpenFileFS], so
// the Create arm must win: it is the one every write file system is required to
// have, and the one its own Create implementation is built on.
type createOpenFS struct {
	*store
}

func (f createOpenFS) Open(name string) (fs.File, error) {
	return f.open(name)
}

func (f createOpenFS) Create(name string) (types.File, error) {
	return f.create(name)
}

func (f createOpenFS) OpenFile(name string, flag int, perm fs.FileMode) (types.File, error) {
	return f.openFile(name, flag, perm)
}

// unqueryableCreateFS implements [types.CreateFileFS] but cannot be queried:
// every open fails with something other than [fs.ErrNotExist], so the stat that
// guards the destination reports that failure rather than a free overwrite.
type unqueryableCreateFS struct {
	*store
}

func (f unqueryableCreateFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
}

func (f unqueryableCreateFS) Create(name string) (types.File, error) {
	return f.create(name)
}

func TestCopyFile(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		wrap       func(*store) fs.FS
		seed       func(*store)
		shortWrite string
		writeErr   string
		srcPath    string
		dstPath    string
		wantErr    string
		wantFiles  map[string]string
		wantPerms  map[string]fs.FileMode
		wantOpens  int
		wantCloses int
		wantCopies []copyCall
	}{
		{
			// The native method keeps whatever efficiency the back end has, so it
			// must win over the emulation even when the emulation is available.
			name: "native CopyFileFS wins over the emulation",
			wrap: func(s *store) fs.FS {
				return copyFS{s}
			},
			seed:       seedAlpha,
			srcPath:    "a.txt",
			dstPath:    "b.txt",
			wantFiles:  map[string]string{"a.txt": "alpha", "b.txt": "alpha"},
			wantPerms:  map[string]fs.FileMode{"a.txt": filePerm, "b.txt": filePerm},
			wantCopies: []copyCall{{srcPath: "a.txt", dstPath: "b.txt"}},
		},
		{
			name: "native CopyFileFS wins over CreateFileFS",
			wrap: func(s *store) fs.FS {
				return copyCreateFS{s}
			},
			seed:       seedAlpha,
			srcPath:    "a.txt",
			dstPath:    "b.txt",
			wantFiles:  map[string]string{"a.txt": "alpha", "b.txt": "alpha"},
			wantCopies: []copyCall{{srcPath: "a.txt", dstPath: "b.txt"}},
		},
		{
			name: "emulated from OpenFileFS, which carries the mode",
			wrap: func(s *store) fs.FS {
				return openFileFS{s}
			},
			seed:       seedSrcMode,
			srcPath:    "a.txt",
			dstPath:    "b.txt",
			wantFiles:  map[string]string{"a.txt": "alpha", "b.txt": "alpha"},
			wantPerms:  map[string]fs.FileMode{"a.txt": srcMode, "b.txt": srcMode},
			wantOpens:  1,
			wantCloses: 2,
		},
		{
			// Create takes no permission bits, so only the content moves.
			name: "emulated from CreateFileFS",
			wrap: func(s *store) fs.FS {
				return createFS{s}
			},
			seed:       seedSrcMode,
			srcPath:    "a.txt",
			dstPath:    "b.txt",
			wantFiles:  map[string]string{"a.txt": "alpha", "b.txt": "alpha"},
			wantPerms:  map[string]fs.FileMode{"a.txt": srcMode},
			wantOpens:  1,
			wantCloses: 2,
		},
		{
			// A file system with both write interfaces is emulated through Create,
			// which every write file system must have. The zero perm of that arm
			// rather than the source's mode shows which one ran.
			name: "CreateFileFS is preferred over OpenFileFS",
			wrap: func(s *store) fs.FS {
				return createOpenFS{s}
			},
			seed:       seedSrcMode,
			srcPath:    "a.txt",
			dstPath:    "b.txt",
			wantFiles:  map[string]string{"a.txt": "alpha", "b.txt": "alpha"},
			wantPerms:  map[string]fs.FileMode{"a.txt": srcMode, "b.txt": 0},
			wantOpens:  1,
			wantCloses: 2,
		},
		{
			name: "an existing destination is refused by OpenFileFS",
			wrap: func(s *store) fs.FS {
				return openFileFS{s}
			},
			seed:       seedAlphaBeta,
			srcPath:    "a.txt",
			dstPath:    "b.txt",
			wantErr:    "file already exists",
			wantFiles:  map[string]string{"a.txt": "alpha", "b.txt": "beta"},
			wantCloses: 1,
		},
		{
			name: "an existing destination is refused by CreateFileFS",
			wrap: func(s *store) fs.FS {
				return createFS{s}
			},
			seed:       seedAlphaBeta,
			srcPath:    "a.txt",
			dstPath:    "b.txt",
			wantErr:    "file already exists",
			wantFiles:  map[string]string{"a.txt": "alpha", "b.txt": "beta"},
			wantCloses: 1,
		},
		{
			// The destination exists because it is the source, so the copy is
			// refused instead of truncating the file it was given.
			name: "copying a file onto itself is refused",
			wrap: func(s *store) fs.FS {
				return createFS{s}
			},
			seed:       seedAlpha,
			srcPath:    "a.txt",
			dstPath:    "a.txt",
			wantErr:    "file already exists",
			wantFiles:  map[string]string{"a.txt": "alpha"},
			wantCloses: 1,
		},
		{
			name: "a missing source reports fs.ErrNotExist",
			wrap: func(s *store) fs.FS {
				return openFileFS{s}
			},
			srcPath:   "a.txt",
			dstPath:   "b.txt",
			wantErr:   "file does not exist",
			wantFiles: map[string]string{},
		},
		{
			name: "a missing source on the Create path reports fs.ErrNotExist",
			wrap: func(s *store) fs.FS {
				return createFS{s}
			},
			srcPath:   "a.txt",
			dstPath:   "b.txt",
			wantErr:   "file does not exist",
			wantFiles: map[string]string{},
		},
		{
			// The source is checked before the destination is created, so a
			// directory does not leave an empty file behind.
			name: "a directory source is refused",
			wrap: func(s *store) fs.FS {
				return openFileFS{s}
			},
			seed: func(s *store) {
				s.seedDir("d")
			},
			srcPath:   "d",
			dstPath:   "d-copy",
			wantErr:   "is not a file",
			wantFiles: map[string]string{},
		},
		{
			name: "no write interface at all",
			wrap: func(s *store) fs.FS {
				return plainFS{s}
			},
			seed:      seedAlpha,
			srcPath:   "a.txt",
			dstPath:   "b.txt",
			wantErr:   `does not support "copyfile"`,
			wantFiles: map[string]string{"a.txt": "alpha"},
		},
		{
			name: "the root is refused",
			wrap: func(s *store) fs.FS {
				return createFS{s}
			},
			seed:      seedAlpha,
			srcPath:   ".",
			dstPath:   "b.txt",
			wantErr:   "the root is a directory",
			wantFiles: map[string]string{"a.txt": "alpha"},
		},
		{
			name: "an invalid path is refused",
			wrap: func(s *store) fs.FS {
				return createFS{s}
			},
			seed:      seedAlpha,
			srcPath:   "a.txt",
			dstPath:   "../escape",
			wantErr:   "is not a valid path",
			wantFiles: map[string]string{"a.txt": "alpha"},
		},
		{
			// A destination that cannot be queried is not a license to overwrite
			// it, and nothing may be created for it.
			name: "a destination that cannot be queried is not overwritten",
			wrap: func(s *store) fs.FS {
				return unqueryableCreateFS{s}
			},
			seed:      seedAlpha,
			srcPath:   "a.txt",
			dstPath:   "b.txt",
			wantErr:   "permission denied",
			wantFiles: map[string]string{"a.txt": "alpha"},
		},
		{
			// A partial copy is a failed copy, but both handles must still close.
			name: "a write failure closes both handles",
			wrap: func(s *store) fs.FS {
				return createFS{s}
			},
			seed:       seedAlpha,
			writeErr:   "b.txt",
			srcPath:    "a.txt",
			dstPath:    "b.txt",
			wantErr:    "no space left on device",
			wantFiles:  map[string]string{"a.txt": "alpha", "b.txt": ""},
			wantOpens:  1,
			wantCloses: 2,
		},
		{
			name: "a short write is an error",
			wrap: func(s *store) fs.FS {
				return openFileFS{s}
			},
			seed:       seedAlpha,
			shortWrite: "b.txt",
			srcPath:    "a.txt",
			dstPath:    "b.txt",
			wantErr:    "short write",
			wantFiles:  map[string]string{"a.txt": "alpha", "b.txt": "al"},
			wantOpens:  1,
			wantCloses: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			if tc.seed != nil {
				tc.seed(s)
			}
			s.shortWriteOn = tc.shortWrite
			s.writeErrOn = tc.writeErr
			err := CopyFile(tc.wrap(s), tc.srcPath, tc.dstPath)
			checkWantErr(t, "CopyFile", err, tc.wantErr)

			if !reflect.DeepEqual(s.copyCalls, tc.wantCopies) {
				t.Errorf("CopyFile() called CopyFile with %v, want %v", s.copyCalls, tc.wantCopies)
			}
			if len(s.files) != len(tc.wantFiles) {
				t.Errorf("CopyFile() left %d files (%v), want %d (%v)",
					len(s.files), slices.Sorted(maps.Keys(s.files)),
					len(tc.wantFiles), slices.Sorted(maps.Keys(tc.wantFiles)))
			}
			for name, want := range tc.wantFiles {
				if got := string(s.files[name]); got != want {
					t.Errorf("CopyFile() wrote %q to %q, want %q", got, name, want)
				}
			}
			for name, want := range tc.wantPerms {
				if got := s.perms[name]; got != want {
					t.Errorf("CopyFile() perm for %q = %v, want %v", name, got, want)
				}
			}
			if s.openCalls != tc.wantOpens {
				t.Errorf("CopyFile() opened %d handles, want %d", s.openCalls, tc.wantOpens)
			}
			if s.closeCalls != tc.wantCloses {
				t.Errorf("CopyFile() closed %d handles, want %d", s.closeCalls, tc.wantCloses)
			}
		})
	}
}

// srcMode is a source mode that differs from the filePerm a seeded file would
// otherwise hold, so a carried-over mode is distinguishable from a default.
const srcMode = fs.FileMode(0o600)

// seedAlpha, seedAlphaBeta and seedSrcMode set up the files a copy starts from.
func seedAlpha(s *store) {
	s.seedFile("a.txt", "alpha")
}

func seedAlphaBeta(s *store) {
	s.seedFile("a.txt", "alpha")
	s.seedFile("b.txt", "beta")
}

func seedSrcMode(s *store) {
	s.seedFile("a.txt", "alpha")
	s.perms["a.txt"] = srcMode
}

func TestCopyFileLargeContent(t *testing.T) {
	t.Parallel()

	// Bigger than the buffer io.Copy allocates, so the copy cannot pass by
	// fitting in a single read.
	content := make([]byte, 128*1024)
	for i := range content {
		content[i] = byte(i)
	}

	for name, wrap := range map[string]func(*store) fs.FS{
		"OpenFileFS": func(s *store) fs.FS {
			return openFileFS{s}
		},
		"CreateFileFS": func(s *store) fs.FS {
			return createFS{s}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			s.seedFile("a.txt", "seed")
			s.files["a.txt"] = content
			if err := CopyFile(wrap(s), "a.txt", "b.txt"); err != nil {
				t.Fatalf("CopyFile() = %v, want nil", err)
			}
			if !reflect.DeepEqual(s.files["b.txt"], content) {
				t.Errorf("CopyFile() wrote %d bytes to b.txt, want the %d bytes of the source",
					len(s.files["b.txt"]), len(content))
			}
			if s.closeCalls != 2 {
				t.Errorf("CopyFile() closed %d handles, want 2", s.closeCalls)
			}
		})
	}
}
