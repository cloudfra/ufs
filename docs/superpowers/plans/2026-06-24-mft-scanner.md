# MFT-Accelerated File Scanning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Windows MFT-based file tree scanning to `localFS` so that `ForEachFilename`, `ForEachFileInfo`, `ListFiles`, `Rsync`, and `Walk` (from `op.go`) run 10-60x faster on NTFS volumes when the process is running as admin.

**Architecture:** `localFS` implements `ForEachFilenameIter`, `ForEachFileInfoIter`, and `ListFilenames` on Windows via a new MFT enumeration engine in `mft_windows.go`. The engine uses `FSCTL_ENUM_USN_DATA` to read all MFT records, filters to the subtree rooted at the `localFS` root directory, reconstructs relative paths, and delivers them to callers. On non-Windows or when not admin, the existing `fs.WalkDir` codepath handles it unchanged.

**Tech Stack:** Go 1.26+, `golang.org/x/sys/windows` (already an indirect dependency), Windows `DeviceIoControl` / `FSCTL_ENUM_USN_DATA` / `GetFileInformationByHandle` syscalls. No CGO.

## Global Constraints

- Build tags: `//go:build windows` for MFT code, `//go:build !windows` for stubs
- All paths delivered to callers must satisfy `fs.ValidPath` (forward-slash, no leading slash, no `.` or `..` components)
- No changes to `op.go`, `ufs.go`, or any non-Windows file
- Copyright header: match existing `// Copyright 2026 Jeremy Edwards` + Apache 2.0 block from other files
- The `fsInfo` struct from `info.go` is used for FileInfo results — no new wrapper types
- `golang.org/x/sys/windows` is already an indirect dependency; if it needs to become direct, run `go mod tidy`
- Tests that require admin skip with `t.Skip` when privileges are unavailable

## File Map

| File | Action | Responsibility |
|:-----|:-------|:---------------|
| `mft_windows.go` | Create | MFT engine: constants, `mftRecord`, `mftScanner`, volume/root resolution, `FSCTL_ENUM_USN_DATA` loop, subtree filter, path construction |
| `mft_nowindows.go` | Create | Empty file with `//go:build !windows` — ensures cross-platform compilation |
| `localfs_windows.go` | Modify | Add `ForEachFilename`, `ForEachFileInfo`, `ListFilenames` methods on `localFS` |
| `mft_windows_test.go` | Create | Tests for MFT engine internals + integration tests via localFS methods |

---

### Task 1: Non-Windows Stub and MFT Engine Skeleton

Create the build-tag files and the core MFT data structures. Verify cross-platform compilation.

**Files:**
- Create: `mft_nowindows.go`
- Create: `mft_windows.go`

**Interfaces:**
- Consumes: nothing
- Produces: `mftRecord` struct, `mftScanner` struct, `newMFTScanner(rootAbsPath string) (*mftScanner, error)`, `errMFTUnavailable` sentinel error

- [ ] **Step 1: Create `mft_nowindows.go`**

```go
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

//go:build !windows

package ufs
```

- [ ] **Step 2: Create `mft_windows.go` with types and constructor**

```go
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

package ufs

import (
	"errors"
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fsctlEnumUSNData = 0x000900B3

	fileAttributeDirectory = 0x10

	usnRecordV2HeaderSize = 64
	mftEnumBufferSize     = 64 * 1024
)

var errMFTUnavailable = errors.New("MFT scan unavailable")

type mftRecord struct {
	parentRef uint64
	name      string
	isDir     bool
}

type mftScanner struct {
	volumePath string
	rootRef    uint64
	rootAbs    string
}

func newMFTScanner(rootAbsPath string) (*mftScanner, error) {
	volRoot := filepath.VolumeName(rootAbsPath)
	if volRoot == "" {
		return nil, fmt.Errorf("cannot determine volume for %q: %w", rootAbsPath, errMFTUnavailable)
	}
	volumePath := `\\.\` + volRoot

	rootRef, err := getFileReferenceNumber(rootAbsPath)
	if err != nil {
		return nil, fmt.Errorf("cannot get MFT reference for %q: %w", rootAbsPath, errMFTUnavailable)
	}

	return &mftScanner{
		volumePath: volumePath,
		rootRef:    rootRef,
		rootAbs:    rootAbsPath,
	}, nil
}

func getFileReferenceNumber(path string) (uint64, error) {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateFile(
		pathUTF16,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return 0, err
	}
	return uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow), nil
}
```

