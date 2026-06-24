# MFT-Accelerated File Scanning for localFS on Windows

**Date:** 2026-06-24
**Status:** Approved
**Scope:** Windows-only performance optimization for file tree enumeration

## Problem

`localFS` scans file trees via `fs.WalkDir`, which calls `ReadDir` directory-by-directory. Each directory is a `FindFirstFile`/`FindNextFile` syscall round-trip. On volumes with millions of files this takes minutes.

On Windows NTFS, the Master File Table (MFT) contains every file record on the volume. Reading it via `FSCTL_ENUM_USN_DATA` enumerates all files in seconds but requires administrator privileges. This feature adds an MFT-based fast path to `localFS` that activates automatically when running as admin on NTFS, with silent fallback to `WalkDir` otherwise.

## Design

### Integration Point

`localFS` gains three new methods on Windows, implementing the existing iterator interfaces from `ufs.go`:

- `ForEachFilename(dir string, f func(string) error) error` — implements `ForEachFilenameIter`
- `ForEachFileInfo(dir string, f func(fs.FileInfo) error) error` — implements `ForEachFileInfoIter`
- `ListFilenames(dir string) ([]string, error)` — implements `ListFilenames`

These are the fast-path hooks that `op.go` already checks. Today `localFS` does not implement them, so `ForEachFilename()`, `ForEachFileInfo()`, and `ListFiles()` in `op.go` all fall through to `fs.WalkDir`. Once `localFS` implements these interfaces, callers get the speedup with zero API changes.

### Two Scan Modes

**Name-only** (`ForEachFilenameIter`, `ListFilenames`): Pure MFT enumeration. No per-file syscalls. Returns forward-slash relative paths satisfying `fs.ValidPath`.

**Full info** (`ForEachFileInfoIter`): MFT for path discovery, then `os.Stat` per file to populate a complete `fsInfo` (name, size, mode, modTime, isDir). Still faster than `WalkDir` because path discovery is flat (single `DeviceIoControl` loop) rather than tree-recursive. The `fsInfo` struct is the same one the codebase already uses — no wrapper type, no extra allocation beyond what `os.Stat` returns.

### Fallback Strategy

Each method attempts the MFT path first. If it fails (access denied, non-NTFS volume, `DeviceIoControl` error), the method falls back to `fs.WalkDir` internally — the same codepath `op.go` uses today. The fallback is silent; no error is surfaced to callers.

This means the methods always exist on Windows builds (`//go:build windows`), but whether they use MFT or WalkDir is a runtime decision. On non-Windows platforms, the methods do not exist (`mft_nowindows.go` is empty), so `op.go` falls through to `fs.WalkDir` at the type-assertion level.

## File Layout

| File | Build tag | Purpose |
|:-----|:----------|:--------|
| `mft_windows.go` | `windows` | MFT enumeration engine: volume handle management, `FSCTL_ENUM_USN_DATA` loop, parent-chain resolver, subtree filter |
| `mft_nowindows.go` | `!windows` | Empty file — ensures cross-platform compilation |
| `localfs_windows.go` | `windows` | Gains `ForEachFilename`, `ForEachFileInfo`, `ListFilenames` methods on `localFS` that delegate to the MFT engine |

No changes to `op.go`, `ufs.go`, `nestfs.go`, or any non-Windows file.

## MFT Engine

### Core Type

```go
type mftScanner struct {
    volumePath string   // e.g. "\\.\C:"
    rootRef    uint64   // MFT reference number of the localFS root directory
    rootAbs    string   // absolute OS path of the localFS root, for stat calls
}
```

### Enumeration Flow

