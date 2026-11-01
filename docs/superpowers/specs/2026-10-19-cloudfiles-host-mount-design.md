# Cloud Files Host Mount — Design Spec

## Goal

Add a read-write Windows host mount driver built on the Cloud Files API
(`cldapi.dll`, the cfapi used by OneDrive-style sync engines), and restructure
`host` so each mount driver (FUSE, ProjFS, Cloud Files) is a separate,
self-contained package that a consumer imports only if it wants it.

On Windows, Cloud Files is the default. ProjFS is chosen automatically only
when it is installed and the whole mounted file system is local. A developer
can force any driver.

Compatibility with the current `host` API may break.

## Non-goals

- Pushing changes made directly in the backing file system into an existing
  mount. Placeholders reflect the backing file system as of when their
  directory was first listed.
- Explorer shell integration (status overlays, context menus, a
  `StorageProviderSyncRootManager` registration). The sync root works without
  it.
- Sharing implementation between drivers. ProjFS and Cloud Files are distinct
  drivers; neither is designed in terms of the other.

## Package layout

```text
host/                     core, no OS-specific mount code
  host.go                 MountServer, Mount, Option, WithBackend, auto-selection
  registry.go             Backend interface, RegisterBackend
host/fuse/                Linux FUSE driver (moved from host/fuse_linux.go)
host/projfs/              Windows ProjFS driver (moved from host/*_windows.go)
  internal/projfslib/     projectedfslib.dll wrapper: bindings, structs,
                          constants, error table
host/cloudfiles/          Windows Cloud Files driver (new)
  backend.go              Backend registration; reports unavailable off Windows
  server_windows.go       mount / unmount lifecycle
  callbacks_windows.go    Cloud Files callbacks
  sync_windows.go         directory watcher and upload queue
  internal/cfapi/         cldapi.dll wrapper (new)
    syscalls_windows.go   bindings
    types_windows.go      struct layouts and constants
    errors_windows.go     error table
host/all/                 blank-imports fuse, projfs and cloudfiles
host/hosttest/            exported conformance suite and child-process helpers
internal/hostutil/        integer clamp helpers (today's host/math.go)
```

Rules:

- `host` imports no driver. Each driver imports `host` (and `hosttest` in its
  tests) and registers itself in `init()`, as drivers under `drivers/` do with
  `ufs.Register`.
- No driver imports another driver. A consumer that imports only
  `host/cloudfiles` compiles and links no ProjFS or FUSE code.
- Each Win32 API wrapper (`projfslib`, `cfapi`) is its own package, internal
  to its driver. It holds only the API: bindings, struct layouts, constants
  and the error table. It knows nothing of ufs (no `host`, `types` or `ops`
  imports), and the driver code reaches the API only through it.
- `cmd/ufsmount` imports `host/all`.

## Core API (`host`)

```go
type Backend interface {
    Name() string                  // "fuse", "projfs", "cloudfiles"
    Available() error              // nil when usable on this host; else an error with a resolution
    Suits(fsys types.ReadFS) bool  // whether auto-selection may pick it for fsys
    Priority() int                 // auto-selection picks the highest that suits
    Mount(ctx context.Context, fsys types.ReadFS, mountPath string) (MountServer, error)
}

func RegisterBackend(b Backend) // panics on a duplicate name

type Option func(*options)
func WithBackend(name string) Option

func Mount(ctx context.Context, fsys types.ReadFS, mountPath string, opts ...Option) (MountServer, error)
```

`MountServer` is unchanged (`io.Closer` plus `Wait()`).

Each driver package exports `const Name = "<name>"` for use with
`WithBackend`.

### Selection

- With `WithBackend(name)`: use that backend. If it is not registered, fail
  with an error naming the package to import. If `Available()` fails, return
  that error. `Suits` is not consulted.
- Without it: among registered backends whose `Available()` is nil and whose
  `Suits(fsys)` is true, pick the highest `Priority()`. Log the choice and the
  reason at Info. If none qualifies, fail with an error listing every
  registered backend and why it was not chosen, or, if none is registered,
  naming `host/all`.