- [ ] **Step 3: Verify cross-platform build**

Run: `go build ./...`
Expected: builds without errors on the current (Linux) platform.

- [ ] **Step 4: Run `go mod tidy` if needed**

Run: `go mod tidy`

If `golang.org/x/sys` moves from indirect to direct, that is expected.

- [ ] **Step 5: Commit**

```bash
git add mft_windows.go mft_nowindows.go go.mod go.sum
git commit -m "Add MFT scanner skeleton and non-Windows stub"
```

---

### Task 2: MFT Enumeration and Subtree Filter

Implement the core `FSCTL_ENUM_USN_DATA` loop, subtree BFS filter, and path reconstruction. This is the heart of the engine.

**Files:**
- Modify: `mft_windows.go`

**Interfaces:**
- Consumes: `mftScanner` from Task 1, `getFileReferenceNumber`
- Produces: `(*mftScanner).enumSubtree() (map[uint64]mftRecord, error)`, `(*mftScanner).buildPaths(records map[uint64]mftRecord) []string`

- [ ] **Step 1: Add the `enumSubtree` method**

This method opens the volume, runs the `FSCTL_ENUM_USN_DATA` loop to collect all MFT records, then filters to the subtree rooted at `rootRef`.

Add to `mft_windows.go`:

```go
// enumSubtree enumerates MFT records on the volume and returns only those
// within the subtree rooted at s.rootRef. The returned map is keyed by file
// reference number.
func (s *mftScanner) enumSubtree() (map[uint64]mftRecord, error) {
	volumeUTF16, err := windows.UTF16PtrFromString(s.volumePath)
	if err != nil {
		return nil, err
	}
	vol, err := windows.CreateFile(
		volumeUTF16,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("open volume %s: %w", s.volumePath, errMFTUnavailable)
	}
	defer windows.CloseHandle(vol)

	allRecords, err := s.readAllRecords(vol)
	if err != nil {
		return nil, err
	}

	return s.filterSubtree(allRecords), nil
}

func (s *mftScanner) readAllRecords(vol windows.Handle) (map[uint64]mftRecord, error) {
	records := make(map[uint64]mftRecord)

	// MFT_ENUM_DATA_V0: StartFileReferenceNumber (uint64) + LowUsn (int64) + HighUsn (int64)
	enumData := make([]byte, 24)
	// StartFileReferenceNumber = 0 (start from beginning)
	// LowUsn = 0
	// HighUsn = max int64
	*(*int64)(unsafe.Pointer(&enumData[16])) = 1<<63 - 1

	buf := make([]byte, mftEnumBufferSize)

	for {
		var bytesReturned uint32
		err := windows.DeviceIoControl(
			vol,
			fsctlEnumUSNData,
			&enumData[0], uint32(len(enumData)),
			&buf[0], uint32(len(buf)),
			&bytesReturned, nil,
		)
		if err != nil {
			if errors.Is(err, windows.ERROR_HANDLE_EOF) {
				break
			}
			return nil, fmt.Errorf("FSCTL_ENUM_USN_DATA: %w", err)
		}
		if bytesReturned <= 8 {
			break
		}

		// First 8 bytes of output is the next StartFileReferenceNumber.
		copy(enumData[:8], buf[:8])

		offset := uint32(8)
		for offset < bytesReturned {
			if offset+4 > bytesReturned {
				break
			}
			recordLen := *(*uint32)(unsafe.Pointer(&buf[offset]))
			if recordLen == 0 || offset+recordLen > bytesReturned {
				break
			}
			rec := parseUSNRecordV2(buf[offset : offset+recordLen])
			if rec != nil {
				records[rec.fileRef] = mftRecord{
					parentRef: rec.parentRef,
					name:      rec.name,
					isDir:     rec.isDir,
				}
			}
			offset += recordLen
		}
	}

	return records, nil
}

type rawUSNRecord struct {
	fileRef   uint64
	parentRef uint64
	name      string
	isDir     bool
}

func parseUSNRecordV2(data []byte) *rawUSNRecord {
	if len(data) < usnRecordV2HeaderSize {
		return nil
	}
	fileRef := *(*uint64)(unsafe.Pointer(&data[8]))
	parentRef := *(*uint64)(unsafe.Pointer(&data[16]))
	fileNameOffset := *(*uint16)(unsafe.Pointer(&data[58]))
	fileNameLength := *(*uint16)(unsafe.Pointer(&data[56]))
	fileAttributes := *(*uint32)(unsafe.Pointer(&data[52]))

	nameStart := int(fileNameOffset)
	nameEnd := nameStart + int(fileNameLength)
	if nameEnd > len(data) || nameStart >= nameEnd {
		return nil
	}
	nameUTF16 := unsafe.Slice((*uint16)(unsafe.Pointer(&data[nameStart])), fileNameLength/2)
	name := windows.UTF16ToString(nameUTF16)

	return &rawUSNRecord{
		fileRef:   fileRef,
		parentRef: parentRef,
		name:      name,
		isDir:     fileAttributes&fileAttributeDirectory != 0,
	}
}

func (s *mftScanner) filterSubtree(allRecords map[uint64]mftRecord) map[uint64]mftRecord {
	// Build children adjacency list.
	children := make(map[uint64][]uint64, len(allRecords))
	for ref, rec := range allRecords {
		children[rec.parentRef] = append(children[rec.parentRef], ref)
	}

	// BFS from rootRef to find reachable records.
	reachable := make(map[uint64]mftRecord)
	queue := []uint64{s.rootRef}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, childRef := range children[cur] {
			if _, already := reachable[childRef]; already {
				continue
			}
			rec := allRecords[childRef]
			reachable[childRef] = rec
			if rec.isDir {
				queue = append(queue, childRef)
			}
		}
	}

	return reachable
}
```