1. **Resolve volume**: From `localFS.osFS.Name()` (e.g. `C:\data`), extract the volume root (`C:\`) and construct the volume device path (`\\.\C:`).

2. **Open volume handle**: `CreateFile(volumePath, GENERIC_READ, FILE_SHARE_READ|FILE_SHARE_WRITE, ...)`. If this returns `ERROR_ACCESS_DENIED`, the caller catches it and falls back to `WalkDir`. This is the privilege gate — no separate admin check is needed.

3. **Resolve root reference number**: `CreateFile` the root directory of the `localFS` (e.g. `C:\data`), then `DeviceIoControl(FSCTL_GET_NTFS_FILE_RECORD)` or `GetFileInformationByHandle` to obtain its MFT file reference number. This anchors the subtree filter.

4. **Enumerate MFT**: `DeviceIoControl(FSCTL_ENUM_USN_DATA)` in a loop with a 64KB buffer. Each call returns a batch of `USN_RECORD_V2` structs containing:
   - `FileReferenceNumber` (uint64)
   - `ParentFileReferenceNumber` (uint64)
   - `FileName` (UTF-16, variable length)
   - `FileAttributes` (FILE_ATTRIBUTE_DIRECTORY, etc.)

   All records are inserted into `map[uint64]mftRecord`.

5. **Subtree filter** (two-pass):
   - **Collect pass**: Already done in step 4 — every record on the volume is in the map.
   - **Mark pass**: Build a children map (`map[uint64][]uint64`) by inverting parent references. BFS from `rootRef` through the children map. Records not reachable from `rootRef` are deleted.

6. **Path construction**: For each surviving non-directory record, walk from the record up the parent chain to `rootRef`, accumulating name components. Reverse to get the forward path. Convert backslashes to forward slashes to satisfy `fs.ValidPath`.

7. **Delivery**:
   - Name-only mode: call `f(relativePath)` for each file.
   - Full-info mode: call `os.Stat(filepath.Join(rootAbs, relativePath))`, construct `fsInfo`, call `f(info)`. If stat fails, return the error (same contract as `WalkDir`).

### Key Data Structure

```go
type mftRecord struct {
    parentRef uint64
    name      string
    isDir     bool
}
```

One entry per file/directory on the volume during enumeration, pruned to the subtree after the mark pass. For a volume with 1M files and a subtree of 100K files, peak memory is ~1M entries (~80 bytes each = ~80MB), dropping to ~100K entries after pruning.

### Subtree Scoping Detail

MFT records do not arrive in parent-before-child order, so parent-chain resolution cannot happen during the enumeration loop. The two-pass approach handles this:

1. All records go into the flat map during enumeration.
2. After enumeration completes, build the children adjacency list and BFS from `rootRef`.
3. Delete unreachable records.

This is O(n) in the total number of files on the volume for the collect pass, and O(m) for the mark pass where m is the subtree size.

## Error Handling

| Condition | Behavior |
|:----------|:---------|
| Volume handle open fails (`ERROR_ACCESS_DENIED`) | Silent fallback to `WalkDir` |
| Non-NTFS volume (FAT32, exFAT, ReFS) | `FSCTL_ENUM_USN_DATA` fails -> silent fallback to `WalkDir` |
| `DeviceIoControl` fails mid-enumeration | Return error (partial MFT reads are not usable) |
| `os.Stat` fails in full-info mode | Return error to caller (same contract as `WalkDir`) |
| `localFS` root path not resolvable to a volume | Silent fallback to `WalkDir` |

## Windows Syscall Surface

All syscalls are via `golang.org/x/sys/windows` (already available as an indirect dependency) or direct `syscall.NewLazyDLL`/`syscall.NewProc` calls. No CGO required.

| Syscall / IOCTL | Purpose |
|:----------------|:--------|
| `CreateFileW` | Open volume handle (`\\.\C:`) and root directory handle |
| `DeviceIoControl` + `FSCTL_ENUM_USN_DATA` | Bulk MFT enumeration |
| `GetFileInformationByHandle` | Get MFT reference number for the root directory |
| `CloseHandle` | Close volume and directory handles |

Constants (`FSCTL_ENUM_USN_DATA`, `USN_RECORD_V2` layout, `FILE_ATTRIBUTE_DIRECTORY`) are defined locally in `mft_windows.go` if not present in `x/sys/windows`.

## Testing

### Unit Tests (`mft_windows_test.go`)

Build tag: `//go:build windows`

Tests skip with `t.Skip("requires admin")` if the volume handle open fails. When running as admin:

1. **Round-trip test**: Create a temp directory tree with known files. Scan via MFT `ForEachFilename`. Verify results match `fs.WalkDir` output (same set of paths, order may differ — sort both before comparing).

2. **Full-info test**: Same setup. Scan via `ForEachFileInfo`. Verify each returned `fsInfo` matches `os.Stat` for that path (name, size, isDir, modTime within 1-second tolerance).

3. **Subtree isolation test**: Create files outside the `localFS` root on the same volume. Verify they do not appear in MFT scan results.

4. **Fallback test**: Temporarily revoke privilege (or mock the volume open to fail). Verify the method still returns correct results via `WalkDir` fallback.

### Cross-Platform

`mft_nowindows.go` ensures the package compiles on all platforms. The existing `testFileSystem` harness exercises `ReadDir`/`Stat`/`Open` which are unaffected by this change. No modifications to the shared test harness.

### CI

Windows CI runners need admin privileges to exercise MFT tests. Non-admin runners still run all other tests; MFT tests self-skip.

## Performance Expectations

For a subtree of ~1M files on a volume with ~2M total files:

| Mode | WalkDir | MFT |
|:-----|:--------|:----|
| Name-only | 30-120s | 2-5s |
| Full info | 30-120s | 10-30s |

The name-only speedup is 10-60x. Full-info is 3-10x (bottlenecked by per-file `os.Stat`). The speedup is most pronounced on deep directory trees where `WalkDir` pays a syscall per directory; MFT enumeration is flat regardless of tree depth.

## Non-Goals

- **Incremental / cached scanning via USN journal**: Out of scope. Each scan reads the MFT fresh. Caching and change-journal integration can be added later.
- **Write operations**: MFT scan is read-only enumeration. File creation, deletion, and modification continue through `os.Root`.
- **Non-NTFS volumes**: No acceleration for FAT32, exFAT, or ReFS. Silent fallback.
- **Non-admin execution**: No privilege escalation. If the process isn't admin, `WalkDir` is used.
