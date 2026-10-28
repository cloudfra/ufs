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
	"fmt"
	"io/fs"
	"slices"
	"sync"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// BatchCreate verifies [ufs.BatchCreate], which uses the file system's own
// BatchCreate when it has one: files are written with their parent
// directories, replace existing files, and an invalid or repeated name fails
// before anything is written. A batch that fails on a file system that
// implements [types.BatchCreateFS] must write nothing, including the
// directories it would have created. Its files are kept below "bc", apart
// from anything a test backend is seeded with.
func BatchCreate(t *testing.T, createFS func(*testing.T) types.WriteFS) {
	batchCreate(t, createFS, true)
}

// BatchCreateWithBuckets is [BatchCreate] for bucket-backed file systems,
// where a file and a directory can share a name, so that conflict is not
// checked.
func BatchCreateWithBuckets(t *testing.T, createFS func(*testing.T) types.WriteFS) {
	batchCreate(t, createFS, false)
}

func batchCreate(t *testing.T, createFS func(*testing.T) types.WriteFS, dirConflicts bool) {
	t.Run("BatchCreate", func(t *testing.T) {
		t.Parallel()

		t.Run("NewFiles", func(t *testing.T) {
			t.Parallel()
			fsys := newBatchFS(t, createFS)
			// Files spread over several directories at different levels,
			// sharing the "bc/new" chain and using chains of their own.
			files := []types.BatchFile{
				{Name: "bc/a.txt", Data: []byte("a")},
				{Name: "bc/new/dir/b.txt", Data: []byte("b")},
				{Name: "bc/new/empty.txt"},
				{Name: "bc/new/deeper/c.txt", Data: []byte("c")},
				{Name: "bc/new/deeper/most/deep/d.txt", Data: []byte("d")},
				{Name: "bc/side/e.txt", Data: []byte("e")},
				{Name: "bc/side/more/f.txt", Data: []byte("f")},
			}
			if err := ufs.BatchCreate(fsys, files...); err != nil {
				t.Fatalf("BatchCreate() = %v, want nil", err)
			}
			want := make(map[string]string, len(files))
			for _, file := range files {
				want[file.Name] = string(file.Data)
			}
			assertBatchFiles(t, fsys, want)
			for _, dir := range []string{"bc/new", "bc/new/dir", "bc/new/deeper", "bc/new/deeper/most", "bc/new/deeper/most/deep", "bc/side", "bc/side/more"} {
				if info, err := fsys.Stat(dir); err != nil || !info.IsDir() {
					t.Errorf("Stat(%q) = %v, %v, want a directory", dir, info, err)
				}
			}
			assertBatchDir(t, fsys, "bc/new", []string{"deeper", "dir", "empty.txt"})
		})

		t.Run("Replace", func(t *testing.T) {
			t.Parallel()
			fsys := newBatchFS(t, createFS)
			WriteFile(t, fsys, "bc/x.txt", "old content")
			if err := ufs.BatchCreate(fsys, types.BatchFile{Name: "bc/x.txt", Data: []byte("new")}); err != nil {
				t.Fatalf("BatchCreate() = %v, want nil", err)
			}
			assertBatchFiles(t, fsys, map[string]string{"bc/x.txt": "new"})
		})

		t.Run("DuplicateName", func(t *testing.T) {
			t.Parallel()
			fsys := newBatchFS(t, createFS)
			WriteFile(t, fsys, "bc/x.txt", "old")
			err := ufs.BatchCreate(fsys,
				types.BatchFile{Name: "bc/new/a.txt", Data: []byte("a")},
				types.BatchFile{Name: "bc/new/deeper/b.txt", Data: []byte("b")},
				types.BatchFile{Name: "bc/x.txt", Data: []byte("first")},
				types.BatchFile{Name: "bc/new/deeper/most/deep/c.txt", Data: []byte("c")},
				types.BatchFile{Name: "bc/side/d.txt", Data: []byte("d")},
				types.BatchFile{Name: "bc/x.txt", Data: []byte("second")},
			)
			var pathErr *fs.PathError
			if !errors.Is(err, fs.ErrInvalid) || !errors.As(err, &pathErr) || pathErr.Path != "bc/x.txt" {
				t.Fatalf("BatchCreate() = %v, want an *fs.PathError for bc/x.txt wrapping %v", err, fs.ErrInvalid)
			}
			assertBatchFiles(t, fsys, map[string]string{"bc/x.txt": "old"})
			if _, ok := fsys.(types.BatchCreateFS); !ok {
				return
			}
			assertBatchGone(t, fsys,
				"bc/new", "bc/new/a.txt", "bc/new/deeper", "bc/new/deeper/b.txt",
				"bc/new/deeper/most", "bc/new/deeper/most/deep", "bc/new/deeper/most/deep/c.txt",
				"bc/side", "bc/side/d.txt")
		})

		t.Run("Empty", func(t *testing.T) {
			t.Parallel()
			fsys := newBatchFS(t, createFS)
			if err := ufs.BatchCreate(fsys); err != nil {
				t.Errorf("BatchCreate() = %v, want nil", err)
			}
		})

		t.Run("DataNotKept", func(t *testing.T) {
			t.Parallel()
			fsys := newBatchFS(t, createFS)
			data := []byte("kept")
			if err := ufs.BatchCreate(fsys, types.BatchFile{Name: "bc/x.txt", Data: data}); err != nil {
				t.Fatalf("BatchCreate() = %v, want nil", err)
			}
			copy(data, "XXXX")
			assertBatchFiles(t, fsys, map[string]string{"bc/x.txt": "kept"})
		})

		t.Run("Concurrent", func(t *testing.T) {
			t.Parallel()
			fsys := newBatchFS(t, createFS)
			const writers = 8
			want := map[string]string{}
			var wg sync.WaitGroup
			errs := make([]error, writers)
			for i := range writers {
				name := fmt.Sprintf("bc/w%d/f.txt", i)
				want[name] = name
				wg.Go(func() {
					errs[i] = ufs.BatchCreate(fsys, types.BatchFile{Name: name, Data: []byte(name)})
				})
			}
			wg.Wait()
			if err := errors.Join(errs...); err != nil {
				t.Fatalf("BatchCreate() = %v, want nil", err)
			}
			assertBatchFiles(t, fsys, want)
		})

		invalid := []struct {
			name    string
			invalid string
		}{
			{name: "InvalidPath", invalid: "../x.txt"},
			{name: "Root", invalid: "."},
		}
		for _, tc := range invalid {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys := newBatchFS(t, createFS)
				err := ufs.BatchCreate(fsys,
					types.BatchFile{Name: "bc/ok.txt", Data: []byte("ok")},
					types.BatchFile{Name: tc.invalid, Data: []byte("bad")},
				)
				var pathErr *fs.PathError
				if !errors.Is(err, fs.ErrInvalid) || !errors.As(err, &pathErr) || pathErr.Path != tc.invalid {
					t.Fatalf("BatchCreate(%q) = %v, want an *fs.PathError for it wrapping %v", tc.invalid, err, fs.ErrInvalid)
				}
				if _, err := fsys.Stat("bc/ok.txt"); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("Stat(bc/ok.txt) = %v, want %v: nothing is written before an invalid name", err, fs.ErrNotExist)
				}
			})
		}

		if !dirConflicts {
			return
		}
		conflicts := []struct {
			name     string
			conflict string
		}{
			{name: "DirectoryAtName", conflict: "bc/dir"},
			{name: "FileAtParent", conflict: "bc/file.txt/x.txt"},
		}
		for _, tc := range conflicts {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fsys := newBatchFS(t, createFS)
				mkdirForTest(t, fsys, "bc/dir")
				WriteFile(t, fsys, "bc/file.txt", "file")
				// The files that are written before the failure are spread
				// over several directories at different levels, sharing a
				// chain and using chains of their own, so that a rollback
				// cannot pass by undoing only the last directory.
				err := ufs.BatchCreate(fsys,
					types.BatchFile{Name: "bc/new/first.txt", Data: []byte("first")},
					types.BatchFile{Name: "bc/new/deeper/second.txt", Data: []byte("second")},
					types.BatchFile{Name: "bc/new/deeper/most/deep/third.txt", Data: []byte("third")},
					types.BatchFile{Name: "bc/side/fourth.txt", Data: []byte("fourth")},
					types.BatchFile{Name: "bc/fifth.txt", Data: []byte("fifth")},
					types.BatchFile{Name: tc.conflict, Data: []byte("conflict")},
				)
				if err == nil {
					t.Fatalf("BatchCreate(%q) = nil, want an error", tc.conflict)
				}
				if info, err := fsys.Stat("bc/dir"); err != nil || !info.IsDir() {
					t.Errorf("Stat(bc/dir) = %v, %v, want a directory", info, err)
				}
				assertBatchFiles(t, fsys, map[string]string{"bc/file.txt": "file"})
				if _, ok := fsys.(types.BatchCreateFS); !ok {
					return
				}
				// Not one file, and not a directory at any level of any chain
				// the batch would have created.
				assertBatchGone(t, fsys,
					"bc/new", "bc/new/first.txt", "bc/new/deeper", "bc/new/deeper/second.txt",
					"bc/new/deeper/most", "bc/new/deeper/most/deep", "bc/new/deeper/most/deep/third.txt",
					"bc/side", "bc/side/fourth.txt", "bc/fifth.txt")
			})
		}
	})
}

