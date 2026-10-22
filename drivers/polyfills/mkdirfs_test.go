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
	"reflect"
	"strings"
	"testing"
)

func TestMkdir(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		wrap          func(*store) fs.FS
		seed          func(*store)
		dir           string
		wantErr       string
		wantDirs      []string
		wantMkdirs    []string
		wantMkdirAlls []string
	}{
		{
			name:          "native MkdirFS wins over the emulation",
			wrap:          func(s *store) fs.FS { return mkdirBothFS{s} },
			seed:          func(s *store) { s.seedDir("a") },
			dir:           "a/b",
			wantDirs:      []string{"a", "a/b"},
			wantMkdirs:    []string{"a/b"},
			wantMkdirAlls: nil,
		},
		{
			name:          "emulated from MkdirAllFS",
			wrap:          func(s *store) fs.FS { return mkdirAllFS{s} },
			seed:          func(s *store) { s.seedDir("a") },
			dir:           "a/b",
			wantDirs:      []string{"a", "a/b"},
			wantMkdirAlls: []string{"a/b"},
		},
		{
			name:          "one level below the root",
			wrap:          func(s *store) fs.FS { return mkdirAllFS{s} },
			dir:           "a",
			wantDirs:      []string{"a"},
			wantMkdirAlls: []string{"a"},
		},
		{
			name:          "an existing directory is a no-op",
			wrap:          func(s *store) fs.FS { return mkdirAllFS{s} },
			seed:          func(s *store) { s.seedDir("a") },
			dir:           "a",
			wantDirs:      []string{"a"},
			wantMkdirAlls: nil,
		},
		{
			// MkdirAll creates every level, so the emulation must not reach it.
			name:          "a missing parent is not created",
			wrap:          func(s *store) fs.FS { return mkdirAllFS{s} },
			dir:           "a/b",
			wantErr:       `must be a directory to create "a/b"`,
			wantDirs:      nil,
			wantMkdirAlls: nil,
		},
		{
			name:       "no mkdir interface at all",
			wrap:       func(s *store) fs.FS { return plainFS{s} },
			dir:        "a",
			wantErr:    `does not support "mkdir"`,
			wantDirs:   nil,
			wantMkdirs: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			if tc.seed != nil {
				tc.seed(s)
			}
			err := Mkdir(tc.wrap(s), tc.dir, dirPerm)
			checkWantErr(t, "Mkdir", err, tc.wantErr)
			if dirs := createdDirs(s); !reflect.DeepEqual(dirs, tc.wantDirs) {
				t.Errorf("Mkdir(%q) created %v, want %v", tc.dir, dirs, tc.wantDirs)
			}
			if !reflect.DeepEqual(s.mkdirCalls, tc.wantMkdirs) {
				t.Errorf("Mkdir(%q) called Mkdir %v, want %v", tc.dir, s.mkdirCalls, tc.wantMkdirs)
			}
			if !reflect.DeepEqual(s.mkdirAllCalls, tc.wantMkdirAlls) {
				t.Errorf("Mkdir(%q) called MkdirAll %v, want %v", tc.dir, s.mkdirAllCalls, tc.wantMkdirAlls)
			}
		})
	}
}

func TestMkdirPerm(t *testing.T) {
	t.Parallel()

	s := newStore()
	if err := Mkdir(mkdirAllFS{s}, "a", dirPerm); err != nil {
		t.Fatalf("Mkdir() = %v, want nil", err)
	}
	if got := s.perms["a"]; got != dirPerm {
		t.Errorf("Mkdir() perm = %v, want %v", got, dirPerm)
	}
}

