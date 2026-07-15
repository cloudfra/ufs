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
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewBaseFSInvalid(t *testing.T) {
	_, err := newBaseFS(t.Context(), "unknown://surely-not-a-path-xyz")
	if err == nil {
		t.Error("newBaseFS(unknown://) succeeded, want error")
	}
	if _, ok := err.(*fs.PathError); !ok {
		t.Errorf("newBaseFS(unknown://) returned %T, want *fs.PathError", err)
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		uri               string
		wantType          string
		wantErr           bool
		nested            bool
		ignoreCloseErrors bool
	}{
		{
			uri:               "angry://",
			wantType:          reflect.TypeFor[*nullFS]().Name(),
			wantErr:           false,
			ignoreCloseErrors: true,
		},
		{
			uri:               "angry://",
			wantType:          reflect.TypeFor[*nullFS]().Name(),
			wantErr:           false,
			ignoreCloseErrors: true,
		},
		{
			uri:      "file://",
			wantType: reflect.TypeFor[*localFS]().Name(),
			wantErr:  false,
		},
		{
			uri:      cwdPath,
			wantType: reflect.TypeFor[*localFS]().Name(),
			wantErr:  false,
		},
		{
			uri:      "memory://",
			wantType: reflect.TypeFor[*memFS]().Name(),
			wantErr:  false,
		},
		{
			uri:      "memory://",
			wantType: reflect.TypeFor[*memFS]().Name(),
			wantErr:  false,
		},
		{
			uri:      "null://",
			wantType: reflect.TypeFor[*nullFS]().Name(),
			wantErr:  false,
		},
		{
			uri:      "null://",
			wantType: reflect.TypeFor[*nullFS]().Name(),
			wantErr:  false,
		},
		{
			uri:      "file:///?a=memory://",
			wantType: reflect.TypeFor[*nullFS]().Name(),
			wantErr:  false,
			nested:   true,
		},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("newBaseFS(%q)", tt.uri), func(t *testing.T) {
			if tt.nested {
				t.Skip("test case requires nestFS support")
			}
			got, err := newBaseFS(t.Context(), tt.uri)
			if got != nil {
				defer func() {
					if tt.ignoreCloseErrors {
						wantCloseError(t, got)()
					} else {
						validateClose(t, got)()
					}
				}()
			}

			if tt.wantErr {
				if err == nil {
					t.Errorf("getBaseFS(%q) = %q, want error", tt.uri, got)
				}
			} else {
				if err != nil {
					t.Errorf("getBaseFS(%q) = %q, want %q", tt.uri, err, tt.wantType)
				} else {
					gotTypeName := reflect.TypeOf(got).Name()
					if gotTypeName != tt.wantType {
						t.Errorf("getBaseFS(%q) = %q, want %q", tt.uri, got, tt.wantType)
					}
				}
			}
		})
		t.Run(fmt.Sprintf("New(%q)", tt.uri), func(t *testing.T) {
			got, err := New(t.Context(), tt.uri)
			if got != nil {
				defer func() {
					if tt.ignoreCloseErrors {
						wantCloseError(t, got)()
					} else {
						validateClose(t, got)()
					}
				}()
			}
			if tt.wantErr {
				if err == nil {
					t.Errorf("getBaseFS(%q) = %q, want error", tt.uri, got)
				}
			} else {
				if err != nil {
					t.Errorf("getBaseFS(%q) = %q, want %q", tt.uri, err, tt.wantType)
				} else {
					gotAsNestFS, ok := got.(*nestFS)
					if ok {
						gotTypeName := reflect.TypeOf(gotAsNestFS.fsys).Name()
						if gotTypeName != tt.wantType {
							t.Errorf("getBaseFS(%q) = %q, want %q", tt.uri, got, tt.wantType)
						}
					} else {
						t.Errorf("%q is not of type *nestFS", got)
					}
				}
			}
		})
	}
}