- [ ] **Step 2: Add the `buildPaths` method**

This method walks the parent chain for each non-directory record in the subtree to reconstruct forward-slash relative paths.

Add to `mft_windows.go`:

```go
// buildPaths reconstructs forward-slash relative paths for every non-directory
// record in the subtree. Paths are relative to the scanner's root directory and
// satisfy fs.ValidPath.
func (s *mftScanner) buildPaths(records map[uint64]mftRecord) []string {
	var paths []string
	for ref, rec := range records {
		if rec.isDir {
			continue
		}
		p := s.buildPath(ref, records)
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

func (s *mftScanner) buildPath(ref uint64, records map[uint64]mftRecord) string {
	var components []string
	cur := ref
	for i := 0; i < 4096; i++ {
		rec, ok := records[cur]
		if !ok {
			// Reached the root (rootRef itself is not in the filtered set).
			break
		}
		components = append(components, rec.name)
		cur = rec.parentRef
	}
	if len(components) == 0 {
		return ""
	}
	// Reverse to root-first order.
	for i, j := 0, len(components)-1; i < j; i, j = i+1, j-1 {
		components[i], components[j] = components[j], components[i]
	}
	return joinFSPath(components)
}

func joinFSPath(components []string) string {
	n := 0
	for _, c := range components {
		n += len(c)
	}
	n += len(components) - 1 // separators
	buf := make([]byte, 0, n)
	for i, c := range components {
		if i > 0 {
			buf = append(buf, '/')
		}
		buf = append(buf, c...)
	}
	return string(buf)
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: builds without errors.

- [ ] **Step 4: Commit**

```bash
git add mft_windows.go
git commit -m "Implement MFT enumeration, subtree filter, and path construction"
```

---

### Task 3: localFS Iterator Methods and Fallback

Wire the MFT engine into `localFS` by adding the three iterator methods. Each method attempts MFT first, falls back to `fs.WalkDir` on failure.

**Files:**
- Modify: `localfs_windows.go`

**Interfaces:**
- Consumes: `newMFTScanner(rootAbsPath string) (*mftScanner, error)`, `(*mftScanner).enumSubtree()`, `(*mftScanner).buildPaths()`, `errMFTUnavailable`, `fsInfo` from `info.go`, `excludeDirs` from `op.go`
- Produces: `(*localFS).ForEachFilename(dir string, f func(string) error) error`, `(*localFS).ForEachFileInfo(dir string, f func(fs.FileInfo) error) error`, `(*localFS).ListFilenames(dir string) ([]string, error)` — satisfying `ForEachFilenameIter`, `ForEachFileInfoIter`, `ListFilenames` interfaces from `ufs.go`

- [ ] **Step 1: Add interface compile-time assertions**

Add to the `var` block at the top of `localfs_windows.go` (the file already has a similar block for `localFSInterface`):

In the existing file, there's no `var` block with assertions. Add these lines below the import block at package level:

```go
var (
	_ ForEachFilenameIter = (*localFS)(nil)
	_ ForEachFileInfoIter = (*localFS)(nil)
	_ ListFilenames       = (*localFS)(nil)
)
```

Note: the `ListFilenames` interface in `ufs.go` is named `ListFilenames` (both the interface and the method share the same name). The assertion `_ ListFilenames = (*localFS)(nil)` verifies `localFS` implements the `ListFilenames` interface which requires the `ListFilenames(string) ([]string, error)` method.

- [ ] **Step 2: Add the `mftScanPaths` internal helper**

This shared helper runs the MFT scan and returns paths, or returns `errMFTUnavailable` to signal fallback. Add to `localfs_windows.go`:

```go
func (fsys *localFS) mftScanPaths(dir string) ([]string, error) {
	absRoot, err := fsys.getAbsPath(dir)
	if err != nil {
		return nil, errMFTUnavailable
	}
	scanner, err := newMFTScanner(absRoot)
	if err != nil {
		return nil, errMFTUnavailable
	}
	records, err := scanner.enumSubtree()
	if err != nil {
		if errors.Is(err, errMFTUnavailable) {
			return nil, errMFTUnavailable
		}
		return nil, err
	}
	return scanner.buildPaths(records), nil
}
```

- [ ] **Step 3: Add `ForEachFilename` method**

```go
func (fsys *localFS) ForEachFilename(dir string, f func(string) error) error {
	paths, err := fsys.mftScanPaths(dir)
	if errors.Is(err, errMFTUnavailable) {
		return fsys.forEachFilenameWalkDir(dir, f)
	}
	if err != nil {
		return err
	}
	for _, p := range paths {
		if err := f(p); err != nil {
			return err
		}
	}
	return nil
}

