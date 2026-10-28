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

package readonlyfs

import (
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudfra/ufs"
	_ "github.com/cloudfra/ufs/drivers/core"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/ops"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

const (
	nullFSPrefix = "null://test"
)

// newInner returns the file system that the tests decorate.
func newInner(t *testing.T) types.WriteFS {
	t.Helper()
	fsys, err := ufs.New(t.Context(), nullFSPrefix)
	if err != nil {
		t.Fatalf("New(%q) = %v, want nil", nullFSPrefix, err)
	}
	return fsys
}

func TestReadOnlyWriteOperationsReturnPermissionDenied(t *testing.T) {
	t.Parallel()

	inner := newInner(t)
	fsys := New(inner)

	tests := []struct {
		name string
		op   func() error
	}{
		{"Create", func() error { _, err := fsys.Create("a.txt"); return err }},
		{"MkdirAll", func() error { return fsys.MkdirAll("subdir", fs.ModePerm) }},
		{"Remove", func() error { return fsys.Remove("a.txt") }},
		{"RemoveAll", func() error { return fsys.RemoveAll("subdir") }},
		{"Rename", func() error { return ops.Rename(fsys, "a.txt", "b.txt") }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.op(); !errors.Is(err, fs.ErrPermission) {
				t.Errorf("%s returned %v, want fs.ErrPermission", tc.name, err)
			}
		})
	}
}

func TestReadOnlyWriteOperationsInvalidPath(t *testing.T) {
	t.Parallel()

	inner := newInner(t)
	fsys := New(inner)

	for _, badPath := range []string{"/absolute", "../parent", "bad/../path"} {
		t.Run(badPath, func(t *testing.T) {
			t.Parallel()
			if _, err := fsys.Create(badPath); err == nil {
				t.Errorf("Create(%q) returned nil error, want error", badPath)
			}
			if err := fsys.MkdirAll(badPath, fs.ModePerm); err == nil {
				t.Errorf("MkdirAll(%q) returned nil error, want error", badPath)
			}
			if err := fsys.Remove(badPath); err == nil {
				t.Errorf("Remove(%q) returned nil error, want error", badPath)
			}
			if err := fsys.RemoveAll(badPath); err == nil {
				t.Errorf("RemoveAll(%q) returned nil error, want error", badPath)
			}
			if err := ops.Rename(fsys, badPath, "a.txt"); err == nil {
				t.Errorf("Rename(%q, a.txt) returned nil error, want error", badPath)
			}
		})
	}
}

func TestReadOnlyDelegatesReadsToInner(t *testing.T) {
	t.Parallel()

	inner := newInner(t)
	fsys := New(inner)

	if _, err := fsys.Open("."); err != nil {
		t.Errorf("Open(.) = %v, want nil", err)
	}
	if _, err := fsys.Stat("."); err != nil {
		t.Errorf("Stat(.) = %v, want nil", err)
	}
	if _, err := fsys.ReadDir("."); err != nil {
		t.Errorf("ReadDir(.) = %v, want nil", err)
	}
	if _, err := fsys.ReadFile("a.txt"); err != nil {
		t.Errorf("ReadFile(a.txt) = %v, want nil", err)
	}
}

func TestReadOnlyString(t *testing.T) {
	t.Parallel()

	inner := newInner(t)
	fsys := New(inner)

	if got := fsys.String(); !strings.Contains(got, nullFSPrefix) {
		t.Errorf("String() should contain %q, got: %q", nullFSPrefix, got)
	}
}