func TestCreateURI(t *testing.T) {
	tests := []struct {
		name              string
		mounts            map[string]string
		want              string
		ignoreCloseErrors bool
	}{
		{
			name:   "memory://",
			mounts: map[string]string{},
			want:   "memory:",
		},
		{
			name:              "angry://",
			mounts:            map[string]string{},
			want:              "angry:",
			ignoreCloseErrors: true,
		},
		{
			name:   "null://",
			mounts: map[string]string{},
			want:   "null:",
		},
		{
			name:   "file://",
			mounts: map[string]string{},
			want:   "file:",
		},
		{
			name: "file:///",
			mounts: map[string]string{
				"mounted/null":   "null://",
				"mounted/angry":  "angry://",
				"mounted/memory": "memory://",
			},
			want:              "file:///?mounted%2Fangry=angry%3A&mounted%2Fmemory=memory%3A&mounted%2Fnull=null%3A",
			ignoreCloseErrors: true,
		},
		{
			name: "file:///",
			mounts: map[string]string{
				"mounted/null":   "null://",
				"mounted/memory": "memory://",
			},
			want: "file:///?mounted%2Fmemory=memory%3A&mounted%2Fnull=null%3A",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CreateURI(tc.name, tc.mounts)
			if err != nil {
				t.Errorf("CreateURI(%q, %v) got error: %s", tc.name, tc.mounts, err)
			}
			if got != tc.want {
				t.Errorf("CreateURI(%q, %v) got: %q, want: %q", tc.name, tc.mounts, got, tc.want)
			}
			fsys, err := New(t.Context(), got)
			if err != nil {
				t.Fatalf("New(%q) got error: %s", got, err)
			}
			defer func() {
				if tc.ignoreCloseErrors {
					wantCloseError(t, fsys)()
				}
			}()

			gotTypeName := reflect.TypeOf(fsys).Name()
			wantType := reflect.TypeFor[*nestFS]().Name()
			if gotTypeName != wantType {
				t.Errorf("New(%q) = %q, want %q", got, fsys, wantType)
			}
		})
	}
}

// testAssetsArchivesDir is the path to the test archive files.
const testAssetsArchivesDir = "testing/testassets/archives"

// mustGetwd returns the working directory, fataling on error.
func mustGetwd(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

func TestFSBuilderBuildEmpty(t *testing.T) {
	t.Parallel()
	fsys, err := NewFSBuilder("").Build(t.Context())
	if err != nil {
		t.Fatalf("Build() = %v, want nil", err)
	}
	defer fsys.Close()
	if _, ok := fsys.(*nestFS); !ok {
		t.Errorf("Build() = %T, want *nestFS", fsys)
	}
}

func TestFSBuilderBuildURI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		root    string
		mounts  map[string]string
		wantURI string
	}{
		{
			name:    "memory no mounts",
			root:    "memory://",
			wantURI: "memory:",
		},
		{
			name:    "null no mounts",
			root:    "null://",
			wantURI: "null:",
		},
		{
			name:    "memory with null mount",
			root:    "memory://",
			mounts:  map[string]string{"data": "null://"},
			wantURI: "memory:?data=null%3A",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := NewFSBuilder(tc.root)
			for path, uri := range tc.mounts {
				b.Mount(path, uri)
			}
			got, err := b.BuildURI()
			if err != nil {
				t.Fatalf("BuildURI() = %v, want nil", err)
			}
			if got != tc.wantURI {
				t.Errorf("BuildURI() = %q, want %q", got, tc.wantURI)
			}
			fsys, err := New(t.Context(), got)
			if err != nil {
				t.Fatalf("New(%q) = %v, want nil", got, err)
			}
			defer fsys.Close()
		})
	}
}

func TestFSBuilderBuildURIEmpty(t *testing.T) {
	t.Parallel()
	uri, err := NewFSBuilder("").BuildURI()
	if err != nil {
		t.Fatalf("BuildURI() = %v, want nil", err)
	}
	fsys, err := New(t.Context(), uri)
	if err != nil {
		t.Fatalf("New(%q) = %v, want nil", uri, err)
	}
	defer fsys.Close()
}

func TestFSBuilderBuildURIWithFSMountErrors(t *testing.T) {
	t.Parallel()
	b := NewFSBuilder("memory://").MountFS("assets", NewEmbedFS("assets", embedTestFiles))
	_, err := b.BuildURI()
	if err == nil {
		t.Fatal("BuildURI() with MountFS = nil, want error")
	}
}

func TestFSBuilderMountFS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	embedFSys := NewEmbedFS("assets", embedTestFiles)
	fsys, err := NewFSBuilder("memory://").MountFS("assets", embedFSys).Build(ctx)
	if err != nil {
		t.Fatalf("Build() = %v, want nil", err)
	}
	defer fsys.Close()

	entries, err := fsys.ReadDir("assets/testing/testassets/files")
	if err != nil {
		t.Fatalf("ReadDir(assets/...) = %v, want nil", err)
	}
	if len(entries) == 0 {
		t.Error("ReadDir returned empty entries under embed mount, want non-empty")
	}
}

