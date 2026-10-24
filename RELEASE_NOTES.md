# Release notes

## v0.17.0 (unreleased) - changes since v0.16.1

The baseline for these notes is the newest tag, `v0.16.1` (`3be45d9`,
2026-10-06 06:30:02 +0000, "Introduce all driver"). That tag has no GitHub
release: the newest published release is `v0.16.0` (`b5c7d8b`,
2026-10-06T05:57:42Z), one commit earlier. Everything below is therefore also
valid for `v0.16.0`, except for the single commit that separates the two tags
("Introduce all driver", which registers every driver in `drivers/all`).

The head of these notes is `main` at `0cf8914` ("Add a read-write Cloud Files
host mount and split host into mount drivers (#473)").

```console
$ git rev-list --count v0.16.1..origin/main
50

$ git diff --shortstat v0.16.1..origin/main
 229 files changed, 16945 insertions(+), 5503 deletions(-)
```

This release is dominated by a reorganization of the package layout. The core
interfaces move out of the root package into `drivers/types`, the file
operations move into a new `ops` package, and the in-memory and on-disk
constructors move into a new `drivers/core` package. **These moves are breaking
for importers**: the root package does not keep aliases for the moved names.

## Public API surface

### How this was measured

The exported surface of every non-internal package of the module was dumped at
both refs with a `go/packages` walker that prints one canonical, sorted line
per exported object: package-level functions, variables, constants, named
types and aliases (with their underlying type), methods of exported types,
struct fields (with tags), interface methods and interface embeds. Unexported
names and imported (re-exported) objects are excluded.

The dump was run with `GOOS=linux` and `GOOS=windows` at both refs. The two
results are byte-identical, so the exported package-level surface does not
depend on the target platform; the platform-specific code in `host` and
`drivers/core` only adds methods on unexported types.

| Ref | Exported declarations |
| --- | --- |
| `v0.16.1` | 300 |
| `main` | 331 |

Classifying the diff by declaration identity (kind + package + name):

| | Count |
| --- | --- |
| Added | 108 |
| Changed signature | 38 |
| Removed from its package | 66 |

Every removed declaration reappears elsewhere except `ufs.WalkArgs`'s fields,
which moved with the type. Nothing was deleted outright.

### New packages

| Package | Contents |
| --- | --- |
| `github.com/cloudfra/ufs/drivers/types` | The core interfaces and value types of ufs: `FileInfo`, `ReadFile`, `File`, `ReadFS`, `WriteFS`, `Watcher`, `NotifyOp`, `NotifyHook`, the device-info types and the optional capability interfaces. |
| `github.com/cloudfra/ufs/drivers/core` | The file systems that need no external service and no driver URI: `MakeMemFS`, `MakeNullFS`, `MakeAngryFS`, `MakeLocalFS`, `NewTempMountFS`, plus the archive file system. |
| `github.com/cloudfra/ufs/ops` | The file operations that work against any file system: `Copy`, `Rsync`, `List`, `ListFiles`, `Walk`, `ForEachFileInfo`, `ForEachFilename`, `Remove`, `RemoveAll`, and the new `Rename` and `RenameByCopy`. |
| `github.com/cloudfra/ufs/drivers/decorators/telemetryfs` | A decorator that reports a trace span and a duration metric per operation (#452). It registers itself through `init`, so it is selected by name in a mount's `options` list; its only exported Go symbol is the `Options` config struct. |

### Breaking: the core types moved from `ufs` to `ufs/drivers/types`

`d61c1d0 Move main types to drivers/types (#463)` moved the interfaces that
every driver implements into their own package. The root package keeps `New`,
`NewFileInfo`, `NewDriver`, `NewDecorator`, `URIOrDefault`, `WrapNestFS`,
`AbsPath`, `FS`, `Driver` and `Decorator`, but their signatures now refer to
`drivers/types`. There are no compatibility aliases, so every importer has to
add the import.

Moved from `github.com/cloudfra/ufs` to `github.com/cloudfra/ufs/drivers/types`:

- Types: `FileInfo`, `ReadFile`, `File`, `ReadFS`, `WriteFS`, `Watcher`,
  `NotifyOp`, `NotifyHook`, `DeviceInfo`, `DeviceMap`, `DeviceInfoGetter`,
  `URIGet`, `ExternalPathGet`, `CopyFileFS`, `OpenFileFS`, `RemoveFileFS`,
  `RenameFileFS`, `ListFilenames`, `ForEachFileInfoIter`,
  `ForEachFilenameIter`.
- Constants: `NotifyCreate`, `NotifyWrite`, `NotifyRemove`, `NotifyRename`,
  `NotifyChmod`.
- Variables: `DefaultDeviceMap`.
- Functions: `NewDeviceInfo`, `NewDeviceMap`.

The interface definitions themselves are unchanged, with one exception:
`WriteFS` is now composed of named interfaces instead of declaring its methods
inline.

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
	RemoveFileFS
	CreateFileFS
	MkdirAllFS
}
```

The method set is identical, so existing implementations of `WriteFS` still
satisfy it. The decomposition is what makes the new optional interfaces
composable: `CreateFileFS` (`Create`, embedding `io/fs.FS`) and `MkdirAllFS`
(`MkdirAll`) can now be asserted on their own.

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
native implementation to each driver.

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

### New exported API

`github.com/cloudfra/ufs/drivers/types`:

- `BatchCreateFS` and `BatchFile` (`26ce399 Propose BatchCreateFS for writing
  several files atomically (#457)`). An optional interface for file systems
  that can write several whole files as one operation, such as a database
  writing them in one transaction. It is deliberately not embedded in
  `WriteFS`; implementations must be atomic, and a file system that cannot
  guarantee atomicity must not implement it so that `ufs.BatchCreate` falls
  back to `Create`.
- `MountedArchiveDirFS` - reports whether a directory is a virtual directory
  backed by an archive (such as `data.zip.d`), so that walkers such as
  `ops.Walk` skip it. Wrappers must forward the method.
- `CreateFileFS`, `MkdirAllFS` - see the `WriteFS` decomposition above.
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
- `FS` gained `types.RenameFileFS`:

  ```go
  // v0.16.1
  type FS interface { WriteFS }

  // main
  type FS interface { types.WriteFS; types.RenameFileFS }
  ```

  `NewNestFS` and `WrapNestFS` return `FS`, so a nest file system must now
  implement `Rename`.

`github.com/cloudfra/ufs/drivers/testing` - three new conformance suites that
a driver author adds to a driver's test set: `BatchCreate`,
`BatchCreateWithBuckets` and `Rename`.

`github.com/cloudfra/ufs/testing` - `ReadDirFiles(tb, dir) map[string][]byte`,
`ZipDir(tb, dir) string` and the `EmptyFS` type.

`github.com/cloudfra/ufs/drivers/decorators/telemetryfs` - `Options{Traces,
Metrics bool}`, with YAML forms `telemetry: true` and the explicit mapping.

### Changed signatures

All 38 changed signatures have the same cause: a parameter or result that used
to name a type in the root package now names the same type in
`drivers/types`. This affects `New`, `NewFileInfo`, `NewDriver`,
`NewDecorator`, `URIOrDefault`, `WrapNestFS`, `Decorator.CreateFunc`,
`Driver.CreateFunc`, `host.Mount`, and the `New`/`NewWithClient` constructors
of `drivers/decorators/readonlyfs`, `drivers/embedfs`, `drivers/gcsfs`,
`drivers/gitfs` and `drivers/wrappers/readwrapfs`, plus every exported function
of `drivers/testing`.

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
  as the subscription context is canceled (`e79b58f (#474)`).
- `ReadDir` no longer mutates an archive's cached listing (`4fa2a33 (#406)`).
- nestFS no longer asserts that its base is a localFS (`83f9b46 (#428)`), and
  opens host-path archives through the archive driver (`e1e15f6 (#429)`).
- `memFile` is now built on `drivers/common/buffile`; the old `bufFile` is
  deleted (`8f68a93 (#426)`). Internal, but it changes what `mem://` writes
  cost.
- xz archives with a dictionary larger than 64 MiB can be mounted (`9698dca
  (#409)`).
- `host` was split into separate mount drivers and gained a read-write Cloud
  Files host mount (`0cf8914 (#473)`).

## Driver and URI changes

- boltfs reads two new options from the URI: `ufs.nosync=true` skips the
  fsync after each commit, and `ufs.nofreelistsync=true` does not write the
  freelist on commit (`e93c6ca (#458)`), for example
  `bolt:/path/to/cache.db?ufs.nosync=true`.
- `drivers/all` now registers the telemetry decorator alongside the drivers.

## Dependency changes

- `go-fuse` v2.12.0 is excluded; it breaks 32-bit Linux builds (`f444e9b
  (#419)`).
- `google.golang.org/api` 0.300.0 to 0.301.0 (`e045510 (#454)`).
- `golang.org/x/sys` 0.48.0 to 0.49.0 (`f541fa7 (#427)`).
- General module update (`0bf7032 (#460)`).

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

## Commit list

```console
$ git log --oneline v0.16.1..origin/main
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
e045510 Bump google.golang.org/api 0.300.0→0.301.0 (#454)
fe87b45 Implement Rename for every file system (#455)
7b24e98 Return a nil file system when New fails (#440)
af49461 Probe for ProjFS in CI and report why it is missing (#431)
e6ffc9d Fail downloads that exceed the size limit instead of truncating them (#438)
b22eb5b Block more non-public address ranges in downloads (#439)
f8e4ade Honor the context when cloning and report why a clone failed (#442)
a08da3f Pass ufs. query parameters to the driver instead of mounting them (#441)
b00cc6c Reject names that escape the root in AbsPath (#443)
a24408d Move the memFS benchmarks to drivers/core (#437)
0bef9dd Move localFS, tempMountFS and archiveFS to drivers/core (#436)
a8b0ca8 Wait for the matching event in TestPublishRespectsPrefix (#430)
8f68a93 Build memFile on drivers/common/buffile and delete bufFile (#426)
e1e15f6 Open host-path archives in nestFS through the archive driver (#429)
83f9b46 Stop nestFS from asserting that its base is a localFS (#428)
1663fb7 Move memFS and bufFile to drivers/core (#424)
f541fa7 Bump golang.org/x/sys 0.48.0→0.49.0 (#427)
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
