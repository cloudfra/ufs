# Windows read-write host mounting design

Status: Draft
Date: 2026-09-27
Branch: `windows-readwrite-mount`

## Goals

- Support read-write host mounting on Windows, at parity with the existing
  Linux FUSE mount (`host/fuse_linux.go`): file creation, writing (including
  overwrite-without-truncate), deletion, and directory creation.
- Choose between two Windows virtualization APIs — ProjFS (already used
  read-only today) and the Cloud Files API (CfApi) — automatically, based on
  whether the mounted `ufs.FS` includes a remote backing device, with an
  explicit override available to callers.
- Reclaim local disk space for CfApi-backed mounts by dehydrating files back
  to placeholders as soon as they are no longer open and any writes have been
  synced to the backing `ufs.FS`.

## Non-goals

- Rename support. `ufs.RenameFileFS` is optional and not implemented by every
  backend, and the existing Linux FUSE mount does not support rename at all
  (see the "Not implemented FUSE operations" comment in `host/fuse_linux.go`).
  This design matches that: both Windows backends deny rename notifications,
  same as `host/mount_windows.go` does today for `PreRename`.
- chmod/xattr/hardlinks/symlink creation — none of these have a `ufs.FS`
  counterpart today.
- Fixing FUSE's existing `O_WRONLY` without `O_TRUNC` limitation
  (`github.com/cloudfra/ufs/issues/218`). Not touched by this work; see
  "Notable side effect" under ProjFS below for why Windows doesn't have this
  limitation in the first place.
- Dehydration policy beyond "eager, on close, once synced": no manual
  pin/"always keep local" UX, no low-disk-space-triggered eviction, no
  idle-timeout sweep.
- Reacting to structural changes (new/removed nested mounts) to a `ufs.FS`
  after `host.Mount` has already started serving it. Backend selection and
  any per-path device information are read once at `Mount` time.

## Background

`host.Mount(ctx, fsys, mountPath)` (`host/host.go`) mounts a `ufs.ReadFS` (or,
for write support, a `ufs.FS`) on the host OS. On Linux this uses FUSE via
go-fuse and already supports full read-write: `host/fuse_linux.go` wires
`Create`, `Mkdir`, `Unlink`, `Rmdir`, and file `Read`/`Write` directly to the
corresponding `ufs.FS` methods synchronously, because FUSE hands the calling
process's exact read/write buffers to the userspace filesystem process on
every syscall.

On Windows, `host/mount_windows.go` currently only implements the read path
via ProjFS: `StartDirectoryEnumerationCallback`, `GetDirectoryEnumeration`,
`GetPlaceholderInfo`, and `GetFileData` are wired to `ufs.ReadFS`, and the
`NotificationCallback` explicitly returns `ACCESS_DENIED` for
`PRJ_NOTIFICATION_PRE_DELETE` and `PRJ_NOTIFICATION_PRE_RENAME` — the mount is
read-only by construction, not by a fundamental ProjFS limitation.

## Windows virtualization APIs compared

Neither ProjFS nor the Cloud Files API works like FUSE. In both, the OS
kernel-mode filter driver (`prjflt.sys` / `cldflt.sys`) writes bytes directly
to a real, local file on the NTFS volume backing the mount root — the
provider process is not in the write path. The provider only learns about the
write afterward, through an asynchronous "handle closed" notification, and is
expected to read the resulting bytes off local disk itself and push them into
the backing store. This shapes the entire design below.

