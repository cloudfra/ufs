# Mount testing architecture

> **Draft:** This file documents the proposed mount test architecture.
> Copy to `docs/` when the conformance test framework (#222) lands.

## Overview

Mount tests are organized in two tiers:

1. **Conformance tests** — verify the contract that every mount backend
   must satisfy. Table-driven, platform-neutral assertions.
2. **Implementation-specific tests** — exercise behavior unique to a
   particular mount backend's interception model. Per-platform files.

## Conformance tests

File: `mount_conformance_test.go` (no build tag)

Every mount backend registers itself via a table:

```go
type mountBackend struct {
    name  string
    setup func(t *testing.T, backingFS FS) (mountDir string, cleanup func())
    skip  func(t *testing.T)
}

var mountBackends []mountBackend
```

Platform-specific registration files add backends:

- `mount_conformance_linux_test.go` — registers FUSE
- `mount_conformance_windows_test.go` — registers ProjFS, future WinFSP

Conformance tests assert only on **final state** after the operation
completes. They do not depend on interception timing, so they work for
both synchronous (FUSE, WinFSP) and write-back (ProjFS) models.

### Conformance test coverage

| Category | Tests |
|----------|-------|
| Read | Read file, stat file/dir, readdir, stat non-existent, nested reads, large file |
| Write | Create file, modify file, create in subdirectory |
| Delete | Delete file, verify gone from backing FS |
| Rename | Rename file, verify old gone + new present |
| Mkdir | Create directory, verify in backing FS |
| Read-only | Delete denied, rename denied |
| Lifecycle | Create → read → modify → read → delete → verify gone |
| Mixed | Existing + new + modified + deleted, verify final state |

## Implementation-specific tests

These live in per-platform test files and exercise behavior sensitive
to the mount backend's interception model:

| Concern | Example |
|---------|---------|
| Interception timing | ProjFS syncs on handle close; FUSE syncs immediately |
| Read-only write rejection | ProjFS can't block new file creation; FUSE returns EROFS |
| Append semantics | ProjFS reads entire file and overwrites; FUSE intercepts individual writes |
| Binary/Unicode content | Platform-specific path and encoding edge cases |
| Concurrency | Backend-specific thread pool behavior |

Implementation-specific tests should have maximum parity across platforms:
same scenario names, same final-state assertions where possible.

## File layout

```
mount_conformance_test.go              — shared conformance tests, table-driven
mount_conformance_linux_test.go        — registers FUSE backend
mount_conformance_windows_test.go      — registers ProjFS (future: + WinFSP)
mount_windows_test.go                  — ProjFS unit tests + deep integration tests
mount_linux_test.go                    — FUSE-specific deep tests
```

## Adding a new mount backend

1. Implement `hostMount` for the platform.
2. Add a registration file (`mount_conformance_<platform>_test.go`) that
   appends to `mountBackends`.
3. Run `go test` — all conformance tests execute automatically.
4. Add implementation-specific tests in the platform test file for
   behavior unique to the new backend.
