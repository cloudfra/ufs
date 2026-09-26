# CLAUDE.md

This project is a go library to provide a unified virtual file system to go applications that's compatible with the fs.FS interface.

It provides features such as:

* Multiple File System interfaces: Local, Cloud Storage, Archive, Git, in-memory, embedded files, etc.
* Nested mounting
* Fault injection
* FUSE and ProjFS mounting

## Design Priorities

1. **Correctness** — every code path must be correct before anything else matters. Verify behavior against the `fs.FS` contract and edge cases (empty archives, implicit directories, symlinks, concurrent access).
2. **Minimize memory allocations** — prefer reusing buffers, avoiding unnecessary copies, and reducing per-operation heap pressure. Profile with `go test -benchmem` to validate.
3. **Performance through minimizing work** — the fastest code is code that doesn't run. Prefer lazy initialization, short-circuit returns, and caching over micro-optimization. Do not trade correctness or allocation discipline for throughput.

## Development

**Always use `make` to build, test, and validate.** Do not run individual `go test` commands or filter builds manually — the Makefile handles cross-platform builds, test asset generation, race detection, and linting in the correct order. Skipping `make` risks missing platform-specific issues (especially `_windows.go` files) and test asset dependencies.

```bash
# Build
make build -j$(nproc)

# Test (includes race detection)
make test

# Linting
make lint

# Full validation before creating or updating a PR
make presubmit
```

Run `make test` and `make lint` during development. Run `make presubmit` before creating or updating a pull request — it is the same check CI runs.

## Architecture

`ufs` is a Go library providing a unified virtual file system abstraction. The module
is github.com/cloudfra/ufs.

### Core interfaces (`ufs.go`)

| Interface  | What it wraps / adds                                                         |
|:-----------|:-----------------------------------------------------------------------------|
| FileInfo   | Name, Size, Mode, ModTime, IsDir, Type, Sys                                  |
| ReadFile   | Read-only file; wraps fs.File                                                |
| File       | Read-write; extends ReadFile with ReaderAt, Seek, StringWriter               |
| ReadFS     | Read-only FS; adds Close, ListFilenames, ForEachIterators                    |
| FS         | Read-write; extends ReadFS with Create, MkdirAll                             |
| Watcher    | Optional; recursive directory change notifications via Watch                 |

### Factory

```go
func New(ctx context.Context, name string) (FS, error)
```

Dispatches to the appropriate implementation based on URI scheme:

| Scheme        | Implementation   | Struct    | Type     | Status  | Behavior                                                 |
|:--------------|:-----------------|:----------|:---------|:--------|:---------------------------------------------------------|
| null://       | nullfs.go        | nullFS    | ro       | Impl.   | /dev/null — writes discarded, reads return empty         |
| memory:       | memfs.go         | memFS     | rw       | Impl.   | In-memory storage; lost when process exits               |
| file:///...   | localfs.go       | localFS   | rw       | Impl.   | Local disk via os.OpenRoot; rejects paths outside root   |
| gs://...      | gcsfs.go         | gcsFS     | ro       | Impl.   | Google Cloud Storage bucket as a virtual FS              |
| git://...     | gitfs.go         | --        | ro       | Impl.   | Reads from a git repo (clones on first open)             |
| archive://    | archivefs.go     | archiveFS | ro       | Impl.   | Reads archives (zip, tar, 7z) as virtual FSs             |

### Layering / nesting

```go
func CreateURI(baseName string, nested map[string]string) (string, error)
```

Creates a URI that mounts additional file systems at specific paths inside a base FS.
The result is nestFS (nestfs.go) which dispatches reads/writes based on mount path
prefix.

A temporary local-mount wrapper (tempMountFS in tempmountfs.go) provides writable
scratch space on top of any read-only FS for implementations that need it.

### Addon wrappers

Wrappers modify the behavior of an existing FS. They are configured via
`MountSpecOptions` fields (YAML mount specs) or applied programmatically.
Wrappers are applied in a fixed order by `applyWrappers()` in construct.go:
ReadOnly → FaultInjector → (future wrappers).

| Wrapper        | File           | Constructor       | Config type   | Behavior                                          |
|:---------------|:---------------|:------------------|:--------------|:--------------------------------------------------|
| ReadOnly       | readonlyfs.go  | ReadOnly(inner)   | bool          | Returns fs.ErrPermission for all write ops        |
| FaultInjector  | faultfs.go     | FaultInjector()   | FaultConfig   | Injects configurable latency and random errors    |

### Host mount (`host` subpackage)

```go
package host // github.com/cloudfra/ufs/host

func Mount(ctx context.Context, fsys ufs.ReadFS, mountPath string) (MountServer, error)
```

Mounts a virtual FS at a host directory so the OS can access it like a regular
file system. On Linux this uses FUSE via go-fuse/v2; on Windows it uses ProjFS.
Returns an unimplemented error on other platforms. The subpackage imports `ufs`
(never the reverse) and only uses its exported API (e.g. `ufs.CwdPath`).

| File (in host/)        | Purpose                                                          |
|:-----------------------|:-----------------------------------------------------------------|
| host.go                | Platform-agnostic MountServer interface and Mount function       |
| host_other.go          | Stub returning "not implemented" on non-Linux/Windows platforms  |
| fuse_linux.go          | FUSE adapter — bridges ufs.ReadFS/FS to go-fuse InodeEmbedder    |
| mount_windows.go       | ProjFS mount server                                              |
| projfs_windows.go      | ProjFS syscall bindings                                          |
| math.go                | Integer clamping helpers for FUSE/ProjFS conversions             |

### Drivers (`drivers/` subpackages)

Drivers outside the base package import `ufs` (never the reverse) and register
themselves in `init()` via `ufs.Register`; callers blank-import the package to
enable its scheme. They may use `internal/` packages.

| Path                     | Purpose                                                          |
|:-------------------------|:-----------------------------------------------------------------|
| drivers/common/buffile/  | Exported fully-buffered file handle for drivers (depends on ufs) |

Shared driver code that depends on `ufs` types goes in `drivers/common/`;
code with no `ufs` dependency goes in `internal/`.

### Supporting files

| File                  | Purpose                                                             |
|:----------------------|:--------------------------------------------------------------------|
| info.go               | fsInfo — concrete fs.FileInfo implementation                        |
| path.go, path_test.go | CwdPath and AbsPath (resolves a virtual path to a host path)        |
| op.go                 | High-level ops — Rsync copies files between FSes                    |
| internal/osutil/      | Path-cleaning wrappers around package os, temp dir/delete helpers   |
| internal/httputil/    | SSRF-hardened file download used by remote archives                 |
| internal/pathutil/    | Path helpers: Validate, RemovePrefix, Split, IsCwd, etc.            |
| internal/ufserrors/   | Error helpers: Join, NewPathError, ErrDirNotEmpty                   |
| internal/notify/      | Prefix-matching change-event bus for in-process Watcher impls       |
| internal/globutil/    | GlobFS — fs.Glob for any FS that only provides ReadDir              |
| localfs_notify.go     | Watcher impl for localFS — recursive fsnotify with path translation |
| testing_test.go       | Shared test harness used by each backend                            |
| assets_test.go        | Test asset loading helpers                                          |

### Conventions

* Keep structs private; expose construction via the public New() factory.
* Factory name arg follows a URI scheme: null://, file:///..., memory:, gs://..., git://..., archive://...
* All path operations call pathutil.Validate first — returns fs.PathError for invalid paths.
* Packages under internal/ must not import the base ufs package.
* Each backend has its own file, its own tests, and runs the shared fstest.TestFS harness via testFileSystem.