| Driver     | Priority | Suits                                                        | Available                                           |
| :--------- | -------: | :----------------------------------------------------------- | :-------------------------------------------------- |
| fuse       |       10 | always                                                       | Linux and /dev/fuse usable                          |
| cloudfiles |       10 | always                                                       | Windows, cldapi.dll and every function load         |
| projfs     |       20 | no device in `fsys.GetDeviceInfo()` is remote (`IsRemote()`) | Windows, projectedfslib.dll and every function load |

This gives: Cloud Files by default on Windows; ProjFS when installed and the
mounted file system is entirely local.

### Device info

Remoteness comes from the device type: a device is remote when its type is
`network`.

- Add `const DeviceTypeNetwork = "network"` and
  `func (DeviceInfo) IsRemote() bool { return info.deviceType == DeviceTypeNetwork }`.
- Remove the `remote` field from `DeviceInfo` and the `remote` parameter from
  `NewDeviceInfo` (breaking); every caller drops its last argument. Callers
  that wrote `"network"` use `DeviceTypeNetwork`.
- Already `network` today: `gcsfs`, Windows `DRIVE_REMOTE` drives, and Linux
  `nfs`, `nfs4`, `cifs`, `smbfs`, `sshfs`, `fuse.sshfs`, `fuse.s3fs`,
  `fuse.gcsfuse`. Add to Linux's list: `smb3`, `9p`, `afs`, `ceph`,
  `glusterfs`, `fuse.glusterfs`.

### ufsmount

Add `-backend auto|fuse|projfs|cloudfiles` (default `auto`), passed through
`WithBackend` unless `auto`.

## Cloud Files driver (`host/cloudfiles`)

### Mount

1. Check the mount directory: it must exist, be on a local NTFS volume, and be
   empty. A directory still registered as a ufs sync root (a crashed mount)
   that holds only placeholders is accepted and re-registered.
2. `CfRegisterSyncRoot` with provider name `ufs`, a fixed provider GUID, a
   sync root identity of the mount path, and policies:
   - hydration: `CF_HYDRATION_POLICY_PROGRESSIVE` with modifiers
     `STREAMING_ALLOWED | AUTO_DEHYDRATION_ALLOWED`
   - population: `CF_POPULATION_POLICY_FULL` (directories are populated on
     first listing through `FETCH_PLACEHOLDERS`)
   - in-sync: track file attributes, times and size
   - hard links: none
3. `CfConnectSyncRoot` with the callback table and
   `REQUIRE_PROCESS_INFO | REQUIRE_FULL_FILE_PATH`. The callback context is a
   server ID looked up in a registry, never a Go pointer.
4. Start the directory watcher and the sync goroutine.
5. If `fsys` is not a `types.WriteFS`, the mount is read-only.

### Read path: placeholders and streaming

- `FETCH_PLACEHOLDERS` (first listing of a directory): `ReadDir` the backing
  directory and send the entries with `CfExecute(TRANSFER_PLACEHOLDERS)` and
  `DISABLE_ON_DEMAND_POPULATION`. Placeholders are metadata only: name, size,
  times, attributes (read-only when the mount is read-only) and a FileIdentity
  holding an 8-byte ID.
- The server keeps `map[ID]backingPath`. Renames update it. `FETCH_DATA` and
  every other callback resolve the backing path through the ID, falling back
  to the callback's path for an unknown ID.
- `FETCH_DATA`: read the required range from the backing file (`ReaderAt`,
  else `Seek`) and send it with `CfExecute(TRANSFER_DATA)`, in chunks of up to
  4 MiB at 4 KiB-aligned offsets, the last chunk ending at end of file.
  `CANCEL_FETCH_DATA` stops an in-flight transfer.
- A placeholder keeps the size the backing file had when its directory was
  listed (backing-side changes are a non-goal). If the backing file is now
  shorter than the requested range, the fetch fails with
  `STATUS_CLOUD_FILE_NOT_IN_SYNC` and the driver logs that the backing file
  changed underneath the mount; a remount picks up the new size. A longer
  backing file is served up to the placeholder's size.
- Names Windows cannot hold as placeholders are skipped with a Warn log:
  - two backing entries in one directory whose names differ only by case
    (Windows is case-insensitive); the first in `ReadDir` order wins;
  - symbolic links (a cloud placeholder cannot be a reparse point of another
    kind);
  - names invalid on Windows (reserved names such as `CON`, trailing dots or
    spaces, `<>:"|?*`).