func TestOptionsDecode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     any
		want    Options
		wantErr bool
	}{
		{name: "nil", raw: nil, want: Options{}},
		{name: "bare true", raw: true, want: Options{Enabled: true}},
		{name: "bare false", raw: false, want: Options{}},
		{name: "mapping enabled", raw: map[string]any{"enabled": true}, want: Options{Enabled: true}},
		{name: "mapping disabled", raw: map[string]any{"enabled": false}, want: Options{}},
		{name: "empty mapping", raw: map[string]any{}, want: Options{}},
		{name: "typed", raw: Options{Enabled: true}, want: Options{Enabled: true}},
		{name: "bare non-bool", raw: "sometimes", wantErr: true},
		{name: "mapping non-bool", raw: map[string]any{"enabled": []int{1}}, wantErr: true},
		{name: "sequence", raw: []bool{true}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ufs.DecodeOptions[Options](tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("DecodeOptions(%#v) = %+v, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeOptions(%#v) = %v, want nil", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("DecodeOptions(%#v) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestWrap(t *testing.T) {
	t.Parallel()
	inner := newInner(t)

	fsys, err := wrap(t.Context(), inner, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if fsys != inner {
		t.Errorf("wrap with Enabled=false = %v, want the inner FS unchanged", fsys)
	}

	fsys, err = wrap(t.Context(), inner, Options{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fsys.(*readOnlyFS); !ok {
		t.Errorf("wrap with Enabled=true = %T, want *readOnlyFS", fsys)
	}
}

// uriFS overrides the URI reported by the file system it embeds.
type uriFS struct {
	types.WriteFS
	u   *url.URL
	err error
}

func (fsys *uriFS) URI() (*url.URL, error) {
	return fsys.u, fsys.err
}

func TestReadOnlyURI(t *testing.T) {
	t.Parallel()

	t.Run("records the decorator", func(t *testing.T) {
		t.Parallel()
		fsys := New(&uriFS{WriteFS: newInner(t), u: &url.URL{Scheme: "memory", Host: "test", RawQuery: "cache=null%3A"}})
		u, err := fsys.URI()
		if err != nil {
			t.Fatalf("URI() = %v, want nil", err)
		}
		if got, want := u.String(), "memory://test?cache=null%3A&options=%5B%7BreadOnly%3A+true%7D%5D&ro=true"; got != want {
			t.Errorf("URI() = %q, want %q", got, want)
		}
	})

	t.Run("inner has no URI", func(t *testing.T) {
		t.Parallel()
		fsys := New(&uriFS{WriteFS: newInner(t)})
		if u, err := fsys.URI(); u != nil || err != nil {
			t.Errorf("URI() = %v, %v, want nil, nil", u, err)
		}
	})

	t.Run("inner URI fails", func(t *testing.T) {
		t.Parallel()
		wantErr := errors.New("no uri")
		fsys := New(&uriFS{WriteFS: newInner(t), err: wantErr})
		if _, err := fsys.URI(); !errors.Is(err, wantErr) {
			t.Errorf("URI() = %v, want %v", err, wantErr)
		}
	})
}

// assertMaskedInfo checks that info reports inner's mode without its write
// permission bits and passes every other field through.
func assertMaskedInfo(t *testing.T, what string, info, innerInfo fs.FileInfo) {
	t.Helper()

	wantMode := innerInfo.Mode() &^ writableModeBits
	if info.Mode() != wantMode {
		t.Errorf("%s mode = %v, want %v", what, info.Mode(), wantMode)
	}
	if info.Name() != innerInfo.Name() {
		t.Errorf("%s name = %q, want %q", what, info.Name(), innerInfo.Name())
	}
	if info.Size() != innerInfo.Size() {
		t.Errorf("%s size = %d, want %d", what, info.Size(), innerInfo.Size())
	}
	if info.IsDir() != innerInfo.IsDir() {
		t.Errorf("%s IsDir = %t, want %t", what, info.IsDir(), innerInfo.IsDir())
	}
	if !info.ModTime().Equal(innerInfo.ModTime()) {
		t.Errorf("%s modification time = %v, want %v", what, info.ModTime(), innerInfo.ModTime())
	}
}

func TestReadOnlyMasksStatAndLstatModeBits(t *testing.T) {
	t.Parallel()

	inner := newInner(t)
	fsys := New(inner)

	tests := []struct {
		name   string
		masked func() (fs.FileInfo, error)
		inner  func() (fs.FileInfo, error)
	}{
		{
			name:   "file",
			masked: func() (fs.FileInfo, error) { return fsys.Stat("a.txt") },
			inner:  func() (fs.FileInfo, error) { return inner.Stat("a.txt") },
		},
		{
			name:   "directory",
			masked: func() (fs.FileInfo, error) { return fsys.Stat(".") },
			inner:  func() (fs.FileInfo, error) { return inner.Stat(".") },
		},
		{
			name:   "Lstat file",
			masked: func() (fs.FileInfo, error) { return fsys.Lstat("a.txt") },
			inner:  func() (fs.FileInfo, error) { return inner.Lstat("a.txt") },
		},
		{
			name:   "Lstat directory",
			masked: func() (fs.FileInfo, error) { return fsys.Lstat(".") },
			inner:  func() (fs.FileInfo, error) { return inner.Lstat(".") },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			innerInfo, err := tc.inner()
			if err != nil {
				t.Fatalf("%s on the wrapped file system = %v, want nil", tc.name, err)
			}
			if innerInfo.Mode().Perm()&writableModeBits == 0 {
				t.Fatalf("%s on the wrapped file system mode = %v, want write permission bits to mask", tc.name, innerInfo.Mode())
			}

			info, err := tc.masked()
			if err != nil {
				t.Fatalf("%s = %v, want nil", tc.name, err)
			}
			assertMaskedInfo(t, tc.name, info, innerInfo)
		})
	}
}

// newLocalInner returns a local file system over a temporary directory holding a
// writable file and a writable subdirectory. The modes give the owner write
// permission without any group or other bits, so a normal umask cannot change
// them and gosec does not object to them either.
func newLocalInner(t *testing.T) (types.WriteFS, string) {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("contents"), 0o600); err != nil {
		t.Fatalf("WriteFile = %v, want nil", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o700); err != nil {
		t.Fatalf("Mkdir = %v, want nil", err)
	}

	fsys, err := ufs.New(t.Context(), dir)
	if err != nil {
		t.Fatalf("New(%q) = %v, want nil", dir, err)
	}
	// localFS keeps an open handle on the directory, which makes Windows refuse
	// to remove the temporary directory when the test ends, so the file system
	// has to be closed before t.TempDir cleanup runs.
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	return fsys, dir
}

func TestReadOnlyMasksLocalFSModeBits(t *testing.T) {
	t.Parallel()

	inner, _ := newLocalInner(t)
	fsys := New(inner)

	tests := []struct {
		name   string
		masked func() (fs.FileInfo, error)
		inner  func() (fs.FileInfo, error)
	}{
		{
			name:   "Stat file",
			masked: func() (fs.FileInfo, error) { return fsys.Stat("a.txt") },
			inner:  func() (fs.FileInfo, error) { return inner.Stat("a.txt") },
		},
		{
			name:   "Stat directory",
			masked: func() (fs.FileInfo, error) { return fsys.Stat("subdir") },
			inner:  func() (fs.FileInfo, error) { return inner.Stat("subdir") },
		},
		{
			name:   "Lstat file",
			masked: func() (fs.FileInfo, error) { return fsys.Lstat("a.txt") },
			inner:  func() (fs.FileInfo, error) { return inner.Lstat("a.txt") },
		},
		{
			name: "ReadDir entry",
			masked: func() (fs.FileInfo, error) {
				entries, err := fsys.ReadDir(".")
				if err != nil {
					return nil, err
				}
				for _, entry := range entries {
					if entry.Name() == "a.txt" {
						return entry.Info()
					}
				}
				return nil, fs.ErrNotExist
			},
			inner: func() (fs.FileInfo, error) {
				info, err := inner.Stat("a.txt")
				if err != nil {
					return nil, err
				}
				return info, nil
			},
		},
		{
			name: "ReadDir entry directory",
			masked: func() (fs.FileInfo, error) {
				entries, err := fsys.ReadDir(".")
				if err != nil {
					return nil, err
				}
				for _, entry := range entries {
					if entry.Name() == "subdir" {
						return entry.Info()
					}
				}
				return nil, fs.ErrNotExist
			},
			inner: func() (fs.FileInfo, error) {
				info, err := inner.Stat("subdir")
				if err != nil {
					return nil, err
				}
				return info, nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			innerInfo, err := tc.inner()
			if err != nil {
				t.Fatalf("%s on the wrapped file system = %v, want nil", tc.name, err)
			}
			if innerInfo.Mode().Perm()&writableModeBits == 0 {
				t.Fatalf("%s on the wrapped file system mode = %v, want write permission bits to mask", tc.name, innerInfo.Mode())
			}

			info, err := tc.masked()
			if err != nil {
				t.Fatalf("%s = %v, want nil", tc.name, err)
			}
			assertMaskedInfo(t, tc.name, info, innerInfo)
		})
	}
}

func TestReadOnlyMasksOpenHandleMode(t *testing.T) {
	t.Parallel()

	inner, _ := newLocalInner(t)
	fsys := New(inner)

	file, err := fsys.Open("a.txt")
	if err != nil {
		t.Fatalf("Open(a.txt) = %v, want nil", err)
	}
	innerFile, err := inner.Open("a.txt")
	if err != nil {
		t.Fatalf("Open(a.txt) on the wrapped file system = %v, want nil", err)
	}

	info, err := file.Stat()
	if err != nil {
		t.Fatalf("Stat on the opened file = %v, want nil", err)
	}
	innerInfo, err := innerFile.Stat()
	if err != nil {
		t.Fatalf("Stat on the wrapped opened file = %v, want nil", err)
	}
	assertMaskedInfo(t, "Open handle", info, innerInfo)

	// The FUSE and ProjFS hosts serve reads at an offset, so the handle must keep
	// the capabilities of the file it wraps.
	if _, ok := file.(io.ReaderAt); !ok {
		t.Errorf("Open handle does not satisfy io.ReaderAt, want the wrapped capability kept")
	}
	if _, ok := file.(io.Seeker); !ok {
		t.Errorf("Open handle does not satisfy io.Seeker, want the wrapped capability kept")
	}

	if err := file.Close(); err != nil {
		t.Errorf("Close = %v, want nil", err)
	}
	if err := innerFile.Close(); err != nil {
		t.Errorf("Close on the wrapped file = %v, want nil", err)
	}
}

func TestReadOnlyMasksDirectoryHandleEntries(t *testing.T) {
	t.Parallel()

	inner, _ := newLocalInner(t)
	fsys := New(inner)

	dir, err := fsys.Open(".")
	if err != nil {
		t.Fatalf("Open(.) = %v, want nil", err)
	}
	// nestFS recognizes a mounted directory by the fs.ReadDirFile capability of
	// the handle, so masking must not drop it.
	dirFile, ok := dir.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("Open(.) handle does not satisfy fs.ReadDirFile, want the wrapped capability kept")
	}

	entries, err := dirFile.ReadDir(-1)
	if err != nil {
		t.Fatalf("ReadDir(-1) = %v, want nil", err)
	}
	innerEntries, err := inner.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.) on the wrapped file system = %v, want nil", err)
	}
	if len(entries) != len(innerEntries) {
		t.Fatalf("ReadDir(-1) returned %d entries, want %d", len(entries), len(innerEntries))
	}
	// A directory handle lists in the order the operating system stores its
	// entries, so the entries are paired by name, not by position.
	innerInfos := make(map[string]fs.FileInfo, len(innerEntries))
	for _, entry := range innerEntries {
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("Info for %q on the wrapped file system = %v, want nil", entry.Name(), err)
		}
		innerInfos[entry.Name()] = info
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("Info for %q = %v, want nil", entry.Name(), err)
		}
		innerInfo, ok := innerInfos[entry.Name()]
		if !ok {
			t.Fatalf("the directory handle listed %q, which the wrapped file system does not list", entry.Name())
		}
		assertMaskedInfo(t, "directory handle "+entry.Name(), info, innerInfo)
	}

	if err := dir.Close(); err != nil {
		t.Errorf("Close = %v, want nil", err)
	}
}

// TestWrappedFileSystemKeepsModeBits checks that masking does not rewrite the
// entries or infos the wrapped file system may have cached for its own use.
func TestWrappedFileSystemKeepsModeBits(t *testing.T) {
	t.Parallel()

	inner, _ := newLocalInner(t)
	fsys := New(inner)

	if _, err := fsys.ReadDir("."); err != nil {
		t.Fatalf("ReadDir(.) = %v, want nil", err)
	}
	if _, err := fsys.Stat("a.txt"); err != nil {
		t.Fatalf("Stat(a.txt) = %v, want nil", err)
	}

	entries, err := inner.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.) on the wrapped file system = %v, want nil", err)
	}
	if len(entries) == 0 {
		t.Fatal("the wrapped file system returned no entries")
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("Info for %q = %v, want nil", entry.Name(), err)
		}
		if info.Mode().Perm()&writableModeBits == 0 {
			t.Errorf("wrapped entry %q mode = %v, want its write permission bits kept", entry.Name(), info.Mode())
		}
	}

	info, err := inner.Stat("a.txt")
	if err != nil {
		t.Fatalf("Stat(a.txt) on the wrapped file system = %v, want nil", err)
	}
	if info.Mode().Perm()&writableModeBits == 0 {
		t.Errorf("wrapped file mode = %v, want its write permission bits kept", info.Mode())
	}
}

