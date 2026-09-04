# ProjFS Read-Write Mount — Design Spec

## Goal

Add read-write support to the existing ProjFS Windows host mount so that
file creation, modification, deletion, and renaming through the OS mount
point are synced back to the backing `ufs.FS`. When the backing FS is
read-only (`ReadFS`), behavior is unchanged (destructive operations are
denied).

## Architecture

ProjFS cannot intercept writes in-flight — writes land on the local NTFS
directory. ProjFS does deliver post-notifications when files are created,
modified, deleted, or renamed. The write-back model uses these
notifications to sync local NTFS state back into the backing `FS`.

### Data flow

```text
User writes file via OS
  → File materializes on local NTFS (ProjFS can't prevent this)
  → ProjFS fires post-notification (NEW_FILE_CREATED, FILE_HANDLE_CLOSED_FILE_MODIFIED, etc.)
  → notificationCB reads the file from the local mount directory
  → notificationCB writes it into the backing FS via Create/MkdirAll/Remove
```

For deletes, ProjFS fires `PRE_DELETE` *before* the delete occurs. The
callback can allow or deny it. For a writable FS, the callback calls
`Remove` on the backing FS and returns `S_OK` to allow the delete.

For renames, the callback handles the post-rename notification
(`FILE_RENAMED`): sync the file at the new path into the backing FS and
remove the old path.

## Constraints

- No new dependencies. Pure Go syscall bindings only.
- No new public API. The existing `HostMount(ctx, fsys, mountPath)` already
  accepts `ReadFS` and checks for `FS` — this is the mechanism for
  enabling read-write.
