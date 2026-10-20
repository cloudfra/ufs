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

func TestBatchCreate(t *testing.T) {
	t.Parallel()

	twoFiles := []types.BatchFile{
		{Name: "a", Data: []byte("alpha"), Perm: filePerm},
		{Name: "d/b", Data: []byte("beta"), Perm: filePerm},
	}

	testCases := []struct {
		name       string
		wrap       func(*store) fs.FS
		seed       func(*store)
		shortWrite string
		writeErr   string
		files      []types.BatchFile
		wantErr    string
		wantFiles  map[string]string
		wantPerms  map[string]fs.FileMode
		wantDirs   []string
		wantMkdirs []string
		wantOpens  int
		wantCloses int
		wantBatch  [][]string
	}{
		{
			name:      "native BatchCreateFS wins over the emulation",
			wrap:      func(s *store) fs.FS { return batchFS{s} },
			seed:      func(s *store) { s.seedDir("d") },
			files:     twoFiles,
			wantDirs:  []string{"d"},
			wantBatch: [][]string{{"a", "d/b"}},
		},
		{
			name:       "emulated from OpenFileFS, which applies Perm",
			wrap:       func(s *store) fs.FS { return openFileFS{s} },
			seed:       func(s *store) { s.seedDir("d") },
			files:      twoFiles,
			wantFiles:  map[string]string{"a": "alpha", "d/b": "beta"},
			wantPerms:  map[string]fs.FileMode{"a": filePerm, "d/b": filePerm},
			wantDirs:   []string{"d"},
			wantOpens:  2,
			wantCloses: 2,
		},
		{
			name:       "emulated from CreateFileFS, which ignores Perm",
			wrap:       func(s *store) fs.FS { return createFS{s} },
			seed:       func(s *store) { s.seedDir("d") },
			files:      twoFiles,
			wantFiles:  map[string]string{"a": "alpha", "d/b": "beta"},
			wantPerms:  map[string]fs.FileMode{"a": 0, "d/b": 0},
			wantDirs:   []string{"d"},
			wantOpens:  2,
			wantCloses: 2,
		},
		{
			// A missing O_CREATE or O_TRUNC in the emulated flags shows up
			// here: the write would land after the old content instead of
			// replacing it.
			name:       "a new file is created and an existing one truncated",
			wrap:       func(s *store) fs.FS { return openFileFS{s} },
			seed:       func(s *store) { s.seedFile("c", "content that is longer than the new data") },
			files:      []types.BatchFile{{Name: "c", Data: []byte("new"), Perm: 0o600}},
			wantFiles:  map[string]string{"c": "new"},
			wantPerms:  map[string]fs.FileMode{"c": filePerm},
			wantOpens:  1,
			wantCloses: 1,
		},
		{
			name:       "an empty batch does nothing",
			wrap:       func(s *store) fs.FS { return openFileFS{s} },
			files:      nil,
			wantOpens:  0,
			wantCloses: 0,
		},
		{
			name:       "a missing parent is created",
			wrap:       func(s *store) fs.FS { return openFileMkdirAllFS{s} },
			files:      []types.BatchFile{{Name: "d/b", Data: []byte("beta"), Perm: filePerm}},
			wantFiles:  map[string]string{"d/b": "beta"},
			wantPerms:  map[string]fs.FileMode{"d/b": filePerm},
			wantDirs:   []string{"d"},
			wantOpens:  1,
			wantCloses: 1,
		},
		{
			name:       "a deep missing parent chain is created top down",
			wrap:       func(s *store) fs.FS { return createMkdirFS{s} },
			files:      []types.BatchFile{{Name: "d/e/f", Data: []byte("beta")}},
			wantFiles:  map[string]string{"d/e/f": "beta"},
			wantPerms:  map[string]fs.FileMode{"d/e/f": 0},
			wantDirs:   []string{"d", "d/e"},
			wantMkdirs: []string{"d", "d/e"},
			wantOpens:  1,
			wantCloses: 1,
		},
		{
			// Only a file system that can make directories gets a parent for
			// free, so the unimplemented error has to reach the caller.
			name:      "a file system that cannot make directories needs the parent",
			wrap:      func(s *store) fs.FS { return openFileFS{s} },
			files:     []types.BatchFile{{Name: "d/b", Data: []byte("beta"), Perm: filePerm}},
			wantErr:   `does not support "mkdirall"`,
			wantOpens: 0,
		},
		{
			name:      "the Create path needs a mkdir interface for a missing parent",
			wrap:      func(s *store) fs.FS { return createFS{s} },
			files:     []types.BatchFile{{Name: "d/b", Data: []byte("beta")}},
			wantErr:   `does not support "mkdirall"`,
			wantOpens: 0,
		},
		{
			// The parent is there, so the failure belongs to the create
			// itself rather than to the emulation's MkdirAll.
			name:       "an entry named like an existing directory fails",
			wrap:       func(s *store) fs.FS { return openFileMkdirAllFS{s} },
			seed:       func(s *store) { s.seedDir("d") },
			files:      []types.BatchFile{{Name: "d", Data: []byte("beta"), Perm: filePerm}},
			wantErr:    "invalid argument",
			wantDirs:   []string{"d"},
			wantOpens:  0,
			wantCloses: 0,
		},
		{
			name:       "the Create path fails on a name that is a directory",
			wrap:       func(s *store) fs.FS { return createMkdirFS{s} },
			seed:       func(s *store) { s.seedDir("d") },
			files:      []types.BatchFile{{Name: "d", Data: []byte("beta")}},
			wantErr:    "invalid argument",
			wantDirs:   []string{"d"},
			wantOpens:  0,
			wantCloses: 0,
		},
		{
			// The emulation writes one file at a time, so an earlier file
			// survives a later failure.
			name:       "an earlier file survives a later failure",
			wrap:       func(s *store) fs.FS { return openFileFS{s} },
			files:      twoFiles,
			wantErr:    `does not support "mkdirall"`,
			wantFiles:  map[string]string{"a": "alpha"},
			wantPerms:  map[string]fs.FileMode{"a": filePerm},
			wantOpens:  1,
			wantCloses: 1,
		},
		{
			name:       "a short write is an error and still closes the handle",
			wrap:       func(s *store) fs.FS { return createFS{s} },
			shortWrite: "a",
			files:      []types.BatchFile{{Name: "a", Data: []byte("12345")}},
			wantErr:    `wrote 2 of 5 bytes`,
			wantFiles:  map[string]string{"a": "12"},
			wantPerms:  map[string]fs.FileMode{"a": 0},
			wantOpens:  1,
			wantCloses: 1,
		},
		{
			name:       "a failing write closes the handle and stops the batch",
			wrap:       func(s *store) fs.FS { return openFileFS{s} },
			writeErr:   "a",
			files:      twoFiles,
			wantErr:    "no space left on device",
			wantFiles:  map[string]string{"a": ""},
			wantPerms:  map[string]fs.FileMode{"a": filePerm},
			wantOpens:  1,
			wantCloses: 1,
		},
		{
			name:    "no create interface at all",
			wrap:    func(s *store) fs.FS { return plainFS{s} },
			files:   twoFiles,
			wantErr: `does not support "create"`,
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
			err := BatchCreate(tc.wrap(s), tc.files)
			checkWantErr(t, "BatchCreate", err, tc.wantErr)

			if !reflect.DeepEqual(s.batches, tc.wantBatch) {
				t.Errorf("BatchCreate() called BatchCreate with %v, want %v", s.batches, tc.wantBatch)
			}
			if len(s.files) != len(tc.wantFiles) {
				t.Errorf("BatchCreate() wrote %d files (%v), want %d (%v)",
					len(s.files), slices.Sorted(maps.Keys(s.files)),
					len(tc.wantFiles), slices.Sorted(maps.Keys(tc.wantFiles)))
			}
			for name, want := range tc.wantFiles {
				if got := string(s.files[name]); got != want {
					t.Errorf("BatchCreate() wrote %q to %q, want %q", got, name, want)
				}
			}
			for name, want := range tc.wantPerms {
				if got := s.perms[name]; got != want {
					t.Errorf("BatchCreate() perm for %q = %v, want %v", name, got, want)
				}
			}
			if dirs := createdDirs(s); !reflect.DeepEqual(dirs, tc.wantDirs) {
				t.Errorf("BatchCreate() created %v, want %v", dirs, tc.wantDirs)
			}
			if !reflect.DeepEqual(s.mkdirCalls, tc.wantMkdirs) {
				t.Errorf("BatchCreate() called Mkdir %v, want %v", s.mkdirCalls, tc.wantMkdirs)
			}
			if s.openCalls != tc.wantOpens {
				t.Errorf("BatchCreate() opened %d handles, want %d", s.openCalls, tc.wantOpens)
			}
			if s.closeCalls != tc.wantCloses {
				t.Errorf("BatchCreate() closed %d handles, want %d", s.closeCalls, tc.wantCloses)
			}
		})
	}
}