func TestReadOnlyMaskingIsIdempotent(t *testing.T) {
	t.Parallel()

	inner, _ := newLocalInner(t)
	single := New(inner)
	double := New(single)

	tests := []struct {
		name  string
		once  func() (fs.FileInfo, error)
		twice func() (fs.FileInfo, error)
	}{
		{
			name:  "Stat",
			once:  func() (fs.FileInfo, error) { return single.Stat("a.txt") },
			twice: func() (fs.FileInfo, error) { return double.Stat("a.txt") },
		},
		{
			name:  "ReadDir entry",
			once:  func() (fs.FileInfo, error) { return maskedEntryInfo(single, ".", "a.txt") },
			twice: func() (fs.FileInfo, error) { return maskedEntryInfo(double, ".", "a.txt") },
		},
		{
			name:  "Open handle",
			once:  func() (fs.FileInfo, error) { return handleStat(single, "a.txt") },
			twice: func() (fs.FileInfo, error) { return handleStat(double, "a.txt") },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			one, err := tc.once()
			if err != nil {
				t.Fatalf("%s through one read-only layer = %v, want nil", tc.name, err)
			}
			first, err := tc.twice()
			if err != nil {
				t.Fatalf("%s through two read-only layers = %v, want nil", tc.name, err)
			}
			second, err := tc.twice()
			if err != nil {
				t.Fatalf("%s through two read-only layers = %v, want nil", tc.name, err)
			}
			if first.Mode() != one.Mode() {
				t.Errorf("%s mode = %v through one layer and %v through two, want the same mode", tc.name, one.Mode(), first.Mode())
			}
			if second.Mode() != first.Mode() {
				t.Errorf("%s mode = %v, then %v, want the same mode", tc.name, first.Mode(), second.Mode())
			}
			if first.Mode().Perm()&writableModeBits != 0 {
				t.Errorf("%s mode = %v, want no write permission bits", tc.name, first.Mode())
			}
		})
	}
}

