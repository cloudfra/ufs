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

package testing

import (
	"errors"
	"io/fs"
	"path"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

// MkdirAll verifies that MkdirAll creates every missing directory in a path,
// that each one is reported as a directory and listed by its parent next to
// its siblings, and that repeating the call on existing directories succeeds.
func MkdirAll(t *testing.T, args Args, createFSFunc func(t *testing.T) fs.FS) {
	mkdirAll(t, args, func(t *testing.T) types.WriteFS {
		return mustWriteFS(t, createFSFunc(t))
	})
}

func mkdirAll(t *testing.T, args Args, createFSFunc func(t *testing.T) types.WriteFS) {
	if args.Bucket {
		t.Run("MkdirAll", func(t *testing.T) {
			t.Parallel()
			fsys := createFSFunc(t)
			defer ufsTesting.ValidateClose(t, fsys)()
			for _, dir := range mkdirAllLeaves {
				if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
					t.Fatalf("MkdirAll(%q) = %v, want nil", dir, err)
				}
				name := path.Join(dir, "file")
				f, err := fsys.Create(name)
				if err != nil {
					t.Fatalf("Create(%q) = %v, want nil", name, err)
				}
				if err := f.Close(); err != nil {
					t.Fatalf("Close() = %v, want nil", err)
				}
			}
			ufsTesting.AssertDirsInOrder(t, fsys, wantDirTree(mkdirAllLeaves, "file"))
		})
	} else {
		t.Run("MkdirAll", func(t *testing.T) {
			t.Parallel()
			fsys := createFSFunc(t)
			defer ufsTesting.ValidateClose(t, fsys)()
			for _, dir := range mkdirAllLeaves {
				if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
					t.Fatalf("MkdirAll(%q) = %v, want nil", dir, err)
				}
			}
			ufsTesting.AssertDirsInOrder(t, fsys, wantDirTree(mkdirAllLeaves))
			for _, dir := range mkdirAllLeaves {
				if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
					t.Errorf("MkdirAll(%q) on existing directories = %v, want nil", dir, err)
				}
			}
		})
	}
}

// mkdirAllFS runs the [types.MkdirAllFS] assertions a file system declared
// read-only through Args.ReadOnly has to hold. A writable file system gets
// them from fsInternal and writeFS instead, which also run MkdirAll through a
// nest wrapper, so this only covers what neither of them reaches.
func mkdirAllFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	if !args.ReadOnly {
		return
	}
	t.Run("MkdirAllFS", func(t *testing.T) {
		requireType[types.MkdirAllFS](t, args)
		t.Parallel()
		mkdirAllReadOnly(t, requireFSTypeFunc[mkdirAllReadOnlyTestFS](t, createFSFunc))
		mkdirAllInvalidPaths(t, requireFSTypeFunc[mkdirAllInvalidNamesTestFS](t, createFSFunc))
	})
}

// mkdirAllReadOnlyTestFS is the minimum set of interfaces mkdirAllReadOnly
// needs: it asks a read-only file system to create a directory and checks that
// its own root listing is left alone, which needs a listing and nothing else.
type mkdirAllReadOnlyTestFS interface {
	types.SimpleFS
	types.MkdirAllFS
	fs.ReadDirFS
}

// mkdirAllInvalidNamesTestFS is the minimum set of interfaces
// mkdirAllInvalidPaths needs: it only asks MkdirAll for names that must be
// refused, which every file system checks before it looks anything up, so
// nothing has to be seeded or read back.
type mkdirAllInvalidNamesTestFS interface {
	types.SimpleFS
	types.MkdirAllFS
}

// mkdirAllReadOnlyNames are the directories a read-only file system is asked
// to create. They live under mkdirall/, like the writable suite's tree, and
// the nested one is asked for first so a partial creation leaves a listing the
// second call cannot hide.
var mkdirAllReadOnlyNames = []string{"mkdirall/a/b/c", "mkdirall/a"}

// mkdirAllReadOnly checks MkdirAll on a file system declared read-only through
// Args.ReadOnly: it refuses with an *fs.PathError for mkdir wrapping
// [fs.ErrPermission], or reports success without creating anything, as
// *core.nullFS does for every write.
func mkdirAllReadOnly(t *testing.T, createFSFunc func(t *testing.T) mkdirAllReadOnlyTestFS) {
	t.Run("MkdirAllReadOnly", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		before, err := fs.ReadDir(fsys, ".")
		if err != nil {
			t.Fatalf("ReadDir('.') = %v, want nil", err)
		}

		for _, name := range mkdirAllReadOnlyNames {
			err := fsys.MkdirAll(name, fs.ModePerm)
			if err == nil {
				continue
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("MkdirAll(%q) = %T %v, want an *fs.PathError", name, err, err)
			} else if pathErr.Op != "mkdir" {
				t.Errorf("MkdirAll(%q) Op = %q, want mkdir", name, pathErr.Op)
			}
			if !errors.Is(err, fs.ErrPermission) {
				t.Errorf("MkdirAll(%q) = %v, want an error wrapping %v", name, err, fs.ErrPermission)
			}
		}

		after, err := fs.ReadDir(fsys, ".")
		if err != nil {
			t.Fatalf("ReadDir('.') after MkdirAll = %v, want nil", err)
		}
		if diff := cmp.Diff(dirNames(before), dirNames(after)); diff != "" {
			t.Errorf("ReadDir('.') changed after MkdirAll on a read-only file system (-want +got):\n%s", diff)
		}
	})
}

// mkdirAllInvalidPaths checks that MkdirAll refuses every invalid name with an
// *fs.PathError for mkdir, which is what [pathutil.Validate] returns before a
// file system looks anything up. A writable file system gets this from
// InvalidPathsForWriteFS, which no read-only file system reaches.
func mkdirAllInvalidPaths(t *testing.T, createFSFunc func(t *testing.T) mkdirAllInvalidNamesTestFS) {
	t.Run("MkdirAllInvalidPaths", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		for _, name := range invalidPaths {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				err := fsys.MkdirAll(name, fs.ModePerm)
				ufsTesting.AssertInvalidPathError(t, name, err, "mkdir")
				// AssertInvalidPathError checks the shape of the error only, so
				// the sentinel is checked here too: a file system that looks an
				// invalid name up before validating it reports fs.ErrNotExist in
				// an *fs.PathError with the same Op and Path.
				if !errors.Is(err, fs.ErrInvalid) {
					t.Errorf("MkdirAll(%q) = %v, want an error wrapping %v", name, err, fs.ErrInvalid)
				}
			})
		}
	})
}