func TestFSBuilderMountFSWriteBlocked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	embedFSys := NewEmbedFS("assets", embedTestFiles)
	fsys, err := NewFSBuilder("memory://").MountFS("assets", embedFSys).Build(ctx)
	if err != nil {
		t.Fatalf("Build() = %v, want nil", err)
	}
	defer fsys.Close()

	_, err = fsys.Create("assets/newfile.txt")
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Create under embedFS mount = %v, want fs.ErrPermission", err)
	}
}

// containsEntry reports whether name appears in the slice of DirEntry names.
func containsEntry(entries []fs.DirEntry, name string) bool {
	for _, e := range entries {
		if e.Name() == name {
			return true
		}
	}
	return false
}

// TestCreateURIWithSiblingMounts verifies that CreateURI correctly builds a URI
// that layers a local directory alongside multiple archive mount points as siblings,
// and that New can open the resulting URI.
func TestCreateURIWithSiblingMounts(t *testing.T) {
	t.Parallel()
	wd := mustGetwd(t)

	tarGz := filepath.Join(wd, testAssetsArchivesDir, "testassets.tar.gz")
	zip := filepath.Join(wd, testAssetsArchivesDir, "single-testassets.zip")
	tar := filepath.Join(wd, testAssetsArchivesDir, "testassets.tar")
	filesDir := "file://" + filepath.Join(wd, testAssetsFilesDir)
	testassetsDir := "file://" + filepath.Join(wd, "testing/testassets")

	tests := []struct {
		name          string
		base          string
		mounts        map[string]string
		wantMountDirs []string
	}{
		{
			name: "files dir with two archive siblings",
			base: filesDir,
			mounts: map[string]string{
				"from-zip":    zip,
				"from-tar-gz": tarGz,
			},
			wantMountDirs: []string{"from-zip", "from-tar-gz"},
		},
		{
			name: "testassets dir with three archive format siblings",
			base: testassetsDir,
			mounts: map[string]string{
				"zip-content":    zip,
				"tar-gz-content": tarGz,
				"tar-content":    tar,
			},
			wantMountDirs: []string{"zip-content", "tar-gz-content", "tar-content"},
		},
		{
			name: "memory base with dir and archive siblings",
			base: "memory://",
			mounts: map[string]string{
				"files":   filesDir,
				"archive": tarGz,
			},
			wantMountDirs: []string{"files", "archive"},
		},
		{
			name: "null base with archive sibling",
			base: "null://",
			mounts: map[string]string{
				"data": tarGz,
			},
			wantMountDirs: []string{"data"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			uri, err := CreateURI(tt.base, tt.mounts)
			if err != nil {
				t.Fatalf("CreateURI(%q, ...) = %v, want nil", tt.base, err)
			}

			fsys, err := New(t.Context(), uri)
			if err != nil {
				t.Fatalf("New(%q) = %v, want nil", uri, err)
			}
			defer fsys.Close()

			if _, ok := fsys.(*nestFS); !ok {
				t.Errorf("New() = %T, want *nestFS", fsys)
			}

			entries, err := fsys.ReadDir(".")
			if err != nil {
				t.Fatalf("ReadDir('.') = %v", err)
			}
			for _, wantDir := range tt.wantMountDirs {
				if !containsEntry(entries, wantDir) {
					t.Errorf("ReadDir('.') missing mount point %q, got: %v", wantDir, dirEntryListToNames(entries))
				}
			}
		})
	}
}

