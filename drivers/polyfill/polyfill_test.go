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

package polyfill_test

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/core"
	"github.com/cloudfra/ufs/drivers/polyfill"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// selfFS implements OpenFile with the polyfill, as the drivers do.
type selfFS struct {
	ufs.WriteFS
}

func (fsys selfFS) OpenFile(name string, flag int, perm fs.FileMode) (fs.File, error) {
	return polyfill.OpenFile(fsys, name, flag, perm)
}

// TestOpenFileConformance runs the shared OpenFile tests against a file
// system whose OpenFile is the polyfill.
func TestOpenFileConformance(t *testing.T) {
	ufsdriversTesting.OpenFile(t, func(_ *testing.T) ufs.WriteFS {
		return selfFS{core.MakeMemFS("memory://test")}
	})
}

func TestOpenFileFlags(t *testing.T) {
	t.Parallel()
	const name = "a.txt"
	for _, tc := range []struct {
		name string
		flag int
		// exists is whether the file is there before the call.
		exists  bool
		wantErr error
	}{
		{name: "read", flag: os.O_RDONLY, exists: true},
		{name: "read_sync", flag: os.O_RDONLY | os.O_SYNC, exists: true},
		{name: "read_missing", flag: os.O_RDONLY, wantErr: fs.ErrNotExist},
		{name: "replace", flag: os.O_WRONLY | os.O_CREATE | os.O_TRUNC, exists: true},
		{name: "create", flag: os.O_WRONLY | os.O_CREATE | os.O_TRUNC},
		{name: "create_read_write", flag: os.O_RDWR | os.O_CREATE | os.O_TRUNC},
		{name: "truncate", flag: os.O_WRONLY | os.O_TRUNC, exists: true},
		{name: "truncate_missing", flag: os.O_WRONLY | os.O_TRUNC, wantErr: fs.ErrNotExist},
		{name: "write_in_place", flag: os.O_WRONLY, exists: true, wantErr: errors.ErrUnsupported},
		{name: "read_write_in_place", flag: os.O_RDWR, exists: true, wantErr: errors.ErrUnsupported},
		{name: "create_without_truncate", flag: os.O_WRONLY | os.O_CREATE, wantErr: errors.ErrUnsupported},
		{name: "append", flag: os.O_WRONLY | os.O_APPEND, exists: true, wantErr: errors.ErrUnsupported},
		{name: "append_truncate", flag: os.O_WRONLY | os.O_CREATE | os.O_TRUNC | os.O_APPEND, wantErr: errors.ErrUnsupported},
		{name: "exclusive", flag: os.O_WRONLY | os.O_CREATE | os.O_TRUNC | os.O_EXCL, wantErr: errors.ErrUnsupported},
		{name: "read_only_truncate", flag: os.O_RDONLY | os.O_TRUNC, exists: true, wantErr: errors.ErrUnsupported},
		{name: "read_only_create", flag: os.O_RDONLY | os.O_CREATE, wantErr: errors.ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := core.MakeMemFS("memory://test")
			defer ufsTesting.ValidateClose(t, fsys)()
			if tc.exists {
				ufsdriversTesting.WriteFile(t, fsys, name, "content")
			}

			f, err := polyfill.OpenFile(fsys, name, tc.flag, fs.ModePerm)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("OpenFile(%#x) = %v, want %v", tc.flag, err, tc.wantErr)
			}
			if err != nil {
				if f != nil {
					t.Errorf("OpenFile(%#x) = %v with an error, want a nil file", tc.flag, f)
				}
				var pathErr *fs.PathError
				if !errors.As(err, &pathErr) || pathErr.Path != name {
					t.Errorf("OpenFile(%#x) = %v, want an *fs.PathError for %q", tc.flag, err, name)
				}
				// A rejected open leaves the file as it was.
				data, readErr := fsys.ReadFile(name)
				if tc.exists && (readErr != nil || string(data) != "content") {
					t.Errorf("ReadFile() after a rejected open = %q, %v, want content, nil", data, readErr)
				}
				if !tc.exists && !errors.Is(readErr, fs.ErrNotExist) {
					t.Errorf("ReadFile() after a rejected open = %v, want %v", readErr, fs.ErrNotExist)
				}
				return
			}
			defer ufsTesting.ValidateClose(t, f)()
			if _, ok := f.(ufs.File); opensForWrite(tc.flag) && !ok {
				t.Errorf("OpenFile(%#x) = %T, want a ufs.File", tc.flag, f)
			}
		})
	}
}

func opensForWrite(flag int) bool {
	return flag&(os.O_WRONLY|os.O_RDWR) != 0
}

func TestOpenFileReadOnlyFS(t *testing.T) {
	t.Parallel()
	inner := core.MakeMemFS("memory://test")
	ufsdriversTesting.WriteFile(t, inner, "a.txt", "content")
	fsys := struct{ ufs.ReadFS }{inner}
	defer ufsTesting.ValidateClose(t, fsys)()

	f, err := polyfill.OpenFile(fsys, "a.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(O_RDONLY) = %v, want nil", err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}

	if _, err := polyfill.OpenFile(fsys, "a.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fs.ModePerm); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("OpenFile(O_WRONLY|O_CREATE|O_TRUNC) = %v, want %v", err, fs.ErrPermission)
	}
	if data, err := inner.ReadFile("a.txt"); err != nil || string(data) != "content" {
		t.Errorf("ReadFile() = %q, %v, want content, nil", data, err)
	}
}

func TestOpenFileInvalidPath(t *testing.T) {
	t.Parallel()
	fsys := core.MakeMemFS("memory://test")
	defer ufsTesting.ValidateClose(t, fsys)()
	for _, flag := range []int{os.O_RDONLY, os.O_WRONLY | os.O_CREATE | os.O_TRUNC, os.O_RDWR} {
		if _, err := polyfill.OpenFile(fsys, "/a.txt", flag, fs.ModePerm); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("OpenFile(/a.txt, %#x) = %v, want %v", flag, err, fs.ErrInvalid)
		}
	}
}
