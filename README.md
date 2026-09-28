# ufs

<img src="logo.png" alt="Logo" width="64" height="64" />

Unified File System (UFS) is a Go library that allows apps to access multiple
storage backends through a single fs.FS-based API. It provides a factory
constructor (ufs.New) that dispatches to the appropriate implementation based on
URI scheme, and a layering helper (ufs.CreateURI) for composing nested file systems.

## Features

* **Unified API** — All backends implement the same FS / ReadFS / File interfaces,
  so you can swap storage without changing application code.
* **Factory constructor** — ufs.New(ctx, uri) opens any supported backend; no
  per-backend import needed at call site.
* **Layering** — ufs.CreateURI() mounts additional virtual file systems at specific
  paths inside a base FS (caching, scratch space, etc.).
* **Cross-platform** — Tested on Linux, macOS, and Windows; includes platform-specific
  build tags where needed.

## File Systems

| Storage      | URI Prefix     | Implementation | Description                                               |
|:-------------|:---------------|:---------------|:----------------------------------------------------------|
| Null         | `null://`      | nullfs.go      | Acts as /dev/null. Writes discarded, reads return empty.  |
| Memory       | `memory:`      | memfs.go       | In-memory storage; lost when the process exits.           |
| Local        | `file:///path` | localfs.go     | Local disk, mounted at a root path via os.OpenRoot.       |
| Google Cloud | `gs://bucket`  | drivers/gcsfs  | Google Cloud Storage bucket as a read-only file system.¹  |
| Git          | `git://<url>`  | drivers/gitfs  | Reads files from a git repository (clones on first open).¹|
| Archive      | `archive://`   | archivefs.go   | Reads archives (zip, tar, 7z) as read-only FSs.           |
| BoltDB       | `bolt:/path`   | drivers/boltfs | Single BoltDB file.¹                                      |
| Nested       | via CreateURI  | nestfs.go      | Layers one or more virtual FSs at specific mount paths    |
|              |                |                | inside a base FS.                                         |

¹ Drivers under `drivers/` register their scheme when imported. For example, add
`import _ "github.com/cloudfra/ufs/drivers/gcsfs"` to open `gs://` URIs with `ufs.New`,
or `import _ "github.com/cloudfra/ufs/drivers/boltfs"` to open `bolt:` URIs.

## Public API

```go
// Open any supported file system from a URI.
func New(ctx context.Context, name string) (FS, error)

// Compose nested mounts: base FS with additional FSs at specific paths.
func CreateURI(baseName string, nested map[string]string) (string, error)
```

### Interfaces

| Interface | Content                                                   |
|:----------|:----------------------------------------------------------|
| ReadFile  | Read-only file; wraps fs.File                             |
| File      | Read-write file; extends ReadFile with ReaderAt, Seek     |
| ReadFS    | Read-only FS; adds Close, ListFilenames, ForEach iterators|
| FS        | Read-write FS; extends ReadFS with Create, MkdirAll       |
| FileInfo  | Name, Size, Mode, ModTime, IsDir, Type, Sys               |

## Host Mount

`host.Mount` (package `github.com/cloudfra/ufs/host`) exposes any virtual file system as a regular directory on
the host OS. On Linux it uses FUSE (read-write); on Windows it uses
[ProjFS](https://learn.microsoft.com/en-us/windows/win32/projfs/projected-file-system)
(read-only). See [docs/ufsmount.md](docs/ufsmount.md) for the `ufsmount`
CLI tool and platform-specific setup instructions.

## Commands

```bash
# Build
go build ./...

# Test (CGO disabled)
make test

# Test with race detector
CGO_ENABLED=1 go test -race ./...

# Run a single test
go test -run TestName ./...

# Lint (requires golangci-lint)
golangci-lint run

# Presubmit (lint + check)
make presubmit

# Deflake flaky tests (runs race tests 10 times)
make test-deflake

# Cross-compile all binaries
make build
```

## Android builds

`make build` builds `android/arm64` on every host. On a `linux/amd64` host it
also builds `android/386`, `android/amd64` and `android/arm/v7`, which need cgo
external linking: the first build downloads Android NDK r28c (about 700 MB)
into `build/toolchain/` and links with its clang. That needs `curl` and `unzip`
on the host (`apt install curl unzip`).

`android/arm/v5` and `android/arm/v6` are not built. NDK r17 and newer only
target ARMv7 (`armeabi-v7a`), so the last NDK that can link them is r16b, the
final release with `armeabi` (ARMv5TE). To build them by hand on a
`linux/amd64` host:

```bash
# Packages: curl, unzip and python3 (apt install curl unzip python3)
curl -LO https://dl.google.com/android/repository/android-ndk-r16b-linux-x86_64.zip
unzip -q android-ndk-r16b-linux-x86_64.zip
# API 23 or newer: Go's runtime/cgo uses the stderr symbol, which bionic
# only exports from Android 6.0 (API 23).
python3 android-ndk-r16b/build/tools/make_standalone_toolchain.py \
    --arch arm --api 23 --install-dir ndk-armeabi
# Use the toolchain's gcc: its clang needs libncurses.so.5 (the libncurses5
# package), which current Debian and Ubuntu releases no longer ship.
CGO_ENABLED=1 GOOS=android GOARCH=arm GOARM=5 \
    CC="$PWD/ndk-armeabi/bin/arm-linux-androideabi-gcc" \
    go build -o build/bin/android/arm/v5/walk ./cmd/walk
```

Use `GOARM=6` for v6. Both `walk` and `ufsmount` link this way. The binaries
require Android 6.0 or newer, and no ARMv5 or ARMv6 device shipped with
Android 6.0 or newer, so these builds are left out of `make build`.

## Use ollama with Claude Code

```bash
ANTHROPIC_AUTH_TOKEN="ollama" ANTHROPIC_API_KEY="" ANTHROPIC_BASE_URL="http://mega:11434" claude --model qwen3.6:35b
```

## License

Apache 2.0 — see LICENSE.