// maskedEntryInfo returns the info that fsys reports for the named entry of dir.
func maskedEntryInfo(fsys fs.FS, dir, name string) (fs.FileInfo, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Name() == name {
			return entry.Info()
		}
	}
	return nil, fs.ErrNotExist
}

// handleStat returns the info that a handle opened by fsys reports.
func handleStat(fsys fs.FS, name string) (fs.FileInfo, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	info, statErr := f.Stat()
	if err := f.Close(); err != nil {
		return nil, err
	}
	return info, statErr
}

func TestMaskFileInfoModeTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode fs.FileMode
		want fs.FileMode
	}{
		{name: "writable file", mode: 0o644, want: 0o444},
		{name: "writable executable", mode: 0o755, want: 0o555},
		{name: "writable directory", mode: fs.ModeDir | 0o777, want: fs.ModeDir | 0o555},
		{name: "writable symlink", mode: fs.ModeSymlink | 0o777, want: fs.ModeSymlink | 0o555},
		{name: "read-only file", mode: 0o444, want: 0o444},
		{name: "read-only directory", mode: fs.ModeDir | 0o555, want: fs.ModeDir | 0o555},
		{name: "setuid file", mode: fs.ModeSetuid | 0o664, want: fs.ModeSetuid | 0o444},
		{name: "no permissions", mode: 0o000, want: 0o000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			inner := &fakeFileInfo{name: "fake.txt", size: 7, mode: tc.mode, modTime: fakeModTime, sys: fakeSys}
			info, err := maskFileInfo(inner, nil)
			if err != nil {
				t.Fatalf("maskFileInfo = %v, want nil", err)
			}
			if info.Mode() != tc.want {
				t.Errorf("maskFileInfo mode = %v, want %v", info.Mode(), tc.want)
			}
			if info.Sys() != fakeSys {
				t.Errorf("maskFileInfo Sys = %v, want the wrapped value %v", info.Sys(), fakeSys)
			}
		})
	}
}

