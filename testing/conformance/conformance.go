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

package conformance

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"testing"
	"testing/fstest"

	"github.com/cloudfra/ufs"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

func TestFileSystem(t *testing.T, newFSFunc func(ctx context.Context, name string) (ufs.FS, error), name string) {
	t.Helper()
	fsys := ufsTesting.MustFS(t, newFSFunc, name)

	wantFiles := []string{"a", "ab/b/c", "ab/d/c", "def", "abc", "abc.txt", "temp/abc.txt"}

	mkdirForTest(t, fsys, "ab/b")
	mkdirForTest(t, fsys, "temp")
	mkdirForTest(t, fsys, "ab/d")

	for _, name := range wantFiles {
		t.Run(fmt.Sprintf("crud_%s", name), func(t *testing.T) {
			wantData := ufsTesting.RandomString(1000)
			if wf, err := fsys.Create(name); err != nil {
				t.Errorf("cannot create file %q, %s", name, err)
			} else {
				info, err := wf.Stat()
				if err != nil {
					t.Errorf("cannot Stat() %q, %s", name, err)
				}
				if info == nil {
					t.Fatalf("info is nil")
				}
				if info.IsDir() != false {
					t.Errorf("%q is a directory, want file", name)
				}
				if n, err := io.WriteString(wf, wantData); err != nil {
					t.Errorf("cannot write file content to %q, %s", name, err)
				} else if n != len(wantData) {
					t.Errorf("contents written to file does not match the size got %d, want %d", n, len(wantData))
				}
				if err := wf.Close(); err != nil {
					t.Errorf("failed to Close() write file %q, %s", name, err)
				}
			}

			if rf, err := fsys.Open(name); err != nil {
				t.Errorf("cannot open file %q, %s", name, err)
			} else {
				if rf == nil {
					t.Fatal("rf is nil")
				}
				info, err := rf.Stat()
				if err != nil {
					t.Errorf("cannot Stat() %q, %s", name, err)
				}
				if info == nil {
					t.Fatal("info is nil")
				}
				if info.IsDir() != false {
					t.Errorf("%q is a directory, want file", name)
				}
				if got, err := io.ReadAll(rf); err != nil {
					t.Errorf("cannot read file content to %q, %s", name, err)
				} else if diff := cmp.Diff(wantData, string(got)); diff != "" {
					t.Errorf("io.ReadAll(%s) mismatch (-want +got):\n%s\nwant: %q\ngot: %q", name, diff, wantData, string(got))
				}
				if err := rf.Close(); err != nil {
					t.Errorf("failed to Close() read file %q, %s", name, err)
				}
			}
		})
	}

	if err := fstest.TestFS(fsys, wantFiles...); err != nil {
		t.Errorf("fstest.TestFS failed for %q: %v", name, err)
	}

	if err := fsys.Close(); err != nil {
		t.Errorf("error on Close(), %v", err)
	}
}

func mkdirForTest(tb testing.TB, fsys FS, dirs ...string) {
	tb.Helper()
	dir := path.Join(dirs...)
	if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
		tb.Fatalf("cannot create directory %q, %s", dir, err)
	}
}
