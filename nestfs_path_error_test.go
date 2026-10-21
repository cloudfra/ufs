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

package ufs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/testing/archivetest"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// pathErrorPath returns the path an error reports, which is empty when the
// error is not an [fs.PathError].
func pathErrorPath(t *testing.T, err error) string {
	t.Helper()
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		return ""
	}
	return pathErr.Path
}

// mountedAngryFS returns a nestFS with the file system that errors on every
// operation mounted at mountPath. Its errors name the path relative to its own
// root, which is what makes it a fixture for the path a caller should see.
func mountedAngryFS(t *testing.T, mountPath string) FS {
	t.Helper()
	fsys := makeNestFS(t.Context(), mustBaseFS(t, "memory:///"))
	if err := fsys.addMount(mountPath, makeNestFS(t.Context(), mustBaseFS(t, "angry:"))); err != nil {
		t.Fatal(err)
	}
	return fsys
}

func TestNestFSMountErrorNamesCallerPath(t *testing.T) {
	const mountPath = "frozen"
	cases := []struct {
		name     string
		op       func(fsys FS) error
		wantOp   string
		wantPath string
	}{
		{
			name:     "open",
			op:       func(fsys FS) error { _, err := fsys.Open(mountPath + "/sub/x.txt"); return err },
			wantOp:   "open",
			wantPath: mountPath + "/sub/x.txt",
		},
		{
			name:     "create",
			op:       func(fsys FS) error { _, err := fsys.Create(mountPath + "/x.txt"); return err },
			wantOp:   "create",
			wantPath: mountPath + "/x.txt",
		},
		{
			name:     "mkdir",
			op:       func(fsys FS) error { return fsys.MkdirAll(mountPath+"/deep/dir", 0o700) },
			wantOp:   "mkdir",
			wantPath: mountPath + "/deep/dir",
		},
		{
			name:     "readdir",
			op:       func(fsys FS) error { _, err := fsys.ReadDir(mountPath + "/sub"); return err },
			wantOp:   "readdir",
			wantPath: mountPath + "/sub",
		},
		{
			name:     "stat",
			op:       func(fsys FS) error { _, err := fsys.Stat(mountPath + "/x.txt"); return err },
			wantOp:   "stat",
			wantPath: mountPath + "/x.txt",
		},
		{
			name:     "lstat",
			op:       func(fsys FS) error { _, err := fsys.Lstat(mountPath + "/x.txt"); return err },
			wantOp:   "lstat",
			wantPath: mountPath + "/x.txt",
		},
		{
			name:     "readfile",
			op:       func(fsys FS) error { _, err := fsys.ReadFile(mountPath + "/x.txt"); return err },
			wantOp:   "readfile",
			wantPath: mountPath + "/x.txt",
		},
		{
			name:     "readlink",
			op:       func(fsys FS) error { _, err := fsys.ReadLink(mountPath + "/x.txt"); return err },
			wantOp:   "readlink",
			wantPath: mountPath + "/x.txt",
		},
		{
			name:     "remove",
			op:       func(fsys FS) error { return fsys.Remove(mountPath + "/x.txt") },
			wantOp:   "remove",
			wantPath: mountPath + "/x.txt",
		},
		{
			name:     "removeall",
			op:       func(fsys FS) error { return fsys.RemoveAll(mountPath + "/x.txt") },
			wantOp:   "removeall",
			wantPath: mountPath + "/x.txt",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := mountedAngryFS(t, mountPath)

			err := tc.op(fsys)
			if err == nil {
				t.Fatalf("%s through the mount returned no error", tc.name)
			}
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("%s through the mount returned %v, want fs.ErrInvalid from the mounted file system", tc.name, err)
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Fatalf("%s through the mount returned %v, want an *fs.PathError", tc.name, err)
			}
			if pathErr.Op != tc.wantOp {
				t.Errorf("%s through the mount reported op %q, want %q", tc.name, pathErr.Op, tc.wantOp)
			}
			if pathErr.Path != tc.wantPath {
				t.Errorf("%s through the mount reported path %q, want %q: %v", tc.name, pathErr.Path, tc.wantPath, err)
			}
		})
	}
}

func TestNestFSRenameThroughMountNamesCallerPaths(t *testing.T) {
	fsys := mountedAngryFS(t, "frozen")
	err := fsys.Rename("frozen/old.txt", "frozen/new.txt")
	if err == nil {
		t.Fatal("rename through the mount returned no error")
	}
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) {
		t.Fatalf("rename through the mount returned %v, want an *os.LinkError", err)
	}
	if linkErr.Old != "frozen/old.txt" || linkErr.New != "frozen/new.txt" {
		t.Errorf("rename through the mount reported %q to %q, want %q to %q", linkErr.Old, linkErr.New, "frozen/old.txt", "frozen/new.txt")
	}
}

