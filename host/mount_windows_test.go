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

//go:build windows

package host

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/cloudfra/ufs"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
)

// enableProjFSTestEnv names the environment variable that opts the ProjFS
// mount tests in. They are skipped unless it is set, so a host without ProjFS
// (a Server Core image, for example) is not expected to run them. A host that
// does set it is expected to have ProjFS, so a failure there fails the test
// instead of being skipped.
const enableProjFSTestEnv = "UFS_ENABLE_PROJFS_TEST"

func requireProjFS(t *testing.T) {
	t.Helper()
	if os.Getenv(enableProjFSTestEnv) == "" {
		t.Skipf("set %s to run the ProjFS tests", enableProjFSTestEnv)
	}
}

// projfsReadOnlyFS wraps a types.ReadFS so the result does not satisfy the FS
// interface, forcing the mount to be treated as read-only.
type projfsReadOnlyFS struct {
	types.ReadFS
}

func testProjFSMount(t *testing.T, fsys types.ReadFS) string {
	t.Helper()
	requireProjFS(t)
	mountDir := t.TempDir()
	server, err := Mount(t.Context(), fsys, mountDir)
	if err != nil {
		t.Fatalf("Mount: %v", err)
		return ""
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("unmount cleanup: %v", err)
		}
	})
	return mountDir
}

// --- Unit tests (no ProjFS required) ---

func TestProjfsPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input *uint16
		want  string
	}{
		{nil, "."},
	}
	for _, tc := range tests {
		got := projfsPath(tc.input)
		if got != tc.want {
			t.Errorf("projfsPath(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}

	// Test with actual string pointers — backslash conversion.
	for _, tc := range []struct {
		name string
		want string
	}{
		{`dir1\dir2\file.txt`, "dir1/dir2/file.txt"},
		{`file.txt`, "file.txt"},
		{`a\b`, "a/b"},
	} {
		ptr, err := windows.UTF16PtrFromString(tc.name)
		if err != nil {
			t.Fatalf("UTF16PtrFromString(%q): %v", tc.name, err)
		}
		got := projfsPath(ptr)
		if got != tc.want {
			t.Errorf("projfsPath(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestProjfsHRESULT(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want uintptr
	}{
		{nil, 0},
		{fs.ErrNotExist, 0x80070002},
		{fs.ErrPermission, 0x80070005},
		{fs.ErrExist, 0x80070050},
		{errors.New("unknown"), 0x80004005},
		{fmt.Errorf("wrap: %w", fs.ErrNotExist), 0x80070002},
	}
	for _, tc := range tests {
		got := projfsHRESULT(tc.err)
		if got != tc.want {
			t.Errorf("projfsHRESULT(%v) = %#x, want %#x", tc.err, got, tc.want)
		}
	}
}

func TestHresultToError(t *testing.T) {
	t.Parallel()
	if err := hresultToError("PrjStartVirtualizing", 0); err != nil {
		t.Errorf("hresultToError(S_OK) = %v, want nil", err)
	}
	tests := []struct {
		hr        uintptr
		want      error
		wantInMsg string
	}{
		// HRESULT_FROM_WIN32(ERROR_FILE_SYSTEM_VIRTUALIZATION_UNAVAILABLE)
		{0x80070171, windows.ERROR_FILE_SYSTEM_VIRTUALIZATION_UNAVAILABLE, "fltmc load PrjFlt"},
		// HRESULT_FROM_WIN32(ERROR_REPARSE_POINT_ENCOUNTERED)
		{0x8007112B, windows.ERROR_REPARSE_POINT_ENCOUNTERED, "new empty directory"},
		// HRESULT_FROM_WIN32(ERROR_REPARSE_TAG_MISMATCH)
		{0x8007112A, windows.ERROR_REPARSE_TAG_MISMATCH, "not marked as a ProjFS virtualization root"},
		// HRESULT_FROM_WIN32(ERROR_FILE_NOT_FOUND) also matches fs.ErrNotExist.
		{0x80070002, fs.ErrNotExist, "HRESULT 0x80070002"},
		// HRESULT_FROM_WIN32(ERROR_ACCESS_DENIED) also matches fs.ErrPermission.
		{0x80070005, fs.ErrPermission, "can write to the mount directory"},
		// E_FAIL is not a Win32 error and has no resolution.
		{0x80004005, windows.Errno(0x80004005), "HRESULT 0x80004005"},
	}
	for _, tc := range tests {
		err := hresultToError("PrjStartVirtualizing", tc.hr)
		if !errors.Is(err, tc.want) {
			t.Errorf("hresultToError(%#x) = %v, want errors.Is %v", tc.hr, err, tc.want)
		}
		if msg := err.Error(); !strings.HasPrefix(msg, "PrjStartVirtualizing: ") || !strings.Contains(msg, tc.wantInMsg) {
			t.Errorf("hresultToError(%#x).Error() = %q, want it to name the call and contain %q", tc.hr, msg, tc.wantInMsg)
		}
		if got := projfsHRESULT(err); got != tc.hr {
			t.Errorf("projfsHRESULT(hresultToError(%#x)) = %#x, want the same HRESULT", tc.hr, got)
		}
	}
}

func TestProjfsFileRenames(t *testing.T) {
	t.Parallel()
	s := &projfsMountServer{files: make(map[uint64]string)}
	ids := map[string]uint64{}
	for _, p := range []string{"a.txt", "dir/f.txt", "dir/sub/g.txt", "dirx/h.txt"} {
		ids[p] = s.addFile(p)
	}
	s.renameFiles("a.txt", "b.txt")
	s.renameFiles("dir", "moved")

	name, err := windows.UTF16PtrFromString("original.txt")
	if err != nil {
		t.Fatal(err)
	}
	for orig, want := range map[string]string{
		"a.txt":         "b.txt",
		"dir/f.txt":     "moved/f.txt",
		"dir/sub/g.txt": "moved/sub/g.txt",
		"dirx/h.txt":    "dirx/h.txt",
	} {
		var v prjPlaceholderVersionInfo
		binary.LittleEndian.PutUint64(v.ContentID[:], ids[orig])
		if got := s.filePath(&prjCallbackData{FilePathName: name, VersionInfo: &v}); got != want {
			t.Errorf("filePath(%q) = %q, want %q", orig, got, want)
		}
	}
	// A placeholder without a known ID is read from its FilePathName.
	if got := s.filePath(&prjCallbackData{FilePathName: name}); got != "original.txt" {
		t.Errorf("filePath(no version info) = %q, want %q", got, "original.txt")
	}
}

// --- Integration tests — lifecycle ---

func TestMountClose(t *testing.T) {
	t.Parallel()
	requireProjFS(t)

	srcDir := t.TempDir()
	if err := osutil.WriteFile(filepath.Join(srcDir, "f.txt"), []byte("x")); err != nil {
		t.Fatal(err)
	}

	fsys, err := ufs.New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	mountDir := t.TempDir()
	server, err := Mount(t.Context(), fsys, mountDir)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	if _, err := osutil.Stat(filepath.Join(mountDir, "f.txt")); err != nil {
		t.Fatalf("Stat before close: %v", err)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestMountContextCancel(t *testing.T) {
	t.Parallel()
	requireProjFS(t)

	srcDir := t.TempDir()
	if err := osutil.WriteFile(filepath.Join(srcDir, "f.txt"), []byte("x")); err != nil {
		t.Fatal(err)
	}

	fsys, err := ufs.New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	ctx, cancel := context.WithCancel(t.Context())
	mountDir := t.TempDir()
	server, err := Mount(ctx, fsys, mountDir)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	defer validateClose(t, server)()

	if _, err := osutil.Stat(filepath.Join(mountDir, "f.txt")); err != nil {
		t.Fatalf("Stat before cancel: %v", err)
	}

	cancel()
}

// --- Integration tests — read-only enforcement and special backends ---

func TestMountReadOnly(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()

	fsys, err := ufs.New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	roFS := &projfsReadOnlyFS{fsys}
	t.Log(testProjFSMount(t, roFS))
	t.Skip("ProjFS cannot intercept new file creation or mkdir — writes materialize to local NTFS")
}

func TestMountReadOnlyMkdir(t *testing.T) {
	t.Parallel()
	fsys, err := ufs.New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	roFS := &projfsReadOnlyFS{fsys}
	t.Log(testProjFSMount(t, roFS))
	t.Skip("ProjFS cannot intercept new file creation or mkdir — writes materialize to local NTFS")
}

func TestMountReadOnlyRemove(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	if err := osutil.WriteFile(filepath.Join(srcDir, "keep.txt"), []byte("x")); err != nil {
		t.Fatal(err)
	}

	fsys, err := ufs.New(t.Context(), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	mountDir := testProjFSMount(t, fsys)

	err = hostRemove(t.Context(), filepath.Join(mountDir, "keep.txt"))
	if err == nil {
		t.Fatal("remove on mount succeeded, want access denied")
	}

	data, err := osutil.ReadFile(filepath.Join(mountDir, "keep.txt"))
	if err != nil {
		t.Fatalf("ReadFile after failed remove: %v", err)
	}
	if string(data) != "x" {
		t.Errorf("content = %q, want %q", data, "x")
	}
}

func TestMountReadOnlyRename(t *testing.T) {
	t.Parallel()
	fsys, err := ufs.New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))
	if err := writeFile(fsys, "keep.txt", "x"); err != nil {
		t.Fatal(err)
	}

	mountDir := testProjFSMount(t, &projfsReadOnlyFS{fsys})
	if err := hostRename(t.Context(), filepath.Join(mountDir, "keep.txt"), filepath.Join(mountDir, "moved.txt")); err == nil {
		t.Fatal("rename on read-only mount succeeded, want access denied")
	}
	if _, err := fsys.Stat("keep.txt"); err != nil {
		t.Errorf("Stat(keep.txt) after failed rename = %v", err)
	}
	if _, err := osutil.Stat(filepath.Join(mountDir, "keep.txt")); err != nil {
		t.Errorf("Stat(keep.txt) on mount after failed rename = %v", err)
	}
}

// --- Integration test — end-to-end mount and read-back verification ---

func TestProjFSMountReadBack(t *testing.T) {
	t.Parallel()
	requireProjFS(t)

	type testFile struct {
		path    string
		content []byte
	}

	// Build a set of files with varied sizes and nesting depths.
	files := []testFile{
		{"hello.txt", []byte("hello world")},
		{"empty.txt", []byte{}},
		{"binary.bin", func() []byte {
			b := make([]byte, 256)
			for i := range b {
				b[i] = byte(i)
			}
			return b
		}()},
		{"subdir/nested.txt", []byte("nested content")},
		{"subdir/deep/deeper/leaf.txt", []byte("deep leaf")},
		{"large.dat", bytes.Repeat([]byte("ABCDEFGHIJKLMNOP"), 16384)}, // 256KB
	}

	// Populate a memFS with the test files.
	fsys, err := ufs.New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	for _, f := range files {
		w, err := fsys.Create(f.path)
		if err != nil {
			t.Fatalf("Create(%q): %v", f.path, err)
		}
		if _, err := w.Write(f.content); err != nil {
			if err := w.Close(); t != nil {
				t.Errorf("cannot close writer, %s", err)
			}
			t.Fatalf("Write(%q): %v", f.path, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close(%q): %v", f.path, err)
		}
	}

	// Mount the memFS via ProjFS.
	mountDir := testProjFSMount(t, fsys)

	// Read every file back through the OS mount and verify content.
	for _, f := range files {
		osPath := filepath.Join(mountDir, filepath.FromSlash(f.path))
		got, err := osutil.ReadFile(osPath)
		if err != nil {
			t.Errorf("ReadFile(%q): %v", f.path, err)
			continue
		}
		if !bytes.Equal(got, f.content) {
			t.Errorf("content mismatch for %q: got %d bytes, want %d bytes", f.path, len(got), len(f.content))
			if len(got) < 200 && len(f.content) < 200 {
				t.Errorf("  got:  %q", got)
				t.Errorf("  want: %q", f.content)
			}
		}
	}

	// Verify Stat on each file reports correct size and type.
	for _, f := range files {
		osPath := filepath.Join(mountDir, filepath.FromSlash(f.path))
		fi, err := osutil.Stat(osPath)
		if err != nil {
			t.Errorf("Stat(%q): %v", f.path, err)
			continue
		}
		if fi.IsDir() {
			t.Errorf("Stat(%q).IsDir() = true, want false", f.path)
		}
		if fi.Size() != int64(len(f.content)) {
			t.Errorf("Stat(%q).Size() = %d, want %d", f.path, fi.Size(), len(f.content))
		}
	}

	// Verify directory listings at the root.
	rootEntries, err := osutil.ReadDir(mountDir)
	if err != nil {
		t.Fatalf("ReadDir(root): %v", err)
	}
	wantRootNames := []string{"binary.bin", "empty.txt", "hello.txt", "large.dat", "subdir"}
	gotRootNames := make([]string, len(rootEntries))
	for i, e := range rootEntries {
		gotRootNames[i] = e.Name()
	}
	sort.Strings(gotRootNames)
	if !slices.Equal(gotRootNames, wantRootNames) {
		t.Errorf("root entries = %v, want %v", gotRootNames, wantRootNames)
	}

	// Verify subdirectory listing.
	subEntries, err := osutil.ReadDir(filepath.Join(mountDir, "subdir"))
	if err != nil {
		t.Fatalf("ReadDir(subdir): %v", err)
	}
	wantSubNames := []string{"deep", "nested.txt"}
	gotSubNames := make([]string, len(subEntries))
	for i, e := range subEntries {
		gotSubNames[i] = e.Name()
	}
	sort.Strings(gotSubNames)
	if !slices.Equal(gotSubNames, wantSubNames) {
		t.Errorf("subdir entries = %v, want %v", gotSubNames, wantSubNames)
	}

	// Verify non-existent file returns appropriate error.
	_, err = osutil.Stat(filepath.Join(mountDir, "does-not-exist.txt"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(nonexistent) error = %v, want ErrNotExist", err)
	}
}

func TestProjFSPlaceholderInfoLayout(t *testing.T) {
	// Layouts of PRJ_FILE_BASIC_INFO and PRJ_PLACEHOLDER_INFO from
	// projectedfslib.h. PrjWritePlaceholderInfo validates its size argument
	// against sizeof(PRJ_PLACEHOLDER_INFO) and fails the call with
	// ERROR_INSUFFICIENT_BUFFER when it is smaller, so the Go mapping has to
	// match the C struct byte for byte.
	t.Run("PRJ_FILE_BASIC_INFO", func(t *testing.T) {
		var f prjFileBasicInfo
		fields := []struct {
			name string
			got  uintptr
			want uintptr
		}{
			{"IsDirectory", unsafe.Offsetof(f.IsDirectory), 0},
			{"FileSize", unsafe.Offsetof(f.FileSize), 8},
			{"CreationTime", unsafe.Offsetof(f.CreationTime), 16},
			{"LastAccessTime", unsafe.Offsetof(f.LastAccessTime), 24},
			{"LastWriteTime", unsafe.Offsetof(f.LastWriteTime), 32},
			{"ChangeTime", unsafe.Offsetof(f.ChangeTime), 40},
			{"FileAttributes", unsafe.Offsetof(f.FileAttributes), 48},
		}
		for _, field := range fields {
			if field.got != field.want {
				t.Errorf("%s offset = %d, want %d",
					field.name, field.got, field.want)
			}
		}
		if got, want := unsafe.Sizeof(f), uintptr(56); got != want {
			t.Errorf("sizeof(PRJ_FILE_BASIC_INFO) = %d, want %d", got, want)
		}
	})

	t.Run("PRJ_PLACEHOLDER_INFO", func(t *testing.T) {
		var p prjPlaceholderInfo
		fields := []struct {
			name string
			got  uintptr
			want uintptr
		}{
			{"FileBasicInfo", unsafe.Offsetof(p.FileBasicInfo), 0},
			{"EaInformation", unsafe.Offsetof(p.EaInformation), 56},
			{"SecurityInformation", unsafe.Offsetof(p.SecurityInformation), 64},
			{"StreamsInformation", unsafe.Offsetof(p.StreamsInformation), 72},
			{"VersionInfo", unsafe.Offsetof(p.VersionInfo), 80},
			{"VariableData", unsafe.Offsetof(p.VariableData), 336},
		}
		for _, field := range fields {
			if field.got != field.want {
				t.Errorf("%s offset = %d, want %d",
					field.name, field.got, field.want)
			}
		}
		if got, want := unsafe.Sizeof(p), uintptr(344); got != want {
			t.Errorf("sizeof(PRJ_PLACEHOLDER_INFO) = %d, want %d", got, want)
		}
	})
}
