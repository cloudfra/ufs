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

package testing

import (
	"errors"
	"io/fs"
	"testing"
)

// WriteFS is a file system that can create both files and directories.
type WriteFS[F WritableFile] interface {
	CreateFS[F]
	MkdirAllFS
}

// requireWrite returns fsys as a WriteFS, skipping the test if it is not one.
func requireWrite[F WritableFile](tb testing.TB, fsys fs.FS) WriteFS[F] {
	tb.Helper()
	wfs, ok := fsys.(WriteFS[F])
	if !ok {
		tb.Skipf("%T does not implement Create and MkdirAll", fsys)
	}
	return wfs
}

// DirFileConflictCase is a Create or MkdirAll call that conflicts with the
// tree built by [NewDirFileConflictFS]: it replaces a directory with a file or
// treats a regular file as a directory.
type DirFileConflictCase[F WritableFile] struct {
	// Name identifies the case.
	Name string
	// Op performs the conflicting call and returns its error.
	Op func(fsys WriteFS[F]) error
	// WantErr is the fs sentinel error the call should wrap. Backends that
	// surface OS errors (such as ENOTDIR or EISDIR) may not wrap it, so
	// [DirFileConflicts] does not require it; drivers can check it themselves.
	WantErr error
	// WantOp is the fs.PathError.Op the call should report.
	WantOp string
}

// DirFileConflictCases returns the conflicting calls checked by
// [DirFileConflicts].
func DirFileConflictCases[F WritableFile]() []DirFileConflictCase[F] {
	create := func(name string) func(WriteFS[F]) error {
		return func(fsys WriteFS[F]) error {
			_, err := fsys.Create(name)
			return err
		}
	}
	mkdirAll := func(name string) func(WriteFS[F]) error {
		return func(fsys WriteFS[F]) error { return fsys.MkdirAll(name, fs.ModePerm) }
	}
	return []DirFileConflictCase[F]{
		{Name: "create_on_dir", Op: create("dir/sub"), WantErr: fs.ErrInvalid, WantOp: "create"},
		{Name: "create_on_root", Op: create("."), WantErr: fs.ErrInvalid, WantOp: "create"},
		{Name: "create_under_file", Op: create("dir/file/x"), WantErr: fs.ErrExist, WantOp: "create"},
		{Name: "create_deep_under_file", Op: create("dir/file/x/y"), WantErr: fs.ErrExist, WantOp: "create"},
		{Name: "mkdirall_on_file", Op: mkdirAll("dir/file"), WantErr: fs.ErrExist, WantOp: "mkdir"},
		{Name: "mkdirall_under_file", Op: mkdirAll("dir/file/x/y"), WantErr: fs.ErrExist, WantOp: "mkdir"},
	}
}

// NewDirFileConflictFS returns a file system from newFS containing the
// directories dir and dir/sub and the regular file dir/file. It skips the
// test if the file system cannot create files and directories.
func NewDirFileConflictFS[F WritableFile](t *testing.T, newFS Factory) WriteFS[F] {
	t.Helper()
	wfs := requireWrite[F](t, newFS(t))
	if err := wfs.MkdirAll("dir/sub", fs.ModePerm); err != nil {
		t.Fatalf("MkdirAll(dir/sub) = %v", err)
	}
	writeFile(t, wfs, "dir/file", "content")
	return wfs
}

// DirFileConflicts verifies that Create and MkdirAll reject every
// [DirFileConflictCases] call with a *fs.PathError and leave the tree
// unchanged, and that non-conflicting calls on the same tree still succeed.
func DirFileConflicts[F WritableFile](t *testing.T, newFS Factory) {
	t.Helper()
	requireWrite[F](t, newFS(t))
	for _, tc := range DirFileConflictCases[F]() {
		t.Run(tc.Name, func(t *testing.T) {
			fsys := NewDirFileConflictFS[F](t, newFS)
			err := tc.Op(fsys)
			var pe *fs.PathError
			if !errors.As(err, &pe) {
				t.Errorf("err = %v, want a *fs.PathError", err)
			}
			AssertDirFileTreeUnchanged(t, fsys)
		})
	}

	t.Run("existing_dirs_still_ok", func(t *testing.T) {
		fsys := NewDirFileConflictFS[F](t, newFS)
		if err := fsys.MkdirAll("dir/sub/new", fs.ModePerm); err != nil {
			t.Errorf("MkdirAll(dir/sub/new) = %v, want nil", err)
		}
		if err := fsys.MkdirAll(".", fs.ModePerm); err != nil {
			t.Errorf("MkdirAll(.) = %v, want nil", err)
		}
		writeFile(t, fsys, "dir/file", "replaced")
	})
}

// AssertDirFileTreeUnchanged checks that a tree built by
// [NewDirFileConflictFS] still has its directories, its file content, and
// nothing under the regular file.
func AssertDirFileTreeUnchanged(t *testing.T, fsys fs.FS) {
	t.Helper()
	for _, name := range []string{".", "dir", "dir/sub"} {
		if info, err := fs.Stat(fsys, name); err != nil || !info.IsDir() {
			t.Errorf("Stat(%q) = (%v, %v), want a directory", name, info, err)
		}
	}
	if got, err := readFile(t, fsys, "dir/file"); err != nil || string(got) != "content" {
		t.Errorf("ReadFile(dir/file) = (%q, %v), want (%q, nil)", got, err, "content")
	}
	// Nothing may exist under the regular file. Backends backed by the OS
	// report ENOTDIR here rather than fs.ErrNotExist, so only the absence of
	// an entry is checked.
	for _, name := range []string{"dir/file/x", "dir/file/x/y"} {
		if info, err := fs.Stat(fsys, name); err == nil {
			t.Errorf("Stat(%q) = (%v, nil), want an error", name, info)
		}
	}
}