func TestMkdirAll(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		wrap          func(*store) fs.FS
		seed          func(*store)
		dir           string
		wantErr       string
		wantDirs      []string
		wantMkdirs    []string
		wantMkdirAlls []string
	}{
		{
			name:          "native MkdirAllFS wins over the emulation",
			wrap:          func(s *store) fs.FS { return mkdirBothFS{s} },
			seed:          func(s *store) { s.seedDir("a") },
			dir:           "a/b/c",
			wantDirs:      []string{"a", "a/b", "a/b/c"},
			wantMkdirs:    nil,
			wantMkdirAlls: []string{"a/b/c"},
		},
		{
			name:       "every missing level is created, top down",
			wrap:       func(s *store) fs.FS { return mkdirFS{s} },
			dir:        "a/b/c",
			wantDirs:   []string{"a", "a/b", "a/b/c"},
			wantMkdirs: []string{"a", "a/b", "a/b/c"},
		},
		{
			name:       "a deep chain",
			wrap:       func(s *store) fs.FS { return mkdirFS{s} },
			dir:        "a/b/c/d/e",
			wantDirs:   []string{"a", "a/b", "a/b/c", "a/b/c/d", "a/b/c/d/e"},
			wantMkdirs: []string{"a", "a/b", "a/b/c", "a/b/c/d", "a/b/c/d/e"},
		},
		{
			name:       "only the missing levels are created",
			wrap:       func(s *store) fs.FS { return mkdirFS{s} },
			seed:       func(s *store) { s.seedDir("a/b") },
			dir:        "a/b/c",
			wantDirs:   []string{"a", "a/b", "a/b/c"},
			wantMkdirs: []string{"a/b/c"},
		},
		{
			name:       "an existing directory is a no-op",
			wrap:       func(s *store) fs.FS { return mkdirFS{s} },
			seed:       func(s *store) { s.seedDir("a") },
			dir:        "a",
			wantDirs:   []string{"a"},
			wantMkdirs: nil,
		},
		{
			name:       "an ancestor that is a file is an error",
			wrap:       func(s *store) fs.FS { return mkdirFS{s} },
			seed:       func(s *store) { s.seedFile("a/b", "data") },
			dir:        "a/b/c",
			wantErr:    `"a/b" is not a directory`,
			wantDirs:   []string{"a"},
			wantMkdirs: nil,
		},
		{
			// The blocking ancestor is only found once the recursion has
			// walked past the levels that are missing.
			name:       "an ancestor found deeper in the recursion is an error",
			wrap:       func(s *store) fs.FS { return mkdirFS{s} },
			seed:       func(s *store) { s.seedFile("a/b/c", "data") },
			dir:        "a/b/c/d/e",
			wantErr:    `"a/b/c" is not a directory`,
			wantDirs:   []string{"a", "a/b"},
			wantMkdirs: nil,
		},
		{
			// Only a missing ancestor may be created, so any other stat
			// failure has to reach the caller.
			name:       "a stat that fails for another reason is passed through",
			wrap:       func(s *store) fs.FS { return brokenFS{s} },
			dir:        "a/b",
			wantErr:    "permission denied",
			wantDirs:   nil,
			wantMkdirs: nil,
		},
		{
			name:       "no mkdir interface at all",
			wrap:       func(s *store) fs.FS { return plainFS{s} },
			dir:        "a/b",
			wantErr:    `does not support "mkdirall"`,
			wantDirs:   nil,
			wantMkdirs: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			if tc.seed != nil {
				tc.seed(s)
			}
			err := MkdirAll(tc.wrap(s), tc.dir, dirPerm)
			checkWantErr(t, "MkdirAll", err, tc.wantErr)
			if dirs := createdDirs(s); !reflect.DeepEqual(dirs, tc.wantDirs) {
				t.Errorf("MkdirAll(%q) created %v, want %v", tc.dir, dirs, tc.wantDirs)
			}
			if !reflect.DeepEqual(s.mkdirCalls, tc.wantMkdirs) {
				t.Errorf("MkdirAll(%q) called Mkdir %v, want %v", tc.dir, s.mkdirCalls, tc.wantMkdirs)
			}
			if !reflect.DeepEqual(s.mkdirAllCalls, tc.wantMkdirAlls) {
				t.Errorf("MkdirAll(%q) called MkdirAll %v, want %v", tc.dir, s.mkdirAllCalls, tc.wantMkdirAlls)
			}
		})
	}
}

// TestMkdirAllPerm checks that the emulated path passes perm to every level it
// creates, not just the leaf.
func TestMkdirAllPerm(t *testing.T) {
	t.Parallel()

	s := newStore()
	if err := MkdirAll(mkdirFS{s}, "a/b/c", dirPerm); err != nil {
		t.Fatalf("MkdirAll() = %v, want nil", err)
	}
	for _, name := range []string{"a", "a/b", "a/b/c"} {
		if got := s.perms[name]; got != dirPerm {
			t.Errorf("MkdirAll() perm for %q = %v, want %v", name, got, dirPerm)
		}
	}
}

// checkWantErr fails the test unless the error matches wantErr, an empty
// wantErr meaning that no error is wanted.
func checkWantErr(t *testing.T, funcName string, err error, wantErr string) {
	t.Helper()
	if wantErr == "" {
		if err != nil {
			t.Errorf("%s() = %v, want nil", funcName, err)
		}
		return
	}
	if err == nil {
		t.Fatalf("%s() = nil, want an error containing %q", funcName, wantErr)
	}
	if !strings.Contains(err.Error(), wantErr) {
		t.Errorf("%s() = %v, want an error containing %q", funcName, err, wantErr)
	}
}