func (fsys *localFS) forEachFilenameWalkDir(dir string, f func(string) error) error {
	return fs.WalkDir(fsys, dir, func(name string, d fs.DirEntry, err error) error {
		if skip, err := excludeDirs(name, d, err); skip {
			return err
		}
		return f(name)
	})
}
```

- [ ] **Step 4: Add `ForEachFileInfo` method**

```go
func (fsys *localFS) ForEachFileInfo(dir string, f func(fs.FileInfo) error) error {
	paths, err := fsys.mftScanPaths(dir)
	if errors.Is(err, errMFTUnavailable) {
		return fsys.forEachFileInfoWalkDir(dir, f)
	}
	if err != nil {
		return err
	}
	absRoot := fsys.osFS.Name()
	for _, p := range paths {
		info, statErr := os.Stat(filepath.Join(absRoot, filepath.FromSlash(p)))
		if statErr != nil {
			return statErr
		}
		if err := f(info); err != nil {
			return err
		}
	}
	return nil
}

func (fsys *localFS) forEachFileInfoWalkDir(dir string, f func(fs.FileInfo) error) error {
	return fs.WalkDir(fsys, dir, func(name string, d fs.DirEntry, err error) error {
		if skip, err := excludeDirs(name, d, err); skip {
			return err
		}
		info, statErr := fs.Stat(fsys, name)
		if statErr != nil {
			return statErr
		}
		return f(info)
	})
}
```

- [ ] **Step 5: Add `ListFilenames` method**

```go
func (fsys *localFS) ListFilenames(dir string) ([]string, error) {
	paths, err := fsys.mftScanPaths(dir)
	if errors.Is(err, errMFTUnavailable) {
		return fsys.listFilenamesWalkDir(dir)
	}
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func (fsys *localFS) listFilenamesWalkDir(dir string) ([]string, error) {
	var items []string
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if isCwd(p) {
			return nil
		}
		if err != nil {
			return err
		}
		if !d.IsDir() {
			items = append(items, p)
		}
		return nil
	})
	return items, err
}
```

- [ ] **Step 6: Add missing imports to `localfs_windows.go`**

The file's import block needs these additions (merge with existing imports):

```go
import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)
```

- [ ] **Step 7: Verify build**

Run: `go build ./...`
Expected: builds without errors. The compile-time assertions verify `localFS` satisfies all three interfaces.

- [ ] **Step 8: Commit**

```bash
git add localfs_windows.go
git commit -m "Add ForEachFilename, ForEachFileInfo, ListFilenames to localFS on Windows"
```

---

### Task 4: Tests

Write tests that verify MFT scanning produces correct results and that fallback works. Tests require Windows + admin and self-skip otherwise.

**Files:**
- Create: `mft_windows_test.go`

**Interfaces:**
- Consumes: `newMFTScanner`, `(*mftScanner).enumSubtree`, `(*mftScanner).buildPaths`, `(*localFS).ForEachFilename`, `(*localFS).ForEachFileInfo`, `(*localFS).ListFilenames`, `newLocalFS`, `makeLocalFS` from `localfs.go`
- Produces: nothing (test-only)

- [ ] **Step 1: Create `mft_windows_test.go` with helper and round-trip test**

```go
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