- Streaming: with `STREAMING_ALLOWED`, Windows may serve reads without storing
  the data locally. Where it stores data anyway (for example memory-mapped
  access), the driver dehydrates the placeholder (`CfDehydratePlaceholder`)
  after its last handle closes if it is in-sync. Reading the whole mount
  must not leave a local copy of the data.

### Write path: upload on close

- Writing to a placeholder needs its data locally, so the platform hydrates it
  and the file stays local until its last handle closes.
- Sources for the sync queue:
  - `NOTIFY_FILE_CLOSE_COMPLETION` for placeholders.
  - A `ReadDirectoryChangesW` watcher on the sync root for files and
    directories created locally, which get no callback.
- The watcher can overflow (`ReadDirectoryChangesW` returns
  `ERROR_NOTIFY_ENUM_DIR` when its buffer fills and the events are lost).
  On overflow the syncer walks the sync root and enqueues every entry that is
  not an in-sync placeholder.
- The queue holds paths relative to the sync root. Backing-file-system
  changes (uploads, `MkdirAll`, deletes, renames) are serialized by one mutex
  per mount, so an upload never interleaves with a delete or rename of the
  same tree. A delete drops queued paths at or below the deleted path; a
  rename rewrites queued paths at or below the old path to the new one.
- The single sync goroutine processes each queued path:
  - New directory: `MkdirAll` in the backing file system, then
    `CfConvertToPlaceholder` with `MARK_IN_SYNC` and the new ID.
  - File: open it sharing read only. On a sharing violation it is still being
    written; drop it and wait for the next close. Otherwise record its USN,
    write it to the backing file system (`ufs.BatchCreate`, atomic where the
    backend supports it), then `CfSetInSyncState(IN_SYNC)` with that USN. If
    the USN no longer matches, the file changed during the upload: re-queue it.
    New files are converted to placeholders with `CfConvertToPlaceholder`.
  - After a file is in-sync, dehydrate it.
- An upload the backing file system rejects permanently (an invalid name,
  `fs.ErrPermission`, `fs.ErrInvalid`) is logged at Error with the
  `cfapi`/ufs error, not retried, and reported again at unmount. Other
  errors are retried with backoff (1 s doubling to 1 min) while the mount is
  open.
- Read-only mount: local creations cannot be prevented. They are logged once
  and left unsynced.

### Delete and rename

- `NOTIFY_DELETE`: on a read-only mount, refuse. Otherwise `Remove` (file) or
  `RemoveAll` (directory) in the backing file system, then
  `CfExecute(ACK_DELETE)` with `STATUS_SUCCESS`, or with the mapped NTSTATUS
  on failure, which refuses the delete.
- `NOTIFY_RENAME`: on a read-only mount, refuse. A target outside the sync
  root is a delete in the backing file system. Otherwise `ops.Rename` in the
  backing file system, update the ID map, then `CfExecute(ACK_RENAME)` with
  the result. A file moved into the sync root is a local creation (watcher).
- Files created locally and not yet uploaded get no callbacks: deleting one
  just drops it from the queue; renaming one re-queues it under its new name.

### Unmount (Close, or context canceled)

1. Stop the watcher. Process the queue; a file still open is reported, not
   uploaded.
2. `CfDisconnectSyncRoot`, then `CfUnregisterSyncRoot`.
3. Remove the placeholders the driver created (they hold no unsynced data).
   Keep local files that never synced and report them.
4. Unregister the server ID. `Wait` returns.

## Errors

Each API wrapper has its own `errors_windows.go` (`cfapi`, `projfslib`)
holding:

- An error type (`cfapi.Error`, `projfslib.Error`) with the API call, the
  path, and the code. `Error()` gives the call, path, code name and number,
  description, and the resolution for that call. It unwraps to
  `windows.Errno`, so `errors.Is` matches `fs.ErrNotExist` and the others.
- A table from code to description and resolutions keyed by the API call
  that failed, with a default. The call is the context: for example
  `CfRegisterSyncRoot` and `CfConnectSyncRoot` (mounting),
  `CfExecute(TRANSFER_PLACEHOLDERS)` (listing), `CfExecute(TRANSFER_DATA)`
  (reading), `CfSetInSyncState` and `CfConvertToPlaceholder` (uploading),
  `CfExecute(ACK_DELETE)` / `CfExecute(ACK_RENAME)`, `CfDehydratePlaceholder`,
  and `CfDisconnectSyncRoot` / `CfUnregisterSyncRoot` (unmounting). The driver
  wraps these errors with the mount path and the file it was working on.