func TestReadOnlyMaskingPassesThroughErrors(t *testing.T) {
	t.Parallel()

	angry, err := ufs.New(t.Context(), "angry://test")
	if err != nil {
		t.Fatalf("New(angry://test) = %v, want nil", err)
	}
	fsys := New(angry)

	t.Run("Stat", func(t *testing.T) {
		t.Parallel()
		info, err := fsys.Stat("a.txt")
		if info != nil {
			t.Errorf("Stat = %v, want nil", info)
		}
		if !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Stat error = %v, want fs.ErrInvalid", err)
		}
	})

	t.Run("Lstat", func(t *testing.T) {
		t.Parallel()
		info, err := fsys.Lstat("a.txt")
		if info != nil {
			t.Errorf("Lstat = %v, want nil", info)
		}
		if !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Lstat error = %v, want fs.ErrInvalid", err)
		}
	})

	t.Run("ReadDir", func(t *testing.T) {
		t.Parallel()
		entries, err := fsys.ReadDir(".")
		if entries != nil {
			t.Errorf("ReadDir = %v, want nil", entries)
		}
		if !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("ReadDir error = %v, want fs.ErrInvalid", err)
		}
	})

	t.Run("Open", func(t *testing.T) {
		t.Parallel()
		file, err := fsys.Open("a.txt")
		if file != nil {
			t.Errorf("Open = %v, want nil", file)
		}
		if !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Open error = %v, want fs.ErrInvalid", err)
		}
	})
}

