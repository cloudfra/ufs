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
	"io/fs"
	"strings"
	"testing"
)

func TestIsAngryFSUri(t *testing.T) {
	testCases := []struct {
		name string
		want bool
	}{
		{name: "angry:", want: true},
		{name: "angry://", want: true},
		{name: "angryfs://", want: false},
		{name: "mem://", want: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isAngryFSUri(tc.name)
			if got != tc.want {
				t.Errorf("got: %t, want: %t", got, tc.want)
			}
		})
	}
}

func TestNewAngryFS(t *testing.T) {
	fsys := mustAngryFS(t)
	if fsys == nil {
		t.Fatal("fsys is nil")
	}
}

// TestAngryFSOperations verifies that every FS read/write operation on
// angryFS returns fs.ErrInvalid regardless of the path argument.
func TestAngryFSOperations(t *testing.T) {
	dirNames := make([]string, 0, len(testassetDirList))
	for name := range testassetDirList {
		dirNames = append(dirNames, name)
	}

	tests := []struct {
		name string
		tcs  []string
		op   func(fsys FS, tc string) error
	}{
		{"Open", testassetFilenameList, func(fsys FS, tc string) error {
			_, err := fsys.Open(tc)
			return err
		}},
		{"ReadFile", testassetFilenameList, func(fsys FS, tc string) error {
			_, err := fsys.ReadFile(tc)
			return err
		}},
		{"ReadLink", testassetFilenameList, func(fsys FS, tc string) error {
			_, err := fsys.ReadLink(tc)
			return err
		}},
		{"Lstat", testassetFilenameList, func(fsys FS, tc string) error {
			_, err := fsys.Lstat(tc)
			return err
		}},
		{"ReadDir", dirNames, func(fsys FS, tc string) error {
			_, err := fsys.ReadDir(tc)
			return err
		}},
		{"MkdirAll", []string{"a", "a/b", "a/b/c", "abc", "null"}, func(fsys FS, tc string) error {
			return fsys.MkdirAll(tc, fs.ModePerm)
		}},
		{"Create", testassetCreateFileList, func(fsys FS, tc string) error {
			_, err := fsys.Create(tc)
			return err
		}},
		{"Stat", testassetFilenameList, func(fsys FS, tc string) error {
			_, err := fsys.Stat(tc)
			return err
		}},
		{"Remove", testassetFilenameList, func(fsys FS, tc string) error {
			return fsys.Remove(tc)
		}},
		{"RemoveAll", append(testassetFilenameList, dirNames...), func(fsys FS, tc string) error {
			return fsys.RemoveAll(tc)
		}},
	}

	for _, group := range tests {
		t.Run(group.name, func(t *testing.T) {
			for _, tc := range group.tcs {
				t.Run(tc, func(t *testing.T) {
					fsys := mustAngryFS(t)
					if err := group.op(fsys, tc); !errors.Is(err, fs.ErrInvalid) {
						t.Errorf("%s(%q) = %v, want %v", group.name, tc, err, fs.ErrInvalid)
					}
				})
			}
		})
	}
}

func TestAngryFSClose(t *testing.T) {
	fsys := mustAngryFS(t)
	if err := fsys.Close(); err != fs.ErrInvalid {
		t.Errorf("Close() = %v, want %v", err, fs.ErrInvalid)
	}
}

func TestAngryFSGlob(t *testing.T) {
	fsys := mustAngryFS(t)
	glob, ok := fsys.(fs.GlobFS)
	if !ok {
		t.Fatal("angryFS does not implement fs.GlobFS")
	}
	if _, err := glob.Glob("*.txt"); err != fs.ErrInvalid {
		t.Errorf("Glob() = %v, want %v", err, fs.ErrInvalid)
	}
}

func TestAngryFSString(t *testing.T) {
	fsys := mustAngryFS(t)
	if got := fsys.String(); !strings.Contains(got, angryFSPrefix) {
		t.Errorf("String() should contain %q, got: %q", angryFSPrefix, got)
	}
}

func TestAngryFSStatInvalid(t *testing.T) {
	for _, tc := range []string{"/absolute", "../parent", "bad/../path"} {
		t.Run(tc, func(t *testing.T) {
			fsys := mustAngryFS(t)
			if _, err := fsys.Stat(tc); err == nil {
				t.Errorf("Stat(%q) = nil error, want error", tc)
			}
		})
	}
}

func mustAngryFS(tb testing.TB) FS {
	fsys, err := newAngryFS(tb.Context(), angryFSPrefix)
	if err != nil {
		tb.Fatalf("newAngryFS() returned error, %s", err)
	}
	return fsys
}