// newBatchFS returns a file system from createFS with the directory "bc".
func newBatchFS(t *testing.T, createFS func(*testing.T) types.WriteFS) types.WriteFS {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	mkdirForTest(t, fsys, "bc")
	return fsys
}

// assertBatchFiles checks that each file in want has its content.
func assertBatchFiles(t *testing.T, fsys types.WriteFS, want map[string]string) {
	t.Helper()
	for name, content := range want {
		data, err := fsys.ReadFile(name)
		if err != nil {
			t.Errorf("ReadFile(%q) = %v, want nil", name, err)
			continue
		}
		if string(data) != content {
			t.Errorf("ReadFile(%q) = %q, want %q", name, data, content)
		}
	}
}

// assertBatchDir checks that dir holds exactly the entries in want, so that a
// batch cannot pass by writing a file without its directory, or by leaving an
// entry of its own behind.
func assertBatchDir(t *testing.T, fsys types.WriteFS, dir string, want []string) {
	t.Helper()
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		t.Errorf("ReadDir(%q) = %v, want nil", dir, err)
		return
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("ReadDir(%q) = %v, want %v", dir, got, want)
	}
}

// assertBatchGone checks that none of names exists, which a batch that failed
// must leave true for every file and directory it would have written.
func assertBatchGone(t *testing.T, fsys types.WriteFS, names ...string) {
	t.Helper()
	for _, name := range names {
		if info, err := fsys.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) = %v, %v, want %v: a failed batch writes nothing", name, info, err, fs.ErrNotExist)
		}
	}
}