func TestMaskDirEntriesPassesThroughEntryInfoError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("cannot read entry")
	entries, err := maskDirEntries([]fs.DirEntry{&fakeDirEntry{name: "a.txt", infoErr: wantErr}}, nil)
	if err != nil {
		t.Fatalf("maskDirEntries = %v, want nil", err)
	}
	if len(entries) != 1 {
		t.Fatalf("maskDirEntries returned %d entries, want 1", len(entries))
	}
	info, err := entries[0].Info()
	if info != nil {
		t.Errorf("entry Info = %v, want nil", info)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("entry Info error = %v, want %v", err, wantErr)
	}
	if entries[0].Name() != "a.txt" {
		t.Errorf("entry Name = %q, want %q", entries[0].Name(), "a.txt")
	}
	if entries[0].Type() != fs.FileMode(0) {
		t.Errorf("entry Type = %v, want a regular file", entries[0].Type())
	}
}

func TestMaskFileModeKeepsCapabilities(t *testing.T) {
	t.Parallel()

	errStat := errors.New("cannot stat")
	errReadDir := errors.New("cannot read dir")

	// ToFile gives every masked handle the types.File method set, so Seek,
	// ReadAt, Write and WriteString are always callable. What follows from the
	// inner file is whether they succeed or report why they cannot.
	tests := []struct {
		name       string
		file       fs.File
		dir        bool
		seekErr    error
		readAtErr  error
		writeErr   error
		statErr    error
		readDirErr error
	}{
		{
			name:      "bare file",
			file:      &fakeFile{mode: 0o664},
			seekErr:   errors.ErrUnsupported,
			readAtErr: errors.ErrUnsupported,
			writeErr:  fs.ErrPermission,
		},
		{
			name:     "file with seek",
			file:     &fakeSeekFile{fakeFile: fakeFile{mode: 0o664}},
			writeErr: fs.ErrPermission,
		},
		{
			name:     "directory file",
			file:     &fakeDirFile{fakeSeekFile: fakeSeekFile{fakeFile: fakeFile{mode: fs.ModeDir | 0o775}}},
			dir:      true,
			writeErr: fs.ErrPermission,
		},
		{
			name: "typed file",
			file: &fakeTypedFile{fakeSeekFile: fakeSeekFile{fakeFile: fakeFile{mode: 0o600}}},
		},
		{
			name: "typed directory file",
			file: &fakeTypedDirFile{fakeTypedFile: fakeTypedFile{fakeSeekFile: fakeSeekFile{fakeFile: fakeFile{mode: fs.ModeDir | 0o777}}}},
			dir:  true,
		},
		{
			name:       "directory file with failing ReadDir",
			file:       &fakeDirFile{fakeSeekFile: fakeSeekFile{fakeFile: fakeFile{mode: fs.ModeDir | 0o777}}, readDirErr: errReadDir},
			dir:        true,
			writeErr:   fs.ErrPermission,
			readDirErr: errReadDir,
		},
		{
			name:     "directory file with failing Stat",
			file:     &fakeDirFile{fakeSeekFile: fakeSeekFile{fakeFile: fakeFile{mode: fs.ModeDir | 0o777, statErr: errStat}}},
			dir:      true,
			writeErr: fs.ErrPermission,
			statErr:  errStat,
		},
		{
			name:       "typed directory file with failing ReadDir",
			file:       &fakeTypedDirFile{fakeTypedFile: fakeTypedFile{fakeSeekFile: fakeSeekFile{fakeFile: fakeFile{mode: fs.ModeDir | 0o777}}}, readDirErr: errReadDir},
			dir:        true,
			readDirErr: errReadDir,
		},
		{
			name:    "typed file with failing Stat",
			file:    &fakeTypedFile{fakeSeekFile: fakeSeekFile{fakeFile: fakeFile{mode: 0o600, statErr: errStat}}},
			statErr: errStat,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			masked := maskFileMode(tc.file)
			typedFile, isTyped := masked.(types.File)
			if !isTyped {
				t.Fatal("masked file is not a types.File")
			}
			dirFile, isDir := masked.(fs.ReadDirFile)
			if isDir != tc.dir {
				t.Errorf("masked file is an fs.ReadDirFile = %t, want %t", isDir, tc.dir)
			}

			if _, err := typedFile.Seek(0, io.SeekStart); !errors.Is(err, tc.seekErr) {
				t.Errorf("Seek error = %v, want %v", err, tc.seekErr)
			}
			if _, err := typedFile.ReadAt(make([]byte, 1), 0); !errors.Is(err, tc.readAtErr) {
				t.Errorf("ReadAt error = %v, want %v", err, tc.readAtErr)
			}
			if _, err := typedFile.Write([]byte("x")); !errors.Is(err, tc.writeErr) {
				t.Errorf("Write error = %v, want %v", err, tc.writeErr)
			}
			if _, err := typedFile.WriteString("x"); !errors.Is(err, tc.writeErr) {
				t.Errorf("WriteString error = %v, want %v", err, tc.writeErr)
			}

			info, err := masked.Stat()
			switch {
			case tc.statErr != nil:
				if info != nil || !errors.Is(err, tc.statErr) {
					t.Errorf("Stat = %v, %v, want nil and %v", info, err, tc.statErr)
				}
			case err != nil:
				t.Fatalf("Stat = %v, want nil", err)
			case info.Mode().Perm()&writableModeBits != 0:
				t.Errorf("Stat mode = %v, want no write permission bits", info.Mode())
			}

			if !isDir {
				return
			}
			entries, err := dirFile.ReadDir(-1)
			if tc.readDirErr != nil {
				if entries != nil || !errors.Is(err, tc.readDirErr) {
					t.Errorf("ReadDir = %v, %v, want nil and %v", entries, err, tc.readDirErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadDir = %v, want nil", err)
			}
			for _, entry := range entries {
				entryInfo, err := entry.Info()
				if err != nil {
					t.Fatalf("entry Info = %v, want nil", err)
				}
				if entryInfo.Mode().Perm()&writableModeBits != 0 {
					t.Errorf("entry mode = %v, want no write permission bits", entryInfo.Mode())
				}
			}
		})
	}
}

var (
	fakeModTime = time.Unix(1234567890, 0)
	fakeSys     = "wrapped metadata"
)

// fakeFileInfo is a [fs.FileInfo] carrying every field the mask must preserve
// except the write permission bits.
type fakeFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	sys     any
}