- The reverse mapping from Go errors to the NTSTATUS a callback answers with
  (`fs.ErrNotExist` → `STATUS_OBJECT_NAME_NOT_FOUND`, `fs.ErrPermission` →
  `STATUS_ACCESS_DENIED`, `fs.ErrExist` → `STATUS_OBJECT_NAME_COLLISION`,
  otherwise `STATUS_CLOUD_FILE_UNSUCCESSFUL`).

Cloud Files table coverage:

- Every `ERROR_CLOUD_FILE_*` code and `ERROR_NOT_A_CLOUD_FILE`,
  `ERROR_NOT_A_CLOUD_SYNC_ROOT`.
- The general Win32 codes cfapi calls return: access denied, sharing
  violation, path not found, not a directory, not supported (non-NTFS),
  invalid parameter (binding bug), insufficient buffer, disk full, out of
  memory, module or procedure not found (feature missing, or Windows older
  than 10 1709 / Server 2016 with Desktop Experience), and the Cloud Files
  filter (`cldflt`) not running.

Codes come from `golang.org/x/sys/windows` constants. The ProjFS table moves
from `host/projfs_errors_windows.go` into `projfslib` in the same structure,
with per-call resolutions added.

## Testing

### `host/hosttest`

- `Run(t, Backend{Name, Mount, Capabilities})` runs the conformance suite:
  the current read tests plus write tests (create, overwrite, append, mkdir,
  delete file, delete directory, rename file, rename directory, refusals on a
  read-only file system). `Capabilities` lists what a driver supports (for
  example ProjFS cannot rename a partial directory) so each driver runs the
  tests that apply.
- `Main(m)` plus `Remove`, `Rename`, `WriteFile` run I/O from a child process,
  for drivers whose platform ignores I/O by the provider's own process.

### `host/cloudfiles`

- Streaming: after a large file is read fully through the mount, its
  allocation size is 0.
- Write and close: the file is in-sync, the backing file system has the new
  content, and its allocation size returns to 0.
- A file changed during its upload is not marked in-sync and is uploaded
  again.
- Unmount leaves the directory empty; an unsynced local file is kept and
  reported.
- A crashed mount's registration is re-registered.
- `cfapi`: struct sizes and field offsets match `cfapi.h`; every error table
  entry has a description and a default resolution; messages differ by call.
- The ID map follows renames.
- Watcher overflow: with a tiny watcher buffer (a test hook), creating many
  files at once still uploads every one.
- A rename or delete of a path whose upload is queued uploads to the new name
  or not at all.
- A backing file that shrank after listing fails the read with a clear error;
  case-colliding names and symlinks in the backing file system are skipped
  and logged.
- An upload the backing file system rejects is reported at unmount and not
  retried.

### `host` and device info (all platforms)

- Selection with fake registered backends: priority, `Suits`, forced backend,
  unregistered backend, no backends.
- `DeviceInfo.IsRemote()` is true exactly for the `network` device type,
  including the Linux file system types added to the network list.
- Dependency isolation, alongside the existing coredeps test: `host` depends on
  no driver, no driver depends on another, and `cfapi` / `projfslib` import
  nothing from ufs.

### CI

- The Cloud Files tests always run on Windows: Cloud Files ships with
  Windows, so there is no opt-in variable. They run on both `build-windows`
  and `build-windows-root`. (ProjFS keeps `UFS_ENABLE_PROJFS_TEST`, since it
  is an optional feature.)

## Risks, verified first on CI

The first commits are a spike: bindings, mount, the read path, a streaming
test, and a test that reads from the provider process. It answers:

1. Do reads stream (allocation size stays 0)?
2. Does Cloud Files call back for the provider process's own I/O? If not, the
   tests use the `hosttest` child-process helpers.
3. Can placeholders be removed after the sync root is unregistered?
4. Do both Windows runners have the `cldflt` filter? cfapi is documented as
   "desktop apps only", and `build-windows-root` runs Server Core. If Server
   Core lacks it, that comes back for review rather than adding a skip.

A "no" to 1 or 3 changes this design and comes back for review before the
write path is built.
