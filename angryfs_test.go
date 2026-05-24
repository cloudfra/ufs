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

// TestAngryFSOperations verifies that every FS operation on an angryFS returns
// fs.ErrInvalid regardless of the path.
func TestAngryFSOperations(t *testing.T) {
	ops := []struct {
		name  string
		paths []string
		run   func(FS, string) error
	}{
		{"Open", testassetFilenameList, func(f FS, p string) error { _, err := f.Open(p); return err }},
		{"ReadFile", testassetFilenameList, func(f FS, p string) error { _, err := f.ReadFile(p); return err }},
		{"ReadLink", testassetFilenameList, func(f FS, p string) error { _, err := f.ReadLink(p); return err }},
		{"Lstat", testassetFilenameList, func(f FS, p string) error { _, err := f.Lstat(p); return err }},
		{"Stat", testassetFilenameList, func(f FS, p string) error { _, err := f.Stat(p); return err }},
		{"Create", testassetCreateFileList, func(f FS, p string) error { _, err := f.Create(p); return err }},
		{"MkdirAll", []string{"a", "a/b", "a/b/c", "abc", "null"}, func(f FS, p string) error { return f.MkdirAll(p, fs.ModePerm) }},
		{"ReadDir", toMapKeys(testassetDirList), func(f FS, p string) error { _, err := f.ReadDir(p); return err }},
	}
	for _, op := range ops {
		for _, path := range op.paths {
			t.Run(op.name+"/"+path, func(t *testing.T) {
				fsys := mustAngryFS(t)
				if err := op.run(fsys, path); !errors.Is(err, fs.ErrInvalid) {
					t.Errorf("%s(%q) = %v, want %v", op.name, path, err, fs.ErrInvalid)
				}
			})
		}
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
	if got := fsys.String(); got != angryFSPrefix {
		t.Errorf("String() got: %q, want %q", got, angryFSPrefix)
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
	fsys, err := newAngryFS(angryFSPrefix)
	if err != nil {
		tb.Fatalf("newAngryFS() returned error, %s", err)
	}
	return fsys
}