func (info *fakeFileInfo) Name() string {
	return info.name
}

func (info *fakeFileInfo) Size() int64 {
	return info.size
}

func (info *fakeFileInfo) Mode() fs.FileMode {
	return info.mode
}

func (info *fakeFileInfo) ModTime() time.Time {
	return info.modTime
}

func (info *fakeFileInfo) IsDir() bool {
	return info.mode.IsDir()
}

func (info *fakeFileInfo) Sys() any {
	return info.sys
}

// fakeDirEntry is a [fs.DirEntry] whose Info reports the given mode, or fails
// when infoErr is set.
type fakeDirEntry struct {
	name    string
	mode    fs.FileMode
	infoErr error
}

func (entry *fakeDirEntry) Name() string {
	return entry.name
}

func (entry *fakeDirEntry) IsDir() bool {
	return entry.mode.IsDir()
}

func (entry *fakeDirEntry) Type() fs.FileMode {
	return entry.mode.Type()
}

func (entry *fakeDirEntry) Info() (fs.FileInfo, error) {
	if entry.infoErr != nil {
		return nil, entry.infoErr
	}
	return &fakeFileInfo{name: entry.name, size: 7, mode: entry.mode, modTime: fakeModTime, sys: fakeSys}, nil
}

// fakeFile is a bare [fs.File] whose Stat reports a writable mode.
type fakeFile struct {
	mode    fs.FileMode
	statErr error
}