| | ProjFS (existing) | Cloud Files API (new) |
|---|---|---|
| Enablement | Optional Windows feature (`Client-ProjFS` / `FS-Projectedfs`), must be turned on by the user/admin | Built into Windows 10 1709+ (build 16299) and Windows 11; no feature toggle |
| Intended use case (per Microsoft's own guidance) | Fast, local-like backing stores (its flagship consumer is VFS for Git) | Slower/remote backing stores needing hydration progress and online/offline state |
| Disk space reclamation | None — once a placeholder is read or written it becomes a normal NTFS file for the life of the mount | Built-in: `CfSetInSyncState` + `CfConvertToPlaceholder` dehydrate a synced file back to a placeholder |
| Callback shape | Fixed struct of 8 typed callback function pointers (already hand-bound in `host/projfs_windows.go`) | Callback parameters are a C union keyed by callback type — a meaningfully gnarlier FFI surface to hand-bind correctly |
| Placeholder identity | Callback hands back the relative path directly | Provider must invent and persist an opaque per-placeholder identity blob, then map it back to a path itself |
| Go bindings | None official; this repo hand-rolls its own via `golang.org/x/sys/windows`, matching the pattern already used for go-fuse on Linux | None official; one small, unverified third-party module found (`go-bindings-win32`) |
| Volume requirement | Local NTFS/ReFS only, no network drives | Same restriction |

Given ufs wraps both local (`file:///`, `memory:`) and remote (`gs://`)
backends, and Microsoft's guidance splits cleanly along that line, this
design runs **both APIs side by side** rather than picking one.

## Architecture

### 1. Backend selection and override

`host.Mount` gains a variadic, non-breaking option parameter:

```go
// host.go
func Mount(ctx context.Context, fsys ufs.ReadFS, mountPath string, opts ...MountOption) (MountServer, error)

type WindowsHostBackend int

const (
	// WindowsHostBackendAuto selects ProjFS or CfApi based on whether fsys
	// reports any remote backing device. It is the default.
	WindowsHostBackendAuto WindowsHostBackend = iota
	WindowsHostBackendProjFS
	WindowsHostBackendCfApi
)

// WithWindowsHostBackend forces a specific Windows virtualization backend
// instead of relying on auto-detection. It has no effect on non-Windows
// platforms.
func WithWindowsHostBackend(b WindowsHostBackend) MountOption
```

`MountOption` and `WindowsHostBackend` are declared in `host.go` so they are
available on every platform; Linux's `mount()` accepts and ignores them since
there is only one Linux implementation. Existing callers of `Mount(ctx, fsys,
mountPath)` are unaffected.

On Windows, `Auto` resolves once, at `Mount` time:

```go
// mount_windows.go
func selectBackend(fsys ufs.ReadFS) WindowsHostBackend {
	di, ok := fsys.(ufs.DeviceInfoGetter)
	if !ok {
		return WindowsHostBackendProjFS // conservative default
	}
	for _, d := range di.GetDeviceInfo() {
		if d.Remote() {
			return WindowsHostBackendCfApi
		}
	}
	return WindowsHostBackendProjFS
}
```

`ufs.DeviceInfo` already carries an unexported `remote bool`, set correctly by
every backend today (`gcsFS` reports `remote: true`; `localFS`, `memFS`,
`archiveFS`, `nullFS`, `angryFS` report `false`), and `nestFS.GetDeviceInfo()`
already aggregates nested mounts into a single `DeviceMap`. There is currently
no exported accessor, so this design adds one (see "ufs.go changes" below).

`mount_windows.go`'s top-level `mount()` becomes a thin dispatcher:

```go
func mount(ctx context.Context, fsys ufs.ReadFS, mountPath string, opts ...MountOption) (MountServer, error) {
	cfg := resolveMountOptions(opts)
	backend := cfg.windowsBackend
	if backend == WindowsHostBackendAuto {
		backend = selectBackend(fsys)
	}
	switch backend {
	case WindowsHostBackendCfApi:
		return mountCfApi(ctx, fsys, mountPath)
	default:
		return mountProjFS(ctx, fsys, mountPath)
	}
}
```

The existing `mount()` body in `host/mount_windows.go` is renamed to
`mountProjFS` with no behavioral change beyond the write support described
below.

### 2. Shared write-back mechanism

Because both APIs converge on "read the local file yourself, then push it
into the backing store," that logic is written once and shared:

```go
// host/windows_writeback.go (windows-only)

// writebackModified reads the local (fully hydrated) file at
// mountRoot+relPath and copies its contents into fsys via ufs.FS.Create.
func writebackModified(fsys ufs.ReadFS, mountRoot, relPath string) error

// writebackDeleted removes relPath (or, for a directory, its whole subtree)
// from fsys via ufs.FS.Remove/RemoveAll.
func writebackDeleted(fsys ufs.ReadFS, relPath string, isDir bool) error
```

Both functions type-assert `fsys.(ufs.FS)` and return `fs.ErrPermission` if
the backing FS is read-only, mirroring the `EROFS` behavior in
`host/fuse_linux.go`.

### 3. ProjFS backend changes (`host/mount_windows.go`)

Widen the subscribed notification bitmask from
`prjNotifyPreDelete|prjNotifyPreRename` to also include
`prjNotifyNewFileCreated`, `prjNotifyFileOverwritten`,
`prjNotifyFileHandleClosedFileModified`, and
`prjNotifyFileHandleClosedFileDeleted`. In `notificationCB`:

- `PreDelete`: allow (previously always denied) when `fsys` is `ufs.FS`,
  otherwise deny as today.
- `PreRename`: continue denying (see Non-goals).
- `NewFileCreated`, `FileOverwritten`, `FileHandleClosedFileModified`: call
  `writebackModified`.
- `FileHandleClosedFileDeleted`: call `writebackDeleted`.

**Notable side effect:** ProjFS fully hydrates a file (via
`PRJ_NOTIFICATION_FILE_PRE_CONVERT_TO_FULL`) before it can be opened for
writing, so a write-without-truncate — which
`host/fuse_linux.go:fuseNode.Open` currently rejects with `ENOTSUP` — works
correctly on the ProjFS path with no extra effort. This is called out for
awareness only; the Linux limitation is out of scope here.

### 4. CfApi backend (new: `host/mount_windows_cfapi.go`, `host/cfapi_windows.go`)

Structured the same way the ProjFS files are split today
(`mount_windows.go` for the `MountServer`/callback logic,
`projfs_windows.go` for the raw DLL bindings and struct layouts):

- `cfapi_windows.go`: `golang.org/x/sys/windows.NewLazySystemDLL("cldapi.dll")`
  bindings for `CfRegisterSyncRoot`, `CfUnregisterSyncRoot`,
  `CfConnectSyncRoot`, `CfDisconnectSyncRoot`, `CfCreatePlaceholders`,
  `CfExecute` (used to acknowledge/complete each callback, unlike ProjFS's
  plain HRESULT return), `CfSetInSyncState`, `CfConvertToPlaceholder`. Struct
  and union layouts (`CF_CALLBACK_INFO`, `CF_CALLBACK_PARAMETERS`,
  `CF_PLACEHOLDER_CREATE_INFO`, `CF_FS_METADATA`) are the highest-risk,
  most fiddly part of this design — treat getting their byte layout right as
  its own implementation task with dedicated tests, the same way
  `host/math_test.go` exists to pin down `projfs_windows.go`'s conversions.
- `mount_windows_cfapi.go`: registers a sync root at `mountPath`, implements
  `FETCH_DATA` (≈ ProjFS's `GetFileData`), `FETCH_PLACEHOLDERS` (≈ directory
  enumeration, calling `CfCreatePlaceholders` per entry instead of
  `PrjFillDirEntryBuffer`), `NOTIFY_DELETE`/`NOTIFY_DELETE_COMPLETION` (→
  `writebackDeleted`), `NOTIFY_RENAME` (deny, matching ProjFS), and
  `NOTIFY_FILE_CLOSE_COMPLETION` (→ `writebackModified` when the callback's
  modified flag is set, then eager dehydration — see below).
- Placeholder identity: CfApi requires an opaque per-placeholder identity
  blob rather than handing back a path. This design uses the UTF-8-encoded
  relative ufs path as that blob, since ufs paths are already unique and
  stable within one mount.

### 5. Eager dehydration (CfApi only)

Immediately after `NOTIFY_FILE_CLOSE_COMPLETION` fires for a file — once any
required `writebackModified` call has succeeded — the CfApi backend marks the
file in-sync (`CfSetInSyncState`) and converts it back to a placeholder
(`CfConvertToPlaceholder`). This is unconditional and requires no
configuration: a CfApi-backed mount never accumulates local disk usage beyond
an open file handle's lifetime. There is no idle timer and no background
sweep goroutine.

A manual fallback is exposed for retrying a failed eager attempt (e.g. the
file was reopened by another process before conversion completed):

```go
// Dehydrator is implemented by MountServer values returned for a
// CfApi-backed mount. It is not implemented by ProjFS-backed mounts.
type Dehydrator interface {
	Dehydrate(path string) error
}
```

Callers type-assert `server.(host.Dehydrator)` to use it; calling it on a
ProjFS-backed `MountServer` is simply unavailable (the type assertion fails),
not a runtime error.

### 6. ufs.go changes

Add an exported accessor so `host` (which per this repo's convention only
uses `ufs`'s exported API) can read the `remote` bit set by each backend's
`DeviceInfo`:

```go
// deviceinfo.go
// Remote reports whether the device is located on a remote machine, where
// frequent I/O calls may be slow.
func (info DeviceInfo) Remote() bool { return info.remote }
```

This mirrors the recent `NewDeviceInfo`/`DeviceInfo` exports (#329, #330) and
requires no other changes to `ufs.go`.

## Testing

- Extend `host/mount_conformance_windows_test.go` to run the shared
  conformance suite (`host/mount_conformance_test.go`) twice: once with
  `WithWindowsHostBackend(WindowsHostBackendProjFS)`, once with
  `WithWindowsHostBackend(WindowsHostBackendCfApi)`. Run both against a local
  fsys (`memory:`) and against a `DeviceInfoGetter` test double that reports
  `remote: true` (CI is unlikely to have real GCS credentials), so both
  backends get full coverage independent of `Auto` resolution.
- Unit tests for `selectBackend`: no `DeviceInfoGetter` → ProjFS; all-local
  `DeviceMap` → ProjFS; one remote entry among several (nested mount case) →
  CfApi; an explicit `WithWindowsHostBackend` override always wins regardless
  of `DeviceInfo`.
- Create/write/delete/mkdir parity tests mirroring the write cases already in
  `host/fuse_linux_test.go`, run against both Windows backends.
- Eager dehydration test: write through the CfApi mount, close the handle,
  assert (a) the backing `ufs.FS` received the write via `writebackModified`,
  and (b) the local file is back in a placeholder (not fully present) state
  afterward — via `CfGetPlaceholderInfo` or the reparse-point attributes,
  whichever proves more reliable to assert on in a test during
  implementation.
- Manual pass with Windows Defender (or another AV/EDR product) active
  against the CfApi backend: eager dehydrate-then-rehydrate on every access
  is a much higher-churn placeholder pattern than the read-only path today,
  and is worth confirming doesn't trigger excessive AV scanning or
  false-positive quarantines before calling this done.

## Risks and open questions

- Hand-rolling CfApi's C-union callback-parameter bindings correctly, with no
  official Go bindings to build on, is the single highest-risk item in this
  design.
- CfApi's minimum supported OS (Windows 10 1709 / build 16299) is actually
  *older* than ProjFS's (Windows 10 1809) — not a blocker, but worth stating
  explicitly since it's counter-intuitive given CfApi is the "newer-feeling"
  API of the two.
- Backend selection is fixed at `Mount` time; if a caller mounts a
  purely-local `ufs.FS` and later nests a remote mount into it via the
  already-running `nestFS`, the host mount does not switch backends. This is
  accepted as a known limitation (see Non-goals) rather than solved here.
