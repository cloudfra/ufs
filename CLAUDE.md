# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
go build ./...

# Test (CGO disabled)
make test

# Test with race detector (required for presubmit)
CGO_ENABLED=1 go test -race ./...

# Run a single test
go test -run TestName ./...

# Lint (requires golangci-lint)
golangci-lint run

# Presubmit (lint + check)
make presubmit

# Deflake tests (runs race tests 10x)
make test-deflake

# Cross-compile all binaries
make build
```

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
| Watcher    | Optional; recursive directory change notifications via Watch                  |

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

### Host mount

```go
func HostMount(ctx context.Context, fsys ReadFS, mountPath string) (MountServer, error)
```

Mounts a virtual FS at a host directory so the OS can access it like a regular
file system. On Linux this uses FUSE via go-fuse/v2. Returns an unimplemented
error on other platforms (Windows support planned).

| File           | Purpose                                                          |
|:---------------|:-----------------------------------------------------------------|
| host.go        | Platform-agnostic MountServer interface and HostMount function   |
| host_other.go  | Stub returning "not implemented" on non-Linux platforms          |
| fuse_linux.go  | FUSE adapter — bridges ufs.ReadFS/FS to go-fuse InodeEmbedder   |

### Supporting files

| File                | Purpose                                                      |
|:--------------------|:-------------------------------------------------------------|
| info.go             | fsInfo — concrete fs.FileInfo implementation                 |
| path.go, path_test.go | validPath — validates paths against fs.ValidPath           |
| op.go               | High-level ops — Rsync copies files between FSes             |
| osutil.go           | OS helpers (file download, etc.)                             |
| localfs_notify.go   | Watcher impl for localFS — recursive fsnotify with path translation |
| testing_test.go     | Shared test harness used by each backend                     |
| assets_test.go      | Test asset loading helpers                                   |

### Conventions

- Keep structs private; expose construction via the public New() factory.
- Factory name arg follows a URI scheme: null://, file:///..., memory:, gs://..., git://..., archive://...
- All path operations call validPath first — returns fs.PathError for invalid paths.
- Each backend has its own file, its own tests, and runs the shared fstest.TestFS harness via testFileSystem.
