# ufs

<img src="logo.png" alt="Logo" width="64" height="64" />

[![Go Reference](https://pkg.go.dev/badge/github.com/cloudfra/ufs.svg)](https://pkg.go.dev/github.com/cloudfra/ufs)
[![CI](https://github.com/cloudfra/ufs/actions/workflows/deploy.yaml/badge.svg)](https://github.com/cloudfra/ufs/actions/workflows/deploy.yaml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

**Unified File System (UFS)** gives Go programs, and the shell, one way to reach
files wherever they live. A local directory, a zip file, a cloud bucket, a git
repository and an in-memory scratch space all open with the same call and
behave like the standard library's [`fs.FS`](https://pkg.go.dev/io/fs#FS), with
writes added.

* **One API for every backend.** Change the URI, not your code.
* **Drop-in for `fs.FS`.** Anything that accepts an `fs.FS` (`fs.WalkDir`,
  `http.FS`, `template.ParseFS`, ...) accepts a UFS file system.
* **Composable.** Mount one file system inside another, look inside archives
  as if they were directories, and wrap any of them as read-only or with
  injected faults for testing.
* **Mountable.** Expose any of it as a real directory on Linux (FUSE) or
  Windows (ProjFS), so tools that know nothing about UFS can use it too.

## Supported integrations

| Storage              | Open it with                            |
|:---------------------|:----------------------------------------|
| Local disk           | `/path/to/dir` or `file:///path/to/dir` |
| Memory               | `memory:`                               |
| Archives             | `/path/to/file.zip` (zip, tar, 7z, rar and compressed tars) |
| Remote archives      | `https://host/file.zip`                 |
| Google Cloud Storage | `gs://bucket/prefix`                    |
| Git repositories     | `https://host/repo.git`                 |
| BoltDB               | `bolt:/path/to/file.db`                 |
| Go `embed.FS`        | `embedfs.New(name, fsys)`               |
| Null                 | `null://`                               |

Host mounting is available on Linux (FUSE) and Windows (ProjFS).

## Quick start

### Mount something with `ufsmount`

`ufsmount` makes any of the storage types above appear as a normal directory.
Download a prebuilt binary from the
[releases page](https://github.com/cloudfra/ufs/releases).

Linux (amd64):

```bash
curl -fsSL -o ufsmount https://github.com/cloudfra/ufs/releases/latest/download/ufsmount-linux_amd64
chmod +x ufsmount
```

Windows (amd64), in PowerShell:

```powershell
Invoke-WebRequest -Uri https://github.com/cloudfra/ufs/releases/latest/download/ufsmount-windows_amd64.exe -OutFile ufsmount.exe
```

Then mount something, for example a git repository:

```bash
mkdir -p /tmp/ufs
./ufsmount -uri https://github.com/cloudfra/ufs.git -mount /tmp/ufs
```

Press `Ctrl-C` to unmount. Linux needs FUSE (`fuse3`) and Windows needs ProjFS
enabled; [docs/ufsmount.md](docs/ufsmount.md) covers setup, every flag, and
which operations each platform supports.

### Use it in a Go program

```bash
go get github.com/cloudfra/ufs
```

```go
package main

import (
  "context"
  "fmt"
  "log"

  "github.com/cloudfra/ufs"

  // Each blank import installs a driver. Local disk, memory and archives
  // are built in; drop the ones you do not need.
  _ "github.com/cloudfra/ufs/drivers/boltfs" // installs bolt:
  _ "github.com/cloudfra/ufs/drivers/core"   // installs null:
  _ "github.com/cloudfra/ufs/drivers/gcsfs"  // installs gs://
  _ "github.com/cloudfra/ufs/drivers/gitfs"  // installs URIs ending in .git
)

func main() {
  ctx := context.Background()

  // Swap the URI for "/srv/data", "release.zip", "gs://bucket/prefix", ...
    fsys, err := ufs.New(ctx, "memory:")
  if err != nil {
    log.Fatal(err)
  }
  defer fsys.Close()

  f, err := fsys.Create("hello.txt")
  if err != nil {
    log.Fatal(err)
  }
  if _, err := f.WriteString("hello, world"); err != nil {
    log.Fatal(err)
  }
  if err := f.Close(); err != nil {
    log.Fatal(err)
  }

  data, err := fsys.ReadFile("hello.txt")
  if err != nil {
    log.Fatal(err)
  }
  fmt.Println(string(data)) // hello, world
}
```

More runnable examples are in [example_test.go](example_test.go) and on
[pkg.go.dev](https://pkg.go.dev/github.com/cloudfra/ufs#pkg-examples).

## Features

### Backends

| Backend        | URI                                                         | Access     | Package          | Notes                                                                                                |
|:---------------|:------------------------------------------------------------|:-----------|:-----------------|:-----------------------------------------------------------------------------------------------------|
| Local          | `file:///path` or a bare path                               | read-write | built in         | Rooted with `os.OpenRoot`; paths cannot escape the root.                                             |
| Memory         | `memory:`                                                   | read-write | built in         | Lost when the file system is closed.                                                                 |
| Archive        | a path ending in an archive extension, or `archive:///path` | read-only  | built in         | `.zip`, `.tar`, `.tar.gz`, `.tar.bz2`, `.tar.xz`, `.tar.lz4`, `.tar.br`, `.tar.zst`, `.7z`, `.rar`.  |
| Remote archive | `http://` or `https://` URL                                 | read-only  | built in         | Downloaded to a temporary directory that is removed on `Close`.                                      |
| Null           | `null://`                                                   | read-write | `drivers/core`   | Like `/dev/null`: writes are accepted and discarded, reads return nothing.                           |
| GCS            | `gs://bucket/prefix`                                        | read-write | `drivers/gcsfs`  | Uses Application Default Credentials and falls back to anonymous access for public buckets.          |
| Git            | any URI ending in `.git`                                    | read-write | `drivers/gitfs`  | Shallow-cloned into a temporary directory that is removed on `Close`; writes change only that clone. |
| BoltDB         | `bolt:/path/to/file.db`                                     | read-write | `drivers/boltfs` | A whole file system in a single [bbolt](https://github.com/etcd-io/bbolt) file.                      |
| `embed.FS`     | none, use `embedfs.New`                                     | read-only  | `drivers/embedfs`| Wraps files compiled into your binary.                                                               |

Any backend can be made read-only with the `readOnly` [decorator](#decorators).

### Archives are directories

When a file system contains an archive, its contents appear next to it under
the archive's name plus `.d`. Nothing needs to be configured, and it works
inside any backend, including archives nested in other archives.

```go
fsys, _ := ufs.New(ctx, "/srv/downloads")
data, _ := fsys.ReadFile("release.zip.d/docs/README.md")
```

### Nested mounts

Combine file systems by mounting them at paths inside a base file system.
There are three ways to describe the layout, and all of them produce the same
kind of file system.

In code, with a builder. This is also how you mount a file system you built
yourself, such as an `embed.FS`:

```go
fsys, err := ufs.NewFSBuilder("file:///srv/data").
  Mount("scratch", "memory:").
  MountFS("assets", embedfs.New("assets", assets)).
  Build(ctx)
```

As a single URI, which is convenient for flags and configuration values:

```go
uri, err := ufs.CreateURI("file:///srv/data", map[string]string{
  "scratch": "memory:",
})
fsys, err := ufs.New(ctx, uri)
```

As a YAML or fstab-style mount table passed to `ufs.New`:

```yaml
- source: "file:///srv/data"
  mountPoint: "."
- source: "gs://my-bucket/reference"
  mountPoint: "reference"
  options:
    - readOnly: true
```

```text
file:///srv/data          .          auto  rw  0  0
gs://my-bucket/reference  reference  auto  ro  0  0
```

### Decorators

Decorators wrap a file system to change how it behaves. List them under
`options` in a mount table; they are applied in order, so the last one is the
outermost layer. Blank-import a decorator's package to enable it.

| Option     | Package                         | Behavior                                                                                     |
|:-----------|:--------------------------------|:---------------------------------------------------------------------------------------------|
| `readOnly` | `drivers/decorators/readonlyfs` | Every write returns `fs.ErrPermission`. The fstab `ro` option maps to it.                    |
| `fault`    | `drivers/decorators/faultfs`    | Injects latency and random errors, to test how your code copes with slow or failing storage. |

```yaml
- source: "memory:"
  mountPoint: "."
  options:
    - fault:
        latency: 100ms
        errorRate: 0.25
```

Write your own with `ufs.NewDecorator` and `ufs.RegisterDecorator`.

### Host mounting from Go

`ufsmount` is a thin wrapper around the `host` package, which you can call
directly:

```go
import "github.com/cloudfra/ufs/host"

server, err := host.Mount(ctx, fsys, "/mnt/data")
if err != nil {
  log.Fatal(err)
}
defer server.Close()
server.Wait() // until unmounted or ctx is canceled
```

| Platform | Mechanism                                                                              | Read | Write                               |
|:---------|:---------------------------------------------------------------------------------------|:-----|:------------------------------------|
| Linux    | FUSE                                                                                   | yes  | yes, if the file system is writable |
| Windows  | [ProjFS](https://learn.microsoft.com/en-us/windows/win32/projfs/projected-file-system) | yes  | no                                  |

### Helpers

Functions that work on any `fs.FS`, using a backend's faster native
implementation when it has one. They live in `github.com/cloudfra/ufs/ops`,
except `ufs.AbsPath`:

| Function                                     | Purpose                                                                            |
|:---------------------------------------------|:-----------------------------------------------------------------------------------|
| `ops.Copy`                                   | Copy one file between two file systems.                                            |
| `ops.Rsync`                                  | Copy a whole tree between two file systems.                                        |
| `ops.List`, `ops.ListFiles`                  | Collect every path, or every file path, under a directory.                         |
| `ops.ForEachFilename`, `ops.ForEachFileInfo` | Stream the same results without building a slice.                                  |
| `ops.Walk`                                   | Walk a tree, skipping directories by glob and optionally descending into archives. |
| `ops.Remove`, `ops.RemoveAll`                | Delete from any file system that supports it.                                      |
| `ufs.AbsPath`                                | Resolve a virtual path to a real path on the host, when there is one.              |

### Change notifications

Backends that implement `ufs.Watcher` (local disk, memory, BoltDB and GCS)
report changes under a directory and everything below it:

```go
if w, ok := fsys.(ufs.Watcher); ok {
  stop, err := w.Watch(ctx, ".", func(op ufs.NotifyOp, name string) {
    log.Println("changed:", name)
  })
  if err != nil {
    log.Fatal(err)
  }
  defer stop.Close()
}
```

### Writing a driver

A driver is a package that implements `ufs.WriteFS` and registers a URI
matcher from `init()` with `ufs.Register`. [drivers/boltfs](drivers/boltfs) is
a complete example, and [drivers/testing](drivers/testing) provides the
conformance suite that every backend runs.

## Command-line tools

| Tool       | Purpose                                    | Docs                                 |
|:-----------|:-------------------------------------------|:-------------------------------------|
| `ufsmount` | Mount any file system as a host directory. | [docs/ufsmount.md](docs/ufsmount.md) |
| `walk`     | Print every file in any file system.       | [docs/walk.md](docs/walk.md)         |

## Development

Everything goes through `make`, which handles test assets, cross-platform
builds, race detection and linting in the right order.

```bash
make build -j$(nproc)   # cross-compile all binaries
make test               # run the tests
make lint               # run the linters
make presubmit          # everything CI runs; do this before opening a PR
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to contribute.

## License

Apache 2.0, see [LICENSE](LICENSE).
