# Release notes

## v0.17.0 (unreleased) - changes since v0.16.1

The baseline for these notes is the newest tag, `v0.16.1` (`3be45d9`,
2026-10-06 06:30:02 +0000, "Introduce all driver"). That tag has no GitHub
release: the newest published release is `v0.16.0` (`b5c7d8b`,
2026-10-06T05:57:42Z), one commit earlier. Everything below is therefore also
valid for `v0.16.0`, except for the single commit that separates the two tags
("Introduce all driver", which registers every driver in `drivers/all`).

The head of these notes is `main` at `f572064` ("Add a Rename polyfill for
file systems without RenameFileFS (#498)").

```console
$ git rev-list --count v0.16.1..origin/main
79

$ git diff --shortstat v0.16.1..origin/main
 285 files changed, 28924 insertions(+), 6955 deletions(-)
```

This release is dominated by three changes. The package layout is
reorganized: the core interfaces move out of the root package into
`drivers/types`, the file operations move into a new `ops` package, and the
in-memory and on-disk constructors move into a new `drivers/core` package.
**These moves are breaking for importers**: the root package does not keep
aliases for the moved names. Second, a new `drivers/polyfills` package
emulates the optional capabilities that a file system does not implement, so
`Rename`, `Remove`, `RemoveAll`, `Create`, `OpenFile` and `CopyFile` now work
against any file system. Third, `types.ReleaseFS` lets a file system give up
the archives it mounted for itself.

## Public API surface

### How this was measured

The exported surface of every non-internal package of the module was dumped at
both refs with a `go/packages` walker that prints one canonical, sorted line
per exported object: package-level functions, variables, constants, named
types and aliases (with their underlying type), methods of exported types,
struct fields (with tags), interface methods and interface embeds. Unexported
names and imported (re-exported) objects are excluded. The walker is a
throwaway program; it is not checked into the repository and is not part of
the build.

The dump was run with `GOOS=linux` and `GOOS=windows` at both refs. The two
results are byte-identical, so the exported package-level surface does not
depend on the target platform; the platform-specific code in `host` and
`drivers/core` only adds methods on unexported types.

| Ref | Exported declarations |
| --- | --- |
| `v0.16.1` | 462 |
| `main` | 534 |

The diff was classified by declaration identity: the kind, the package and the
declared name, where a method or an interface method is keyed by its receiver
and an interface embed is keyed by the interface and what it embeds.

| | Count |
| --- | --- |
| Added | 152 |
| Changed signature | 37 |
| Removed from its package | 80 |

69 of the 80 removals are moves. They reappear with the same kind and name in
another package:

| From | To | Declarations |
| --- | --- | --- |
| `ufs` | `ufs/drivers/types` | 51 |
| `ufs` | `ufs/ops` | 10 |
| `ufs` | `ufs/drivers/core` | 5 |
| `ufs` | `ufs/ops` or `ufs/drivers/polyfills` | 2 |
| `ufs` | `ufs/drivers/types`, `ufs/proto` or `ufs/drivers/common/buffile` | 1 |

Two rows resolve against more than one package by name alone: `Remove` and
`RemoveAll` exist in both `ops` and `drivers/polyfills` on `main`, and the
`File` name exists in `drivers/types`, in `proto` and in
`drivers/common/buffile`.

The other 11 removals do not reappear anywhere, and all of them are explained
by the type cleanup in `3ddea5f Streamline the types (#478)`, `3231934 Clean
up the DeviceInfo API and its device map lookup (#522)` and `99b99b9 Release
archive mounts so they stop holding local files open (#481)`:

| Removed from `ufs` | Replacement |
| --- | --- |
| `RemoveFileFS`, its `Remove` and `RemoveAll` methods, and the `WriteFS` embed | `types.RemoveFS` and `types.RemoveAllFS` |
| `DeviceInfoGetter` and its `GetDeviceInfo` method, and the `ReadFS` embed | `types.DeviceInfoGet` with a `DeviceInfo()` method, named like `URIGet` and `ExternalPathGet` |
| `WriteFS`'s own `Create` and `MkdirAll` methods | the embedded `types.CreateFileFS` and `types.MkdirAllFS` |
| `DeviceInfo`'s embedded `fmt.Stringer` field | a compile-time assertion; the always-nil field made `String()` panic |
| the `FS` embed of `WriteFS` | the embed is now spelled `types.WriteFS`, so the identity differs by name only |

Nothing else was deleted outright.

The same comparison was cross-checked with `golang.org/x/exp/cmd/apidiff`
(built at `v0.0.0-20261014143641-f0251eb4aa3e`, which is not a dependency of
this module), which groups changes instead of listing declarations:

```console
# in a v0.16.1 checkout, then in a main checkout
apidiff -m -w /tmp/adm-v0161.txt github.com/cloudfra/ufs
apidiff -m -w /tmp/adm-main.txt github.com/cloudfra/ufs
apidiff -m /tmp/adm-v0161.txt /tmp/adm-main.txt
```

| apidiff section | Lines |
| --- | --- |
| `Incompatible changes:` | 82 |
| `Compatible changes:` | 17 |

The `-m` flag matters: without it apidiff compares one package and reports
`unexpected export data with prefix` because the dumps hold the whole module.
Comparing package by package instead gives 82 incompatible and 10 compatible
lines, 57 of them in the root package.

The two measurements agree on the shape of the change. apidiff reports the new
packages, lists the moved root-package names as `removed`, and every signature
change it prints is a root-package type replaced by its `drivers/types`
counterpart:

```console
- New: changed from func(context.Context, string) (WriteFS, error) to func(context.Context, string) (github.com/cloudfra/ufs/drivers/types.WriteFS, error)
```

### New packages

| Package | Contents |
| --- | --- |
| `github.com/cloudfra/ufs/drivers/types` | The core interfaces and value types of ufs: `FileInfo`, `ReadFile`, `File`, `ReadFS`, `WriteFS`, `Watcher`, `NotifyOp`, `NotifyHook`, the device-info types and the optional capability interfaces. |
| `github.com/cloudfra/ufs/drivers/core` | The file systems that need no external service and no driver URI: `MakeMemFS`, `MakeNullFS`, `MakeAngryFS`, `MakeLocalFS`, `NewTempMountFS`, plus the archive file system. |
| `github.com/cloudfra/ufs/ops` | The file operations that work against any file system: `Copy`, `Rsync`, `List`, `ListFiles`, `Walk`, `ForEachFileInfo`, `ForEachFilename`, `Remove`, `RemoveAll`, and the new `Rename` and `RenameByCopy`. |
| `github.com/cloudfra/ufs/drivers/polyfills` | Emulations of the optional interfaces on top of the interfaces the file system does implement. See the section below. |
| `github.com/cloudfra/ufs/drivers/decorators/telemetryfs` | A decorator that reports a trace span and a duration metric per operation (#452). It registers itself through `init`, so it is selected by name in a mount's `options` list; its only exported Go symbol is the `Options` config struct. |
| `github.com/cloudfra/ufs/agenttools/posixcli` | An experimental stub for running a POSIX command against a file system instead of the host. Its exported surface is `func Run(_ context.Context, _ io/fs.FS, _ string) (string, error)`, which reports that nothing is implemented yet. |

### Breaking: the core types moved from `ufs` to `ufs/drivers/types`

`d61c1d0 Move main types to drivers/types (#463)` moved the interfaces that
every driver implements into their own package, and `a285a0b Split type files
for less merge conflicts and granular interface support (#491)` gave each
interface its own file. The root package keeps `New`, `NewFileInfo`,
`NewDriver`, `NewDecorator`, `URIOrDefault`, `WrapNestFS`, `AbsPath`, `FS`,
`Driver` and `Decorator`, but their signatures now refer to `drivers/types`.
There are no compatibility aliases, so every importer has to add the import.

Moved from `github.com/cloudfra/ufs` to `github.com/cloudfra/ufs/drivers/types`:

- Types: `FileInfo`, `ReadFile`, `File`, `ReadFS`, `WriteFS`, `Watcher`,
  `NotifyOp`, `NotifyHook`, `DeviceInfo`, `DeviceMap`, `URIGet`,
  `ExternalPathGet`, `CopyFileFS`, `OpenFileFS`, `RenameFileFS`,
  `ListFilenames`, `ForEachFileInfoIter`, `ForEachFilenameIter`.
- Constants: `NotifyCreate`, `NotifyWrite`, `NotifyRemove`, `NotifyRename`,
  `NotifyChmod`.
- Variables: `DefaultDeviceMap`.
- Functions: `NewDeviceInfo`, `NewDeviceMap`.

The interface definitions themselves were also reshaped
(`3ddea5f Streamline the types (#478)`, `99b99b9 (#481)`):

- Every optional capability interface now embeds `io/fs.FS`, so a capability
  assertion yields something that can actually be read: `BatchCreateFS`,
  `CopyFileFS`, `CreateFileFS`, `ExternalPathGet`, `MkdirAllFS`, `MkdirFS`,
  `MountedArchiveDirFS`, `OpenFileFS`, `RemoveAllFS`, `RemoveFS`,
  `ReleaseFS`, `RenameFileFS`.
- `RemoveFileFS` was replaced by `RemoveFS` (`Remove`) and `RemoveAllFS`
  (`RemoveAll`), so a file system that can remove a single entry is no longer
  forced to implement `RemoveAll` as well.
- `DeviceInfoGetter` was replaced by `DeviceInfoGet`, whose method is
  `DeviceInfo() DeviceMap`.
- `MkdirFS` (`Mkdir`) and `ReleaseFS` (`Release`) are new.
- `OpenFileFS.OpenFile` now returns `types.File` rather than `io/fs.File`.
- `DeviceInfo` no longer embeds `fmt.Stringer` and gained `ThreadCount()` and
  `Remote()` accessors (#522).

`WriteFS` is composed of named interfaces instead of declaring its methods
inline:

```go
// v0.16.1
type WriteFS interface {
	Create(name string) (File, error)
	MkdirAll(name string, perm io/fs.FileMode) error
	ReadFS
	RemoveFileFS
}

// main
type WriteFS interface {
	ReadFS
	RemoveFS
	RemoveAllFS
	CreateFileFS
	MkdirAllFS
}
```

The method set is unchanged, so existing implementations of `WriteFS` still
satisfy it. The decomposition is what makes the optional interfaces
composable: `CreateFileFS` and `MkdirAllFS` can be asserted on their own.

### Breaking: the file operations moved to `ufs/ops`

`851c8c4 Move copy, list and walk helpers to the ops package (#397)` and
`e4842a0 Move Rename into the ops package (#465)`.

| v0.16.1 | main |
| --- | --- |
| `ufs.Copy(srcFS io/fs.FS, src string, destFS WriteFS, dest string)` | `ops.Copy(srcFS io/fs.FS, src string, destFS io/fs.FS, dest string)` |
| `ufs.Rsync(srcFS io/fs.FS, destFS WriteFS, dir string)` | `ops.Rsync(srcFS io/fs.FS, destFS types.WriteFS, dir string)` |
| `ufs.List`, `ufs.ListFiles` | `ops.List`, `ops.ListFiles` |
| `ufs.Walk(fsys, dir, WalkArgs, f)` | `ops.Walk(fsys, dir, ops.WalkArgs, f)` |
| `ufs.ForEachFileInfo`, `ufs.ForEachFilename` | `ops.ForEachFileInfo`, `ops.ForEachFilename` |
| `ufs.Remove`, `ufs.RemoveAll` | `ops.Remove`, `ops.RemoveAll` |
| - | `ops.Rename(fsys types.WriteFS, oldPath, newPath string)` |
| - | `ops.RenameByCopy(src types.WriteFS, oldPath string, dst types.WriteFS, newPath string)` |

`ops.Copy` widened its destination from `WriteFS` to `io/fs.FS`, and
`ops.Walk`, `ops.List` and friends take `io/fs.FS` rather than a ufs
interface, so they now work against a plain `os.DirFS` or `embed.FS`.

`ops.Rename` is the generic entry point for the new `Rename` support: it uses
`types.RenameFileFS` when the file system provides it and falls back to a copy
otherwise. `fe87b45 Implement Rename for every file system (#455)` added the
native implementation to each driver, and `f572064 Add a Rename polyfill for
file systems without RenameFileFS (#498)` added the emulation for the rest.

### Breaking: the local constructors moved to `ufs/drivers/core`

`c8cf06d Add the drivers/core package (#412)` and the moves that followed
(#418, #422, #424, #436, #437).

| v0.16.1 | main |
| --- | --- |
| `ufs.MakeMemFS(name string) WriteFS` | `core.MakeMemFS(name string) types.WriteFS` |
| `ufs.MakeNullFS(name string) WriteFS` | `core.MakeNullFS(name string) types.WriteFS` |
| `ufs.MakeAngryFS(name string) WriteFS` | `core.MakeAngryFS(name string) types.WriteFS` |
| `ufs.MakeLocalFS(name string) (WriteFS, error)` | `core.MakeLocalFS(name string) (types.WriteFS, error)` |
| `ufs.NewTempMountFS(ctx, uri, prepare)` | `core.NewTempMountFS(ctx, uri, prepare)` |

### New: the drivers/polyfills package

`64a639a Introduce the polyfills package (#480)` started a package for the
adapters that a driver author would otherwise write by hand: when a file
system does not implement an optional capability, the package builds the
capability out of the interfaces the file system does implement. Every entry
point takes an `io/fs.FS`, dispatches on the optional interface, and falls
back to a slower emulation. The dispatch bodies are unexported and shared, so
an entry point never calls another entry point's exported function.

| Function | Fallback when the file system lacks |
| --- | --- |
| `BatchCreate(fsys io/fs.FS, files []types.BatchFile) error` | `types.BatchCreateFS` (#480) |
| `CopyFile(fsys io/fs.FS, srcPath, dstPath string) error` | `types.CopyFileFS` (#488) |
| `Create(fsys io/fs.FS, name string) (types.File, error)` | `types.CreateFileFS` (#490) |
| `Mkdir(fsys io/fs.FS, name string, perm io/fs.FileMode) error` | `types.MkdirFS` (#480) |
| `MkdirAll(fsys io/fs.FS, name string, perm io/fs.FileMode) error` | `types.MkdirAllFS` (#480) |
| `OpenFile(fsys io/fs.FS, name string, flag int, perm io/fs.FileMode) (types.File, error)` | `types.OpenFileFS` (#541) |
| `Remove(fsys io/fs.FS, name string) error` | `types.RemoveFS` (#497) |
| `RemoveAll(fsys io/fs.FS, name string) error` | `types.RemoveAllFS` (#495) |
| `Rename(fsys io/fs.FS, oldPath, newPath string) error` | `types.RenameFileFS` (#498) |
| `ToFile(f io/fs.File) types.File` | a handle that is not a `types.File` (#489) |
| `ToBufferedFile(f io/fs.File, readOnly bool, mode BufferMode) (types.File, error)` | `Seek` and `ReadAt` on a handle that cannot seek (#529) |

`ToFile` returns the file unchanged when it already is a `types.File`;
otherwise `ReadAt` falls back to seek, read and seek back, a write the file
cannot do reports `fs.ErrPermission`, and a seek or `ReadAt` it cannot do
reports `errors.ErrUnsupported`. `ToBufferedFile` is the nestFS
`Seek`/`ReadAt` buffering moved out of the mount (`8924106 (#529)`); unlike
`ToFile` it needs the caller's read or write intent, because a file open for
writing is refused: writes would go to the underlying file while reads came
from the buffer, so the two disagreed about content, position and size
(`850f264 Refuse to buffer a writable file and report closed file methods
(#526)`). The mode is `BufferMemory` or `BufferDisk`.

The `OpenFile` polyfill emulates the `os.OpenFile` flag set on top of `Open`
and `CreateFileFS`: a read-only open is `Open`, a new or truncated write is
`Create`, and the handle reports `fs.ErrPermission` for a method outside the
access mode it was opened with. A write that keeps the content already there
is refused with `errors.ErrUnsupported`, since `Create` replaces a file rather
than editing it and a copy kept to write back on `Close` is a copy the rest of
the file system never sees.

The package is not test-only, but on `main` only two files import it:
`nestfs.go`, which uses `ToBufferedFile` and `BufferMode` for its buffered
reads (`8924106 (#529)`, tightened by `850f264 (#526)`), and
`drivers/decorators/readonlyfs`, which uses `ToFile` so that masking the mode
does not drop `io.ReaderAt`, `io.Seeker` or `fs.ReadDirFile`
(`d460b1a Report read-only file systems without write permission bits
(#521)`). Everything else is reached from tests only for now: `ops.Remove` and
`ops.RemoveAll` still return `fs.ErrPermission` when the optional interface is
missing instead of calling the entry point, `ops.Rename` keeps its own
fallback, and a driver that gained an `OpenFile` such as
`92ec8c2 localFS.OpenFile (#486)` calls the capability directly rather than
going through the polyfill. Routing those call sites through the package
changes behavior from permission-denied to emulation, so it is deliberately
left as separate work.

### New exported API

`github.com/cloudfra/ufs/drivers/types`:

- `BatchCreateFS` and `BatchFile` (`26ce399 Propose BatchCreateFS for writing
  several files atomically (#457)`). An optional interface for file systems
  that can write several whole files as one operation, such as a database
  writing them in one transaction. It is deliberately not embedded in
  `WriteFS`; implementations must be atomic, and a file system that cannot
  guarantee atomicity must not implement it so that the `BatchCreate` polyfill
  falls back to `Create`.
- `MountedArchiveDirFS` - reports whether a directory is a virtual directory
  backed by an archive (such as `data.zip.d`), so that walkers such as
  `ops.Walk` skip it. Wrappers must forward the method.
- `CreateFileFS`, `MkdirAllFS`, `MkdirFS` - see the `WriteFS` decomposition
  above.
- `ReleaseFS` - `Release() error` closes the mounts a file system opened for
  itself (`99b99b9 (#481)`). A nestFS keeps an archive mounted once a path
  inside it has been read, so the handle on the archive file - and for a
  remote archive the local copy downloaded into a temporary directory - stayed
  open until the whole file system was closed, and another application could
  not replace or remove the archive. `nestFS.Release` closes and unmounts only
  the `<archive>.d` content directories ufs mounted automatically, innermost
  first.
- `RemoveFS`, `RemoveAllFS`, `DeviceInfoGet` - replacements for `RemoveFileFS`
  and `DeviceInfoGetter`.
- `DefaultDeviceInfo` - the device info used for a file system that does not
  report its own.

`github.com/cloudfra/ufs`:

- `func BatchCreate(fsys types.WriteFS, files ...types.BatchFile) error` -
  writes a batch to any file system, using `BatchCreateFS` when available and
  falling back to `Create`.
- `type AbsPathGetter interface { GetAbsPath(name string) (string, error) }`
  (`20bbc0d Export the interface AbsPath resolves host paths through (#425)`).
  `ufs.AbsPath` now resolves through this interface; `localFS` and `nestFS`
  implement it.
- `const URLQueryParamChecksum = "ufs.checksum"` (`8a08acc Verify downloads
  against a ufs.checksum query parameter (#407)`). Carries the expected
  checksum of a downloaded file as `<algorithm>:<hex digest>`; the parameter
  is stripped before the request and the download fails on a mismatch.
- `Driver.CreateFromFileFunc func(context.Context, io/fs.File)
  (types.WriteFS, error)` (`e1e15f6 Open host-path archives in nestFS through
  the archive driver (#429)`) - lets a driver build a nested file system from
  an already-open file instead of a URI.
- `FS` gained `types.RenameFileFS` and `types.ReleaseFS`:

  ```go
  // v0.16.1
  type FS interface { WriteFS }

  // main
  type FS interface { types.WriteFS; types.RenameFileFS; types.ReleaseFS }
  ```

  `NewNestFS` and `WrapNestFS` return `FS`, so a nest file system must now
  implement `Rename` and `Release`.

`github.com/cloudfra/ufs/drivers/testing` - new conformance suites that a
driver author adds to a driver's test set: `BatchCreate`,
`BatchCreateWithBuckets` and `Rename`.

`github.com/cloudfra/ufs/testing` - `ReadDirFiles(tb, dir) map[string][]byte`,
`ZipDir(tb, dir) string` and the `EmptyFS` type.

`github.com/cloudfra/ufs/drivers/decorators/telemetryfs` - `Options{Traces,
Metrics bool}`, with YAML forms `telemetry: true` and the explicit mapping.

### Changed signatures

All 37 changed signatures have the same cause: a parameter or result that used
to name a type in the root package now names the same type in
`drivers/types`. This affects `New`, `NewFileInfo`, `NewDriver`,
`NewDecorator`, `URIOrDefault`, `WrapNestFS`, `Decorator.CreateFunc`,
`Driver.CreateFunc`, `FSBuilder.MountFS`, `host.Mount`, and the
`New`/`NewWithClient` constructors of `drivers/decorators/readonlyfs`,
`drivers/embedfs`, `drivers/gcsfs`, `drivers/gitfs` and
`drivers/wrappers/readwrapfs`, plus every exported function of
`drivers/testing`.

Two of the 37 carry a change of their own underneath the re-qualification:

```go
// v0.16.1                              main
type Driver struct { ... }              // gains CreateFromFileFunc (#436)
type FS interface { WriteFS }           // embeds types.RenameFileFS and
                                        // types.ReleaseFS as well (#463, #481)
```

`host.Mount` is otherwise unchanged:

```go
// v0.16.1
func Mount(ctx context.Context, fsys ufs.ReadFS, mountPath string) (MountServer, error)

// main
func Mount(ctx context.Context, fsys types.ReadFS, mountPath string) (MountServer, error)
```

`host.MountServer` itself is unchanged, and `drivers/all`, `drivers/boltfs`,
`cmd/ufsmount` and `cmd/walk` export nothing at either ref, so the split of
`host` into mount drivers (#473) is not visible in the Go API.

### Migration

The move is mechanical. A file that used the root package's types needs one
extra import and a qualifier:

```go
// v0.16.1
import "github.com/cloudfra/ufs"

func describe(fsys ufs.ReadFS) { ... }
fsys, err := ufs.New(ctx, "mem://")
names, err := ufs.List(fsys, ".")

// main
import (
    "github.com/cloudfra/ufs"
    "github.com/cloudfra/ufs/drivers/types"
    "github.com/cloudfra/ufs/ops"
)

func describe(fsys types.ReadFS) { ... }
fsys, err := ufs.New(ctx, "mem://")
names, err := ops.List(fsys, ".")
```

`ufs.New`, `ufs.NewFSBuilder`, `ufs.AbsPath`, `ufs.CreateURI`,
`ufs.AppendURIOption`, `ufs.DecodeOptions`, `ufs.Register`,
`ufs.RegisterDecorator`, `ufs.NewNestFS`, `ufs.WrapNestFS` and the whole
`proto` package keep their names and only change in the types they mention.

A driver that asserted `ufs.RemoveFileFS` asserts `types.RemoveFS` or
`types.RemoveAllFS` now. A driver that implemented `GetDeviceInfo()` implements
`DeviceInfo() types.DeviceMap`. Code that called a capability method directly
on a file system that might not have it can go through the matching
`polyfills` function instead of writing its own fallback.

## Behavior changes

- `ufs.New` returns a nil file system when it fails, instead of a non-nil
  value alongside the error (`7b24e98 (#440)`).
- `ufs.AbsPath` rejects names that escape the root, such as names containing
  `..`, so the result is always at or below the root of the file system
  (`b00cc6c (#443)`).
- URI query parameters prefixed `ufs.` are now passed to the driver instead of
  being treated as mount points (`a08da3f (#441)`).
- Download hardening:
  - a download that exceeds the size limit fails instead of being truncated
    (`e6ffc9d (#438)`);
  - more non-public address ranges are blocked (`b22eb5b (#439)`);
  - `ufs.checksum` verifies the downloaded content (`8a08acc (#407)`).
- Cloning honors the context and reports why a clone failed (`f8e4ade
  (#442)`).
- Change notifications: the data race between `notify.Bus.Publish` and
  `Subscription.Close` is fixed (`82bd114 (#370)`), and delivery stops as soon
  as the subscription context is canceled (`e79b58f (#474)`). The local watcher
  no longer walks past a directory it cannot stat or read without saying so;
  such a directory used to leave its whole subtree unwatched with no error and
  no log line (`97db269 (#483)`).
- `ReadDir` no longer mutates an archive's cached listing (`4fa2a33 (#406)`),
  and a directory inside an archive that the archive does not store now reports
  an `fs.ErrNotExist` path error instead of an empty listing, so a typo no
  longer looks like an empty directory to `fs.WalkDir`, `Glob` or a FUSE
  readdir (`5d0b309 (#527)`, closing #394).
- nestFS no longer asserts that its base is a localFS (`83f9b46 (#428)`), and
  opens host-path archives through the archive driver (`e1e15f6 (#429)`).
- A file open for writing is no longer buffered to emulate `Seek` and
  `ReadAt`: a seek the underlying file does not provide reports
  `fs.ErrInvalid`, and after `Close` every method of the buffered handle
  reports `fs.ErrClosed` (`850f264 (#526)`).
- `memFile` is now built on `drivers/common/buffile`; the old `bufFile` is
  deleted (`8f68a93 (#426)`). Internal, but it changes what `mem://` writes
  cost.
- xz archives with a dictionary larger than 64 MiB can be mounted (`9698dca
  (#409)`).
- `host` was split into separate mount drivers and gained a read-write Cloud
  Files host mount (`0cf8914 (#473)`).
- A read-only file system now reports its files without write permission bits.
  `readOnlyFS` rejected every write but still passed through the wrapped mode,
  so a read-only FUSE or ProjFS mount advertised its files as writable; `Stat`,
  `Lstat`, `ReadDir` and `Open` strip `0o222` and adapt handles through
  `polyfills.ToFile` so the mask never drops `io.ReaderAt`, `io.Seeker` or
  `fs.ReadDirFile` (`d460b1a (#521)`).
- An error from a file system below a mount now names the path the caller used.
  A write through a read-only mount used to fail with
  `create x: permission denied` instead of naming `frozen/x`; the operation and
  the wrapped error are kept, so `errors.Is` and `errors.As` still work
  (`9912703 (#523)`).
- `DeviceInfo` no longer carries an embedded `fmt.Stringer` whose method
  panicked, gains `ThreadCount()` and `Remote()`, and `DeviceMap.Backing`
  resolves a path that is itself a key to that key rather than to its ancestor
  (`3231934 (#522)`).

## Driver and URI changes

- boltfs reads two new options from the URI: `ufs.nosync=true` skips the
  fsync after each commit, and `ufs.nofreelistsync=true` does not write the
  freelist on commit (`e93c6ca (#458)`), for example
  `bolt:/path/to/cache.db?ufs.nosync=true`.
- `drivers/all` now registers the telemetry decorator alongside the drivers.
- localFS implements `types.OpenFileFS` natively (`92ec8c2 (#486)`), so an
  `OpenFile` against a host path no longer goes through an emulation.
- Archive mounts release what they opened: `nestFS.Release` unmounts the
  archives it mounted for itself so another process can replace or delete them
  (`99b99b9 (#481)`).

## Dependency changes

- `go-fuse` v2.12.0 is now `exclude`d in `go.mod`, not merely avoided: it does
  not compile for 32-bit Linux (386, arm) because `splice/pair_linux.go`
  assigns `syscall.Splice`'s result to an `int64` where it returns an `int`,
  and the `exclude` keeps `make deps` from upgrading to it (`f444e9b (#419)`).
  The required version moved v2.11.0 to v2.12.1.
- New direct dependencies: `go.opentelemetry.io/otel` and its `metric`, `sdk`,
  `sdk/metric` and `trace` packages at v1.47.0, for the telemetry decorator
  (#452), and `github.com/mikelolasagasti/xz` v1.0.1, which was already
  indirect and is now used directly by the archive handling.
- `google.golang.org/api` 0.300.0 to 0.308.0 (`e045510 (#454)` and the later
  update commits), `golang.org/x/sys` 0.48.0 to 0.49.0 (`f541fa7 (#427)`),
  `cloud.google/go/pubsub/v2` 2.7.0 to 2.8.0, `cloud.google/go/storage` 1.69.0
  to 1.70.0, `fsouza/fake-gcs-server` 1.55.1 to 1.56.1 and
  `grpc-ecosystem/grpc-gateway/v2` 2.31.0 to 2.32.0.
- The `go` directive is 1.26.0 with `toolchain go1.26.10`; it was 1.26.2 with
  `toolchain go1.26.6`. General module updates: `f1b3a00 (#479)`, `5b5670b
  (#485)`, `7435a5e (#482)`, `3937792 (#471)` and `0bf7032 (#460)`.

## Testing and CI

- New conformance suites for `Rename` and `BatchCreate` in `drivers/testing`.
- A `coredeps` guard program (`internal/tests/depguards/coredeps`) that
  imports only the core `github.com/cloudfra/ufs` package, so the module graph
  shows what a user of the core file system APIs actually pulls in, and the
  dependency guard tests fail if something heavy such as the protocol buffer
  runtime leaks into it (`a57f73c (#464)`).
- Base tests no longer reach into `memFS`, `nullFS` or `angryFS` internals
  (#417, #421, #423), and `TestNew` checks the type of the file system it
  opens (#420).
- Flaky sleeps replaced with deterministic waits and fake clocks (#475, #430,
  #416).
- ProjFS is fixed and its tests run on desktop-experience runners; CI probes
  for ProjFS and reports why it is missing (#467, #431).
- The vendored `third_party` protocol buffer trees are used as checked in
  instead of being re-vendored on every build: the rules depended on the
  `googleapis` and `grpc-gateway` master archives, which CI never keeps, so
  every run downloaded and unpacked about 10,000 files - 13 to 27 seconds on
  the Windows runners (`0db4623 (#500)`).

## Commit list

```console
$ git log --oneline v0.16.1..origin/main
f572064 Add a Rename polyfill for file systems without RenameFileFS (#498)
82aa5f3 Add an OpenFile polyfill that emulates the os.OpenFile flags (#541)
9df6024 Fix documentation that does not match behavior (#525)
8924106 Move nestfs's Seek/ReadAt buffering polyfill into drivers/polyfills (#529)
850f264 Refuse to buffer a writable file and report closed file methods (#526)
5d0b309 Report a missing directory inside an archive as fs.ErrNotExist (#527)
d460b1a Report read-only file systems without write permission bits (#521)
9912703 Report mount errors with the path the caller used (#523)
94b122a Sync template-go to main (b51198c) (#524)
3231934 Clean up the DeviceInfo API and its device map lookup (#522)
0db4623 Use the checked-in third_party proto trees instead of re-vendoring (#500)
823aa52 Add a RemoveAll polyfill for file systems without RemoveAllFS (#495)
39f1ee1 Add a Remove polyfill for file systems without RemoveFS (#497)
a91a0ae ufsforagents (#496)
cc62a92 Add stub (#494)
645e5aa Add experimental posix agent toolchain stub (#493)
f4cee80 Rename batchfile.go to batchcreatefs.go for consistency (#492)
a285a0b Split type files for less merge conflicts and granular interface support (#491)
6513c15 Add a Create polyfill for file systems without CreateFileFS (#490)
35f2c21 Add the ToFile polyfill for types.File (#489)
6935ebc Add a CopyFile polyfill for file systems without CopyFileFS (#488)
99b99b9 Release archive mounts so they stop holding local files open (#481)
92ec8c2 localFS.OpenFile (#486)
5b5670b Update template and dependencies (#485)
64a639a Introduce the polyfills package (#480)
97db269 Report directories the local watcher silently skipped (#483)
7435a5e Update template-go (#482)
f1b3a00 Update dependencies (#479)
3ddea5f Streamline the types (#478)
0cf8914 Add a read-write Cloud Files host mount and split host into mount drivers (#473)
568f6c5 Replace test sleeps with deterministic waits (#475)
e79b58f Stop notify delivery as soon as the subscription context is canceled (#474)
3937792 Update template-go (#471)
5685a57 Breakout build-windows-root to a separate file (#469)
a246db1 Prepare for template-go update (#470)
267de39 Update headers (#468)
380b71c Run the ProjFS tests on desktop-experience runners and fix ProjFS (#467)
e4842a0 Move Rename into the ops package (#465)
a57f73c Add coredeps test (#464)
d61c1d0 Move main types to drivers/types (#463)
0bf7032 Update Go module dependencies (#460)
b57ae04 Add a telemetry decorator that reports traces and metrics (#452)
e93c6ca Take bolt NoSync and NoFreelistSync options from the URI (#458)
26ce399 Propose BatchCreateFS for writing several files atomically (#457)
e045510 Bump google.golang.org/api from 0.300.0 to 0.301.0 (#454)
fe87b45 Implement Rename for every file system (#455)
7b24e98 Return a nil file system when New fails (#440)
af49461 Probe for ProjFS in CI and report why it is missing (#431)
e6ffc9d Fail downloads that exceed the size limit instead of truncating them (#438)
b22eb5b Block more non-public address ranges in downloads (#439)
f8e4ade Honor the context when cloning and report why a clone failed (#442)
a08da3f Pass ufs. query parameters to the driver instead of mounting them (#441)
b00cc6c Reject names that escape the root in AbsPath (#443)
a24408d Move the memFS benchmarks to drivers/core (#437)
0bef9dd Move localFS, tempMountFS and archiveFS to drivers/core (single PR) (#436)
a8b0ca8 Wait for the matching event in TestPublishRespectsPrefix (#430)
8f68a93 Build memFile on drivers/common/buffile and delete bufFile (#426)
e1e15f6 Open host-path archives in nestFS through the archive driver (#429)
83f9b46 Stop nestFS from asserting that its base is a localFS (#428)
1663fb7 Move memFS and bufFile to drivers/core (#424)
f541fa7 Bump golang.org/x/sys from 0.48.0 to 0.49.0 (#427)
20bbc0d Export the interface AbsPath resolves host paths through (#425)
29413b4 Stop base tests from using memFS internals (#423)
a24895b Move nullFS to drivers/core (#422)
ffd1a9f Stop base tests from using nullFS internals (#421)
3af26c0 Make TestNew check the type of the file system it opens (#420)
6b0e848 Move angryFS to drivers/core (#418)
f444e9b Exclude go-fuse v2.12.0, which breaks 32-bit Linux builds (#419)
cbaef05 Stop base tests from using angryFS internals (#417)
a7022c1 Export the device info helpers that localFS uses (#414)
ada9d32 Give construct.go its own constant for the default root (#415)
40507aa Build backend file infos with NewFileInfo (#413)
c8cf06d Add the drivers/core package (#412)
6622a5a Run the fault injector jitter test on a fake clock (#416)
851c8c4 Move copy, list and walk helpers to the ops package (#397)
4fa2a33 Stop ReadDir from changing an archive's cached listing (#406)
82bd114 Fix data race between notify.Bus.Publish and Subscription.Close (#370)
8a08acc Verify downloads against a ufs.checksum query parameter (#407)
9698dca Mount xz archives with a dictionary larger than 64 MiB (#409)
```