- No changes to `host.go`, `ufs.go`, or any interface.
- Must not break existing read-only behavior or existing tests (including
  the 9 read-only conformance tests from PR #225).
- The existing `slog.Debug` logging in callbacks (PR #226) must be
  preserved and extended for new write-path code.
- Design must not preclude a future WinFSP backend (WinFSP would provide
  synchronous write interception via a FUSE-like driver, sharing the same
  `FS` interface and `writable` field pattern).

## Changes

### projfsMountServer struct

```go
type projfsMountServer struct {
    fsys         ReadFS
    writable     FS        // nil when fsys does not implement FS (read-only)
    mountPath    string    // local NTFS path, for reading back modified files
    nsCtx        uintptr
    enumSessions sync.Map
    done         chan struct{}
    closeOnce    sync.Once
}
```

New fields: `writable` and `mountPath`.

### hostMount function

- Type-assert `fsys` to `FS`; store in `s.writable` if it succeeds.
- Store `mountPath` in `s.mountPath`.
- When `writable != nil`, expand the notification bitmask:

  ```text
  prjNotifyPreDelete |
  prjNotifyPreRename |          // deny on read-only; allow+remove on writable
  prjNotifyNewFileCreated |     // sync new file to backing FS
  prjNotifyFileOverwritten |    // sync overwritten file to backing FS
  prjNotifyFileRenamed |        // sync rename: create new, remove old
  prjNotifyFileHandleClosedFileModified |  // sync modified file
  prjNotifyFileHandleClosedFileDeleted |   // defensive cleanup
  prjNotifyFilePreConvertToFull            // allow conversion
  ```

- When `writable == nil`, keep the existing bitmask: `prjNotifyPreDelete | prjNotifyPreRename`.

### notificationCB

Read-only path (unchanged):

- `PRE_DELETE`, `PRE_RENAME` → return `hresultAccessDenied`
- All others → return `hresultOK`

Read-write path (new):

- `NEW_FILE_CREATED` → `s.syncFromLocal(path, isDir)`
- `FILE_OVERWRITTEN` → `s.syncFromLocal(path, false)`
- `FILE_HANDLE_CLOSED_FILE_MODIFIED` → `s.syncFromLocal(path, false)`
- `PRE_DELETE` → `s.handleDelete(path)` — calls `writable.Remove`, returns `S_OK` on success
- `FILE_RENAMED` → `s.handleRename(oldPath, newPath)` — sync new path, remove old path
- `FILE_HANDLE_CLOSED_FILE_DELETED` → `hresultOK` (PRE_DELETE already handled)
- `FILE_PRE_CONVERT_TO_FULL` → `hresultOK` (allow placeholder-to-full conversion)

### syncFromLocal method

```go
func (s *projfsMountServer) syncFromLocal(name string, isDir bool) uintptr
```

- If `isDir`: call `s.writable.MkdirAll(name, 0o755)`.
- If file: open `filepath.Join(s.mountPath, windowsPath)` from local NTFS,
  create parent directories with `MkdirAll` if needed,
  call `s.writable.Create(name)`, `io.Copy` the content, close both.
- Return `hresultOK` on success, `projfsHRESULT(err)` on failure.

### handleDelete method

```go
func (s *projfsMountServer) handleDelete(name string) uintptr
```

- Call `s.writable.Remove(name)`.
- Return `hresultOK` on success (allows the delete to proceed).
- Return `projfsHRESULT(err)` on failure (denies the delete).

### handleRename method

```go
func (s *projfsMountServer) handleRename(oldPath string, newPath string, isDir bool) uintptr
```

ProjFS delivers `FILE_RENAMED` with the old path in `callbackData.FilePathName`
and the new path in `destinationFileName`. The rename has already occurred on
the local NTFS by the time this notification fires.

- Call `s.syncFromLocal(newPath, isDir)` to sync the file at its new location.
- Call `s.writable.Remove(oldPath)` to remove the old path from the backing FS.
  Ignore `ErrNotExist` (the old path may not have been in the backing FS if it
  was a locally-created file that was renamed before sync).
- Return `hresultOK` on success.

### File permissions and stat consistency

ProjFS placeholders carry `prjFileBasicInfo` which maps to Windows
concepts: `FileAttributes`, four timestamps, and `FileSize`. The Go
`fs.FileInfo` interface provides Unix-style `Mode()` bits. The mapping:

| fs.FileInfo field | ProjFS placeholder field | Mapping |
|-------------------|--------------------------|---------|
| `IsDir()` | `IsDirectory`, `FILE_ATTRIBUTE_DIRECTORY` | Direct |
| `Size()` | `FileSize` | Direct |
| `ModTime()` | `CreationTime`, `LastAccessTime`, `LastWriteTime`, `ChangeTime` | All four set to `ModTime` |
| `Mode() & 0o222 == 0` | `FILE_ATTRIBUTE_READONLY` | **New** — set when no write bits |
| `Mode()` (other bits) | Not mapped | Windows ACLs are orthogonal to Unix mode bits |
| Security descriptor | `SecurityInformation` | Zeroed — inherits parent directory ACL |

Current behavior (read-only mount): all files get `FILE_ATTRIBUTE_NORMAL`
and all directories get `FILE_ATTRIBUTE_DIRECTORY`. This works because
projected files are inherently read-only (ProjFS only materializes a copy
on write).

For read-write: the `FILE_ATTRIBUTE_READONLY` mapping matters. A file
whose backing `FileInfo.Mode()` has no write bits should appear read-only
in Windows Explorer and reject writes at the OS level. The implementation
should add this mapping in `getPlaceholderCB` and `getDirEnumCB`:

```go
if fi.Mode().Perm()&0o222 == 0 {
    fbi.FileAttributes |= 0x01 // FILE_ATTRIBUTE_READONLY
}
```

**On write-back** (`syncFromLocal`): the file is read from local NTFS
where Windows has set its own attributes and ACLs. The backing `FS.Create`
interface does not accept permissions — it returns a writer. This is
acceptable because:

1. The backing FS implementations (memFS, localFS, gcsFS) don't enforce
   Windows-style ACLs.
2. The `FS` interface has no `Chmod` or permission parameter on `Create`.
3. The file's content is the authoritative state; attributes are
   re-derived from the backing `FileInfo` on each placeholder request.

**Timestamps on write-back**: When `syncFromLocal` copies a file, the
backing FS records its own timestamps (typically `time.Now()`). The next
time ProjFS requests a placeholder for the same path, it gets the updated
timestamps from the backing `FileInfo`. This is correct — the file was
modified, so its timestamps should advance.

### getDirEnumCB — merge local and projected entries

When the FS is writable, a directory listing must include both projected
files from the backing FS and files that were created locally on NTFS but
haven't been synced yet (or were synced but ProjFS still tracks them as
"full" files). ProjFS handles this automatically — it merges placeholder
entries from the provider with full/tombstone entries on the local NTFS.
No change to `getDirEnumCB` is needed.

## Files modified

| File | Change |
|------|--------|
| `mount_windows.go` | Add `writable`/`mountPath` fields, expand notification bitmask, implement `syncFromLocal`/`handleDelete`/`handleRename`, update `notificationCB` |
| `mount_windows_test.go` | Add ~44 ProjFS-specific write integration tests (see test matrix below) |
| `mount_conformance_test.go` | Add write conformance tests (`TestMountConformance*`) using `writeMountBackends` table |
| `mount_conformance_linux_test.go` | Register FUSE write backend |
| `mount_conformance_windows_test.go` | Register ProjFS write backend |
| `fuse_linux_test.go` | Remove write tests that move to conformance |
| `docs/ufsmount.md` | Update Windows supported operations table |

## Test matrix

All tests are in `mount_windows_test.go` with `//go:build windows`. They
mount a writable backing FS (memFS or localFS) via ProjFS and exercise
operations through `os.*` calls against the mount directory, then verify
state in the backing FS.

A future PR (#222) will extract these into shared cross-platform functions.

### Create file (10 tests)

| # | Test | Scenario |
|---|------|----------|
| 1 | TestHostMountCreateFile | Create file, verify content in backing FS |
| 2 | TestHostMountCreateFileEmpty | Create zero-byte file, verify exists |
| 3 | TestHostMountCreateFileLarge | Write 256KB, verify byte-for-byte |
| 4 | TestHostMountCreateFileNested | Create in existing subdirectory |
| 5 | TestHostMountCreateFileDeepNew | Create `a/b/c/file.txt` where dirs don't exist |
| 6 | TestHostMountCreateFileBinaryContent | Write bytes 0x00-0xFF including nulls |
| 7 | TestHostMountCreateFileUnicodeName | File with Unicode name (e.g., `日本語.txt`) |
| 8 | TestHostMountCreateFileSpacesInName | Filename with spaces and special chars |
| 9 | TestHostMountCreateFileOverwriteProjected | Overwrite existing projected file |
| 10 | TestHostMountCreateMultipleFiles | Create 50 files rapidly, verify all in backing FS |

### Modify file (7 tests)

| # | Test | Scenario |
|---|------|----------|
| 11 | TestHostMountModifyFile | Modify projected file, verify new content |
| 12 | TestHostMountModifyFileGrow | 10 bytes → 10KB |
| 13 | TestHostMountModifyFileShrink | 10KB → 10 bytes |
| 14 | TestHostMountModifyFileToEmpty | Overwrite with zero bytes |
| 15 | TestHostMountModifyFilePreservesOthers | Modify one, verify others unchanged |
| 16 | TestHostMountModifyFileTwice | Write, close, write again, verify final |
| 17 | TestHostMountModifyFileAppend | Open for append, verify original + appended |

### Delete (8 tests)

| # | Test | Scenario |
|---|------|----------|
| 18 | TestHostMountDeleteFile | Delete projected file, verify gone |
| 19 | TestHostMountDeleteFileNotExist | Delete non-existent file, expect OS error |
| 20 | TestHostMountDeleteDir | Delete empty directory |
| 21 | TestHostMountDeleteDirNonEmpty | Delete non-empty dir, expect OS error |
| 22 | TestHostMountDeleteThenCreate | Delete then recreate with new content |
| 23 | TestHostMountDeleteThenStat | Delete then Stat, expect ErrNotExist |
| 24 | TestHostMountDeleteMultiple | Delete 10 files, verify remaining untouched |
| 25 | TestHostMountDeleteReadOnly | Delete on read-only mount, expect denied |

### Rename (5 tests)

| # | Test | Scenario |
|---|------|----------|
| 26 | TestHostMountRenameFile | Rename, verify old gone + new has content |
| 27 | TestHostMountRenameFileAcrossDir | Rename `dir1/a.txt` → `dir2/b.txt` |
| 28 | TestHostMountRenameFileOverwrite | Rename onto existing file, verify replaced |
| 29 | TestHostMountRenameDir | Rename directory, verify children accessible |
| 30 | TestHostMountRenameReadOnly | Rename on read-only mount, expect denied |

### Mkdir (4 tests)

| # | Test | Scenario |
|---|------|----------|
| 31 | TestHostMountMkdir | Create directory, verify in backing FS |
| 32 | TestHostMountMkdirNested | Create `a/b/c` via MkdirAll |
| 33 | TestHostMountMkdirAlreadyExists | Mkdir existing dir, expect no error |
| 34 | TestHostMountMkdirThenCreateFile | Mkdir then create file inside |

### UX workflows (10 tests)

| # | Test | Scenario |
|---|------|----------|
| 35 | TestHostMountWorkflowEditCycle | Read → modify → read back, verify round-trip |
| 36 | TestHostMountWorkflowBuildTree | Create 5 dirs + 20 files, WalkDir, verify matches backing FS |
| 37 | TestHostMountWorkflowRsyncRoundTrip | Rsync in, modify through mount, Rsync out, verify |
| 38 | TestHostMountWorkflowDeleteAndReadDir | Delete 2 of 5 files, ReadDir, verify 3 remain |
| 39 | TestHostMountWorkflowCreateReadModifyDelete | Full file lifecycle through mount |
| 40 | TestHostMountWorkflowMixedReadWrite | 10 existing + 5 new + 3 modified + 2 deleted, verify final state |
| 41 | TestHostMountWorkflowConcurrentWrites | 10 goroutines each creating a file, verify all land |
| 42 | TestHostMountWorkflowLargeDir | Create 200 files, ReadDir, verify all present |
| 43 | TestHostMountWorkflowNestedOverlayWrite | Write to nested FS (memory + null), verify routing |
| 44 | TestHostMountWorkflowCopyViaProjFS | io.Copy between files within mount |

## Test architecture — conformance vs. implementation-specific

Mount backends have different interception models:

| Backend | Model | Writes | Deletes | Renames |
|---------|-------|--------|---------|---------|
| FUSE (Linux) | Synchronous interception | Intercepted in-flight | Intercepted | Intercepted |
| ProjFS (Windows) | Post-notification write-back | Land on NTFS, synced after handle close | Intercepted via PRE_DELETE | Post-rename sync |
| WinFSP (Windows, future) | Synchronous interception | Intercepted in-flight | Intercepted | Intercepted |

These differences mean some tests are universally valid (conformance) and
others are sensitive to interception timing or platform behavior
(implementation-specific).

### Conformance test framework (implemented — PR #225)

The shared conformance framework is in place. It uses a table-driven
registration model:

```go
type mountSetup struct {
    mountDir string
}

type mountBackend struct {
    name  string
    mount func(t *testing.T, fsys ReadFS) mountSetup
    skip  func(t *testing.T)
}

var mountBackends []mountBackend // populated by platform init files
```

Platform-specific files register backends via `init()`:

- `mount_conformance_linux_test.go` — registers FUSE backend
- `mount_conformance_windows_test.go` — registers ProjFS backend

The framework currently has 9 read-only conformance tests
(`TestMountConformance*` prefix). All use `ReadFS` for mount setup.

### Read-write conformance tests (this PR)

The existing `mountBackend.mount` function takes `ReadFS`. Read-write
conformance tests need a writable mount. Two approaches:

**Option A — second registration table:** Add a `writeMountBackends` slice
with a `mount func(t *testing.T, fsys FS) mountSetup` signature. Write
conformance tests use `writeMountConformanceRun`. Read-only tests are
unaffected.

**Option B — extend existing table:** Add an optional `mountRW` field to
`mountBackend`. Conformance tests that need writes check `backend.mountRW`
and skip if nil.

Option A is cleaner — the read-only and read-write registration paths
are independent, and a backend that only supports reads (e.g., a future
read-only network FS) never appears in write tests.

Write conformance tests assert only on **final state** after the operation
completes (not on interception timing), so they work for both synchronous
(FUSE, WinFSP) and write-back (ProjFS) models:

- Create a file through mount, verify content in backing FS
- Modify a projected file, verify new content in backing FS
- Delete a file through mount, verify gone from backing FS
- Rename a file, verify old path gone + new path present in backing FS
- Mkdir through mount, verify in backing FS
- Delete on read-only mount, expect denied
- Rename on read-only mount, expect denied
- Full lifecycle: create → read → modify → read → delete → verify gone
- Mixed read-write: existing + new + modified + deleted, verify final state

### Implementation-specific tests — per-platform test files

Deeper tests that exercise behavior specific to the interception model.
These live in the platform-specific test files (`mount_windows_test.go`,
`fuse_linux_test.go`).

FUSE already has write tests in `fuse_linux_test.go`:

- `TestHostMountCreateWriteRead`, `TestHostMountOpenWriteOnlyNoTrunc`,
  `TestHostMountWriteAtOffset`, `TestHostMountMkdir`,
  `TestHostMountMkdirExisting`, `TestHostMountRemoveFile`,
  `TestHostMountRemoveDir`, `TestHostMountReadOnly`,
  `TestHostMountReadOnlyMkdir`, `TestHostMountReadOnlyRemove`

Some of these are conformance-eligible (final-state assertions only).
Others are FUSE-specific (e.g., `WriteAtOffset`, `OpenWriteOnlyNoTrunc`).
The conformance extraction for write tests should follow the same pattern
as the read extraction in PR #225: identify tests that assert only on
final state, move them to `mount_conformance_test.go`, and leave
FUSE/ProjFS-specific tests in their platform files.

Platform-specific tests may differ in:

- **Timing expectations:** ProjFS syncs on handle close, FUSE/WinFSP
  sync immediately. Tests that verify intermediate state are
  implementation-specific.
- **Error behavior:** ProjFS can't block new file creation on a read-only
  mount; FUSE returns EROFS. Tests for write rejection behavior are
  implementation-specific.
- **Append semantics:** ProjFS reads the entire file and overwrites;
  FUSE can intercept individual writes. Append-specific tests are
  implementation-specific.

The full test matrix (tests 1-44 in this spec) is the implementation-
specific set for ProjFS. The conformance subset is carved out of it.

### File layout

```text
mount_conformance_test.go              — shared conformance tests (read + write), no build tag
mount_conformance_linux_test.go        — registers FUSE backend (read + write)
mount_conformance_windows_test.go      — registers ProjFS backend (read + write; future: + WinFSP)
fuse_linux_test.go                     — FUSE-specific deep tests + unit tests
mount_windows_test.go                  — ProjFS-specific deep tests + unit tests
```

### This PR scope

This PR adds the ProjFS read-write implementation in `mount_windows.go`
and the ProjFS-specific write tests (tests 1-44) in
`mount_windows_test.go`. It also adds the write conformance tests to
`mount_conformance_test.go` and extracts common write tests from
`fuse_linux_test.go`.

## Known limitations

- **Append writes:** ProjFS fires `FILE_HANDLE_CLOSED_FILE_MODIFIED` after
  the handle closes. The `syncFromLocal` method reads the entire file and
  overwrites in the backing FS — this works for appends but is not
  incremental. Acceptable for the expected file sizes.
- **Atomic rename:** The rename handler does create-new + remove-old, not
  an atomic operation. If the process crashes between the two steps, the
  old path may still exist in the backing FS. Acceptable for non-transactional
  virtual file systems.
- **New file creation on read-only mounts:** ProjFS cannot intercept new
  file creation. Files created on a read-only mount materialize to local
  NTFS silently. This is a ProjFS limitation documented in `docs/ufsmount.md`.
- **WinFSP follow-up:** A future WinFSP backend will provide synchronous
  write interception via a FUSE-like driver, eliminating the write-back
  model's eventual-consistency semantics. The `writable FS` field and `FS`
  type-assertion pattern established here will be reused. WinFSP will
  register as a second entry in `mountBackends` and run the same
  conformance tests. Its implementation-specific tests will live alongside
  ProjFS tests in `mount_windows_test.go` or a dedicated file, and should
  mirror ProjFS test scenarios where possible to maximize parity.