func TestNestFSRootErrorNamesCallerPath(t *testing.T) {
	fsys := makeNestFS(t.Context(), mustBaseFS(t, "memory:///"))
	defer ufsTesting.ValidateClose(t, fsys)()

	_, err := fsys.Open("sub/x.txt")
	if err == nil {
		t.Fatal("open returned no error")
	}
	if got := pathErrorPath(t, err); got != "sub/x.txt" {
		t.Errorf("open reported path %q, want %q: %v", got, "sub/x.txt", err)
	}

	if err := fsys.MkdirAll("sub/dir", 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err = fsys.Open("sub/dir/x.txt")
	if err == nil {
		t.Fatal("open in a new directory returned no error")
	}
	if got := pathErrorPath(t, err); got != "sub/dir/x.txt" {
		t.Errorf("open in a new directory reported path %q, want %q: %v", got, "sub/dir/x.txt", err)
	}
}

// TestNestFSMountBelowMountErrorNamesCallerPath covers a mount of a file system
// that has a mount of its own, where the two file systems each add the path
// segment they know.
func TestNestFSMountBelowMountErrorNamesCallerPath(t *testing.T) {
	inner := makeNestFS(t.Context(), mustBaseFS(t, "memory:///"))
	if err := inner.addMount("second", makeNestFS(t.Context(), mustBaseFS(t, "angry:"))); err != nil {
		t.Fatal(err)
	}
	outer := makeNestFS(t.Context(), mustBaseFS(t, "memory:///"))
	if err := outer.addMount("frozen", makeNestFS(t.Context(), inner)); err != nil {
		t.Fatal(err)
	}

	const wantPath = "frozen/second/deep/x.txt"
	_, err := outer.Open(wantPath)
	if err == nil {
		t.Fatal("open through two mounts returned no error")
	}
	if got := pathErrorPath(t, err); got != wantPath {
		t.Errorf("open through two mounts reported path %q, want %q: %v", got, wantPath, err)
	}

	if err := outer.MkdirAll("frozen/second/deep/dir", 0o700); err == nil {
		t.Fatal("mkdir through two mounts returned no error")
	} else if got := pathErrorPath(t, err); got != "frozen/second/deep/dir" {
		t.Errorf("mkdir through two mounts reported path %q, want %q: %v", got, "frozen/second/deep/dir", err)
	}
}

// TestNestFSLocalMountErrorNamesCallerPath uses a local file system for a
// mount, which reports its own paths relative to its own directory, so that a
// path below it is only complete once ufs adds the mount point.
func TestNestFSLocalMountErrorNamesCallerPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "ro.txt"), []byte("data"), 0o400); err != nil {
		t.Fatal(err)
	}

	fsys := makeNestFS(t.Context(), mustBaseFS(t, "memory:///"))
	if err := fsys.addMount("disk", makeNestFS(t.Context(), mustBaseFS(t, "file:"+dir))); err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	_, err := fsys.Open("disk/sub/missing.txt")
	if err == nil {
		t.Fatal("open through the local mount returned no error")
	}
	if got := pathErrorPath(t, err); got != "disk/sub/missing.txt" {
		t.Errorf("open through the local mount reported path %q, want %q: %v", got, "disk/sub/missing.txt", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("open through the local mount returned %v, want fs.ErrNotExist", err)
	}
}

