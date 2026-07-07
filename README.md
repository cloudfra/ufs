# ufs

<img src="logo.png" alt="Logo" width="64" height="64" />

[![CI](https://github.com/cloudfra/ufs/actions/workflows/ci.yaml/badge.svg)](https://github.com/cloudfra/ufs/actions/workflows/ci.yaml) [![Go Reference](https://pkg.go.dev/badge/github.com/cloudfra/ufs.svg)](https://pkg.go.dev/github.com/cloudfra/ufs)

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
| Null       | `null://`   | nullfs.go  | Acts as /dev/null. Writes discarded, reads return empty.  |
| Memory       | `memory:`      | memfs.go       | In-memory storage; lost when the process exits.           |
| Local        | `file:///path` | localfs.go     | Local disk, mounted at a root path via os.OpenRoot.       |
| Google Cloud | `gs://bucket`  | gcsfs.go       | Google Cloud Storage bucket as a read-only file system.   |
| Git          | `git://<url>`  | gitfs.go       | Reads files from a git repository (clones on first open). |
| Archive      | `archive://`   | archivefs.go   | Reads archives (zip, tar, 7z) as read-only FSs.           |
| Nested       | via CreateURI  | nestfs.go      | Layers one or more virtual FSs at specific mount paths    |
|              |                |                | inside a base FS.                                         |

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

## Use ollama with Claude Code

```bash
ANTHROPIC_AUTH_TOKEN="ollama" ANTHROPIC_API_KEY="" ANTHROPIC_BASE_URL="http://mega:11434" claude --model qwen3.6:35b
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md).

## License

Apache 2.0 — see LICENSE.