package ufs

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// skipIfNotAdmin attempts to open the volume handle for the temp directory.
// If it fails, the test is skipped.
func skipIfNotAdmin(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	absPath, err := filepath.Abs(tmpDir)
	if err != nil {
		t.Skipf("cannot resolve temp dir: %v", err)
	}
	_, err = newMFTScanner(absPath)
	if err != nil {
		t.Skipf("MFT unavailable (not admin or not NTFS): %v", err)
	}
	return absPath
}

func createTestTree(t *testing.T, root string) {
	t.Helper()
	dirs := []string{
		"a",
		"a/b",
		"a/b/c",
		"d",
	}
	files := []string{
		"top.txt",
		"a/one.txt",
		"a/b/two.txt",
		"a/b/c/three.txt",
		"d/four.txt",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(root, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// walkDirFiles returns all file paths under root using fs.WalkDir, for comparison.
func walkDirFiles(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	var files []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == "." || d.IsDir() {
			return err
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

func TestMFTScannerRoundTrip(t *testing.T) {
	absPath := skipIfNotAdmin(t)
	createTestTree(t, absPath)

	scanner, err := newMFTScanner(absPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := scanner.enumSubtree()
	if err != nil {
		t.Fatal(err)
	}
	got := scanner.buildPaths(records)
	sort.Strings(got)

	lfs, err := makeLocalFS("file://" + filepath.ToSlash(absPath))
	if err != nil {
		t.Fatal(err)
	}
	defer lfs.Close()
	want := walkDirFiles(t, lfs)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MFT scan vs WalkDir mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 2: Add `ForEachFilename` integration test**

Append to `mft_windows_test.go`:

```go
func TestLocalFSForEachFilename(t *testing.T) {
	absPath := skipIfNotAdmin(t)
	createTestTree(t, absPath)

	lfs, err := makeLocalFS("file://" + filepath.ToSlash(absPath))
	if err != nil {
		t.Fatal(err)
	}
	defer lfs.Close()

	var got []string
	err = lfs.ForEachFilename(".", func(name string) error {
		got = append(got, name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := walkDirFiles(t, lfs)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ForEachFilename vs WalkDir mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 3: Add `ForEachFileInfo` integration test**

Append to `mft_windows_test.go`:

```go
func TestLocalFSForEachFileInfo(t *testing.T) {
	absPath := skipIfNotAdmin(t)
	createTestTree(t, absPath)

	lfs, err := makeLocalFS("file://" + filepath.ToSlash(absPath))
	if err != nil {
		t.Fatal(err)
	}
	defer lfs.Close()

	got := map[string]int64{}
	err = lfs.ForEachFileInfo(".", func(info fs.FileInfo) error {
		got[info.Name()] = info.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]int64{
		"top.txt":   int64(len("top.txt")),
		"one.txt":   int64(len("a/one.txt")),
		"two.txt":   int64(len("a/b/two.txt")),
		"three.txt": int64(len("a/b/c/three.txt")),
		"four.txt":  int64(len("d/four.txt")),
	}
	// Verify all expected files are present with correct sizes.
	for name, wantSize := range want {
		gotSize, ok := got[name]
		if !ok {
			t.Errorf("missing file %q in ForEachFileInfo results", name)
			continue
		}
		if gotSize != wantSize {
			t.Errorf("file %q: got size %d, want %d", name, gotSize, wantSize)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d files, want %d", len(got), len(want))
	}
}
```

- [ ] **Step 4: Add `ListFilenames` integration test**

Append to `mft_windows_test.go`:

```go
func TestLocalFSListFilenames(t *testing.T) {
	absPath := skipIfNotAdmin(t)
	createTestTree(t, absPath)

	lfs, err := makeLocalFS("file://" + filepath.ToSlash(absPath))
	if err != nil {
		t.Fatal(err)
	}
	defer lfs.Close()

	got, err := lfs.ListFilenames(".")
	if err != nil {
		t.Fatal(err)
	}
	// ListFilenames returns sorted results.
	want := walkDirFiles(t, lfs)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ListFilenames vs WalkDir mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 5: Add subtree isolation test**

Append to `mft_windows_test.go`:

```go
func TestMFTScannerSubtreeIsolation(t *testing.T) {
	absPath := skipIfNotAdmin(t)

	// Create two sibling directories: "inside" (our root) and "outside".
	inside := filepath.Join(absPath, "inside")
	outside := filepath.Join(absPath, "outside")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inside, "yes.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "no.txt"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scanner, err := newMFTScanner(inside)
	if err != nil {
		t.Fatal(err)
	}
	records, err := scanner.enumSubtree()
	if err != nil {
		t.Fatal(err)
	}
	paths := scanner.buildPaths(records)

	for _, p := range paths {
		if p == "no.txt" || p == "outside/no.txt" {
			t.Errorf("subtree scan returned file outside root: %q", p)
		}
	}
	found := false
	for _, p := range paths {
		if p == "yes.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("subtree scan did not return expected file yes.txt, got: %v", paths)
	}
}
```

- [ ] **Step 6: Add `joinFSPath` unit test (runs on all platforms)**

Create a small test in `mft_windows_test.go`:

```go
func TestJoinFSPath(t *testing.T) {
	tests := []struct {
		input []string
		want  string
	}{
		{[]string{"a"}, "a"},
		{[]string{"a", "b", "c.txt"}, "a/b/c.txt"},
		{[]string{"top.txt"}, "top.txt"},
	}
	for _, tc := range tests {
		got := joinFSPath(tc.input)
		if got != tc.want {
			t.Errorf("joinFSPath(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
```

- [ ] **Step 7: Verify build**

Run: `go build ./...`
Expected: builds without errors (tests won't run on Linux, but the non-test code must still compile).

- [ ] **Step 8: Commit**

```bash
git add mft_windows_test.go
git commit -m "Add MFT scanner tests for round-trip, integration, and subtree isolation"
```

---

### Task 5: Final Verification and Cleanup

Verify everything builds cleanly on the current platform, run the linter, and ensure no regressions in existing tests.

**Files:**
- No new files

**Interfaces:**
- Consumes: all previous tasks
- Produces: nothing (verification only)

- [ ] **Step 1: Verify cross-platform build**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 2: Run linter**

Run: `golangci-lint run`
Expected: no new warnings from the added files. Fix any issues (unused imports, formatting, etc.).

- [ ] **Step 3: Run existing tests**

Run: `make test`
Expected: all existing tests pass. MFT tests are skipped on Linux (build tag excludes them).

- [ ] **Step 4: Verify interface satisfaction**

The compile-time assertions in `localfs_windows.go` ensure `localFS` satisfies `ForEachFilenameIter`, `ForEachFileInfoIter`, and `ListFilenames` on Windows. The `go build ./...` in step 1 validates this via cross-compilation. If you have access to a Windows machine, run:

```
go test -run TestMFTScanner -v -count=1
go test -run TestLocalFS -v -count=1
```

Expected: tests pass when running as admin, skip when not.

- [ ] **Step 5: Commit any lint fixes**

```bash
git add -A
git commit -m "Fix lint issues in MFT scanner"
```

Skip this step if there are no lint issues.