// TestNestFSArchiveErrorNamesCallerPath reads through the content directory of
// an archive, where the file system that answers is the archive itself.
func TestNestFSArchiveErrorNamesCallerPath(t *testing.T) {
	dir := t.TempDir()
	if err := osutil.WriteFile(filepath.Join(dir, "testassets.zip"), archivetest.ZipDir(t, testAssetsFilesDir)); err != nil {
		t.Fatal(err)
	}

	fsys, err := newNestFS(t.Context(), "file://"+dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	// Reading a file that is in the archive mounts its content directory, so
	// the next read is answered by the archive file system.
	if _, err := fsys.ReadFile("testassets.zip.d/index.html"); err != nil {
		t.Fatalf("reading through the archive mount: %v", err)
	}

	const wantPath = "testassets.zip.d/missing.txt"
	_, err = fsys.ReadFile(wantPath)
	if err == nil {
		t.Fatalf("reading %q in the archive returned no error", wantPath)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("reading %q in the archive returned %v, want fs.ErrNotExist", wantPath, err)
	}
	if got := pathErrorPath(t, err); got != wantPath {
		t.Errorf("reading %q in the archive reported path %q, want %q: %v", wantPath, got, wantPath, err)
	}
}

// absPathFS reports an absolute path in the errors of the operations it
// overrides, as a file system backed by a place outside ufs does.
type absPathFS struct {
	types.WriteFS
}

func (fsys *absPathFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{
		Op:   "open",
		Path: "/" + name,
		Err:  fs.ErrNotExist,
	}
}

func TestNestFSMountErrorKeepsAbsolutePath(t *testing.T) {
	base := &absPathFS{WriteFS: mustBaseFS(t, "memory:///")}
	fsys := makeNestFS(t.Context(), mustBaseFS(t, "memory:///"))
	if err := fsys.addMount("outside", makeNestFS(t.Context(), base)); err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	const wantPath = "/sub/x.txt"
	_, err := fsys.Open("outside/sub/x.txt")
	if err == nil {
		t.Fatal("open through the mount returned no error")
	}
	if got := pathErrorPath(t, err); got != wantPath {
		t.Errorf("open through the mount reported path %q, want the absolute path %q unchanged: %v", got, wantPath, err)
	}
}

func TestMountPathError(t *testing.T) {
	missing := &fs.PathError{Op: "open", Path: "sub/x.txt", Err: fs.ErrNotExist}
	notAPathError := errors.New("open went wrong")
	wrapped := &wrappedPathError{err: missing}

	// An empty want means the error must be returned as it came.
	cases := []struct {
		name    string
		err     error
		caller  string
		subName string
		want    string
	}{
		{name: "nil error", err: nil, caller: "frozen/x.txt", subName: "x.txt", want: ""},
		{name: "not routed", err: missing, caller: "sub/x.txt", subName: "sub/x.txt", want: ""},
		{name: "mounted at the root", err: &fs.PathError{Op: "readdir", Path: ".", Err: fs.ErrNotExist}, caller: "testassets.zip.d", subName: ".", want: "testassets.zip.d"},
		{name: "one mount", err: missing, caller: "frozen/sub/x.txt", subName: "sub/x.txt", want: "frozen/sub/x.txt"},
		{name: "deep mount", err: missing, caller: "a/b/frozen/sub/x.txt", subName: "sub/x.txt", want: "a/b/frozen/sub/x.txt"},
		{name: "inner path below the routed path", err: &fs.PathError{Op: "mkdir", Path: "deep", Err: fs.ErrNotExist}, caller: "frozen/deep/dir", subName: "deep/dir", want: "frozen/deep"},
		{name: "not a path error", err: notAPathError, caller: "frozen/x.txt", subName: "x.txt", want: ""},
		{name: "wrapped path error", err: wrapped, caller: "frozen/x.txt", subName: "x.txt", want: ""},
		{name: "absolute inner path", err: &fs.PathError{Op: "open", Path: "/elsewhere/x.txt", Err: fs.ErrNotExist}, caller: "disk/x.txt", subName: "x.txt", want: ""},
		{name: "empty inner path", err: &fs.PathError{Op: "open", Path: "", Err: fs.ErrNotExist}, caller: "frozen", subName: ".", want: "frozen"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rewritePathError(tc.err, tc.caller, tc.subName)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("rewritePathError returned %v, want nil", got)
				}
				return
			}
			// The reason an operation failed must survive, whether the path
			// was rewritten or not.
			if cause := errorCause(tc.err); !errors.Is(got, cause) {
				t.Errorf("rewritePathError returned %v, which no longer reports the cause %v", got, cause)
			}
			if tc.want == "" {
				if got != tc.err {
					t.Errorf("rewritePathError returned %v, want the error unchanged", got)
				}
				return
			}
			var pathErr *fs.PathError
			if !errors.As(got, &pathErr) {
				t.Fatalf("rewritePathError returned %v, want an *fs.PathError", got)
			}
			if pathErr.Path != tc.want {
				t.Errorf("rewritePathError reported path %q, want %q", pathErr.Path, tc.want)
			}
		})
	}
}

// errorCause returns the last error in err's chain, the value that a rewritten
// error must still report with errors.Is.
func errorCause(err error) error {
	for {
		cause := errors.Unwrap(err)
		if cause == nil {
			return err
		}
		err = cause
	}
}

// wrappedPathError is an error that mentions a path in its own message and
// wraps an [fs.PathError], which rewritePathError must leave alone.
type wrappedPathError struct {
	err error
}

func (err *wrappedPathError) Error() string {
	return "the mounted file system could not open sub/x.txt"
}

func (err *wrappedPathError) Unwrap() error {
	return err.err
}