// TestNewSiblingMountsAccess verifies file and directory access across all mount
// points in a FS built from CreateURI. Each case specifies a base FS, sibling
// archive or directory mounts, and a set of assertions:
//
//   - wantRootContains: entries that must appear in ReadDir(".")
//   - wantReadable: file paths that must be readable, each with an optional
//     substring that must appear in the content
//   - wantDirContains: directory paths mapped to child entry names that must be present
//   - wantEqualPaths: a group of file paths whose contents must all be identical
func TestNewSiblingMountsAccess(t *testing.T) {
	t.Parallel()
	wd := mustGetwd(t)
	archivesDir := filepath.Join(wd, testAssetsArchivesDir)
	filesBase := "file://" + filepath.Join(wd, testAssetsFilesDir)
	testassetsBase := "file://" + filepath.Join(wd, "testing/testassets")

	tests := []struct {
		name             string
		base             string
		mounts           map[string]string
		wantRootContains []string
		wantReadable     []struct{ path, substr string }
		wantDirContains  map[string][]string
		wantEqualPaths   []string
	}{
		{
			name: "files dir with zip and tar.gz siblings",
			base: filesBase,
			mounts: map[string]string{
				"from-zip":    filepath.Join(archivesDir, "single-testassets.zip"),
				"from-tar-gz": filepath.Join(archivesDir, "testassets.tar.gz"),
			},
			wantRootContains: []string{"assets", "index.html", "site.js", "from-zip", "from-tar-gz"},
			wantReadable: []struct{ path, substr string }{
				{"index.html", "testing/testassets/files/index.html"},
				{"site.js", "testing/testassets/files/site.js"},
				{"from-zip/index.html", "testing/testassets/files/index.html"},
				{"from-zip/site.js", "testing/testassets/files/site.js"},
				{"from-tar-gz/index.html", "testing/testassets/files/index.html"},
				{"from-tar-gz/site.js", "testing/testassets/files/site.js"},
			},
			wantDirContains: map[string][]string{
				"from-zip/assets":    {"1.txt", "2.txt", "four", "images", "onetwothree", "sixseven"},
				"from-tar-gz/assets": {"1.txt", "2.txt", "four", "images", "onetwothree", "sixseven"},
			},
		},
		{
			name: "testassets dir with zip, tar, and tar.gz siblings",
			base: testassetsBase,
			mounts: map[string]string{
				"zip-content":   filepath.Join(archivesDir, "single-testassets.zip"),
				"tar-content":   filepath.Join(archivesDir, "testassets.tar"),
				"targz-content": filepath.Join(archivesDir, "testassets.tar.gz"),
			},
			wantRootContains: []string{"files", "archives", "zip-content", "tar-content", "targz-content"},
			wantReadable: []struct{ path, substr string }{
				{"files/index.html", "testing/testassets/files/index.html"},
				{"zip-content/index.html", "testing/testassets/files/index.html"},
				{"tar-content/index.html", "testing/testassets/files/index.html"},
				{"targz-content/index.html", "testing/testassets/files/index.html"},
				{"zip-content/assets/1.txt", ""},
				{"targz-content/assets/images/1.txt", ""},
			},
			wantDirContains: map[string][]string{
				"zip-content": {"assets", "index.html", "site.js"},
			},
			wantEqualPaths: []string{
				"zip-content/index.html",
				"tar-content/index.html",
				"targz-content/index.html",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			uri, err := CreateURI(tt.base, tt.mounts)
			if err != nil {
				t.Fatal(err)
			}
			fsys, err := New(t.Context(), uri)
			if err != nil {
				t.Fatalf("New(%q) = %v", uri, err)
			}
			defer fsys.Close()

			rootEntries, err := fsys.ReadDir(".")
			if err != nil {
				t.Fatalf("ReadDir('.') = %v", err)
			}
			for _, want := range tt.wantRootContains {
				if !containsEntry(rootEntries, want) {
					t.Errorf("ReadDir('.') missing %q, got: %v", want, dirEntryListToNames(rootEntries))
				}
			}

			for _, r := range tt.wantReadable {
				assertContains(t, fsys, r.path, r.substr)
			}

			for dir, wantChildren := range tt.wantDirContains {
				entries, err := fsys.ReadDir(dir)
				if err != nil {
					t.Fatalf("ReadDir(%q) = %v", dir, err)
				}
				for _, want := range wantChildren {
					if !containsEntry(entries, want) {
						t.Errorf("ReadDir(%q) missing %q, got: %v", dir, want, dirEntryListToNames(entries))
					}
				}
			}

			if len(tt.wantEqualPaths) > 1 {
				first, err := fsys.ReadFile(tt.wantEqualPaths[0])
				if err != nil {
					t.Fatalf("ReadFile(%q) = %v", tt.wantEqualPaths[0], err)
				}
				for _, p := range tt.wantEqualPaths[1:] {
					got, err := fsys.ReadFile(p)
					if err != nil {
						t.Fatalf("ReadFile(%q) = %v", p, err)
					}
					if string(first) != string(got) {
						t.Errorf("%q and %q have different content: %q vs %q", tt.wantEqualPaths[0], p, first, got)
					}
				}
			}
		})
	}
}