func (f *fakeFile) Stat() (fs.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return &fakeFileInfo{name: "fake.txt", size: 7, mode: f.mode, modTime: fakeModTime, sys: fakeSys}, nil
}

func (f *fakeFile) Read(_ []byte) (int, error) {
	return 0, nil
}

func (f *fakeFile) Close() error {
	return nil
}

// fakeSeekFile adds the offset capabilities that the FUSE and ProjFS hosts need.
type fakeSeekFile struct {
	fakeFile
}

func (f *fakeSeekFile) Seek(_ int64, _ int) (int64, error) {
	return 0, nil
}

func (f *fakeSeekFile) ReadAt(_ []byte, _ int64) (int, error) {
	return 0, nil
}

// fakeTypedFile makes the handle a [types.File].
type fakeTypedFile struct {
	fakeSeekFile
}

func (f *fakeTypedFile) Write(_ []byte) (int, error) {
	return 0, nil
}

func (f *fakeTypedFile) WriteString(_ string) (int, error) {
	return 0, nil
}

// fakeDirFile adds only the directory capability.
type fakeDirFile struct {
	fakeSeekFile
	readDirErr error
}

func (f *fakeDirFile) ReadDir(_ int) ([]fs.DirEntry, error) {
	if f.readDirErr != nil {
		return nil, f.readDirErr
	}
	return []fs.DirEntry{
		&fakeDirEntry{name: "child.txt", mode: 0o664},
		&fakeDirEntry{name: "subdir", mode: fs.ModeDir | 0o775},
	}, nil
}

// fakeTypedDirFile has both the [types.File] and the [fs.ReadDirFile]
// capabilities, which is the shape of a directory handle from a typed driver.
type fakeTypedDirFile struct {
	fakeTypedFile
	readDirErr error
}

func (f *fakeTypedDirFile) ReadDir(_ int) ([]fs.DirEntry, error) {
	if f.readDirErr != nil {
		return nil, f.readDirErr
	}
	return []fs.DirEntry{
		&fakeDirEntry{name: "child.txt", mode: 0o664},
		&fakeDirEntry{name: "subdir", mode: fs.ModeDir | 0o775},
	}, nil
}

// TestReadOnlyURIDoesNotModifyWrappedURI verifies that URI leaves the URL of
// the wrapped file system alone, even when that file system hands out the URL
// it keeps.
func TestReadOnlyURIDoesNotModifyWrappedURI(t *testing.T) {
	inner := &url.URL{Scheme: "memory"}
	got, err := New(&uriFS{WriteFS: newInner(t), u: inner}).URI()
	if err != nil {
		t.Fatalf("URI() = %v, want nil", err)
	}
	if got == nil || !got.Query().Has("ro") {
		t.Errorf("URI() = %v, want a URI with the ro parameter", got)
	}
	if inner.RawQuery != "" {
		t.Errorf("wrapped URI query = %q, want it left empty", inner.RawQuery)
	}
}
