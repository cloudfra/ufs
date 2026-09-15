# Embed RenameFileFS/CopyFileFS in FS — Design Spec

## Goal

`ufs.go` already defines `CopyFileFS` and `RenameFileFS` as optional
interfaces, but `FS` does not embed them yet (the embedding is stubbed out
with a `// TODO: Implement RenameFS` comment). Embed both interfaces into
`FS` so every writable backend exposes `Rename` and `CopyFile`, and make
`nestFS` — the mount-aware dispatcher returned by `New` — detect when the
source and destination of a rename/copy land on the same underlying
backend and that backend implements the optimized interface, delegating to
it directly instead of falling back to a generic read+write.

## Current state

```go
// ufs.go (today)
type FS interface {
	WriteFS

	// Copy(srcPath, dstPath string) error
	// TODO: Implement RenameFS
}
```

`CopyFileFS` and `RenameFileFS` are already documented in `ufs.go` with the
contract every implementation must follow:

- `CopyFile(srcPath, dstPath string) error` — errors wrapping
  `fs.ErrNotExist` if `srcPath` is missing, or an error if `dstPath`
  already exists. Copying `"."` returns `fs.ErrPermission`. File-only (no
  directory support) — the doc says "copies a file", unlike `Rename`.
- `Rename(oldPath, newPath string) error` — same existence contract, but
  covers files *and* directories. Renaming `"."` returns
  `fs.ErrPermission`.
- Neither is guaranteed atomic; callers must not assume partial failure
  leaves either side untouched.

No backend implements either interface today — grepping the codebase
turns up zero `func (fsys *X) Rename(` or `CopyFile(` definitions. Since
`FS` embeds `WriteFS` (not yet `CopyFileFS`/`RenameFileFS`), this compiles
fine today.

## Key finding: every rw backend is returned directly as `FS`

`CLAUDE.md`'s architecture table implies only `nestFS` needs new methods
("Every concrete value is a `*nestFS`" — true only for the value returned
by the *public* `New()`). In practice, many more concrete types are
returned as `FS` from internal constructors (`newLocalFS`, `newMemFS`,
`newAngryFS`, `newNullFS`, `newBaseFS`, `newFaultFS`, `newTempMountFS`,
`newGitFS`, `NewEmbedFS`, and `newBaseFS`'s direct returns of `*archiveFS`
/ `*gcsFS`). Since Go requires a concrete type to implement every method
of an interface at the point it's assigned to that interface, embedding
`CopyFileFS`/`RenameFileFS` into `FS` is a **compile-time forcing
function**: every one of these concrete types must gain `Rename` and
`CopyFile` methods, not just `nestFS`.

Confirmed by attempting the interface change: the build breaks in
`nestfs.go`, `localfs.go` (via `newLocalFS`), `memfs.go`, `angryfs.go`,
`nullfs.go`, `archivefs.go`, `gcsfs.go`, `readonlyfs.go`, `faultfs.go`,
`tempmountfs.go`, and `embedfs.go` — eleven files, not one.

A second finding changed the plan for one of them: `gcsFS` looks
read-only in the architecture table (`Type: ro`), but its `Create`,
`MkdirAll`, `Remove`, and `RemoveAll` are **real** GCS API calls, already
covered by `TestGCSFS`, `TestGCSFSRemove`, `TestGCSFSRemoveAll` against a
fake GCS server (`github.com/fsouza/fake-gcs-server/fakestorage`). The
`ro` in the table describes the typical deployment (wrapped in
`ReadOnly()` by a mount spec), not the concrete type's capability. Its
`CopyFile`/`Rename` should follow its *own* real pattern, not the
error-stub pattern used by genuinely read-only types like `archiveFS`.

## Architecture

### Interface change (`ufs.go`)

```go
type FS interface {
	WriteFS
	CopyFileFS
	RenameFileFS
}
```

Drop the stale TODO comment; update the `FS` doc comment to say it now
includes copying and renaming instead of promising to add them later.

### nestFS: mount-aware dispatch + generic fallback

`nestFS` already resolves any path to the specific mounted backend and a
backend-relative sub-path via `getFSAndSubpath(name) (*nestFS, string, error)`
(used by `Open`, `Create`, `Remove`, etc.). `Rename`/`CopyFile` resolve
*both* paths this way:

```go
func (fsys *nestFS) Rename(oldPath, newPath string) error {
	// validPath both, reject "." with fs.ErrPermission
	oldMountFS, oldSub, err := fsys.getFSAndSubpath(oldPath)
	newMountFS, newSub, err := fsys.getFSAndSubpath(newPath)

	if oldMountFS == newMountFS {
		if r, ok := oldMountFS.fsys.(RenameFileFS); ok {
			return r.Rename(oldSub, newSub) // native optimization
		}
	}
	return renameAcrossFS(fsys, oldPath, newPath) // generic fallback
}
```

`CopyFile` mirrors this shape, dispatching to `CopyFileFS` when both paths
share a mount.

**Optimization path**: same mounted backend + that backend implements the
optional interface → delegate directly with backend-relative sub-paths.
This is what lets `localFS.Rename` (atomic OS-level rename) or `gcsFS`'s
server-side copy fire instead of a generic byte-by-byte copy.

**Generic fallback** (cross-mount, or the backend doesn't implement the
optimized interface): implemented as shared helpers (candidates for
`op.go`, alongside the existing `Copy`/`Rsync`/`ForEachFilename`):

- `copyFileGeneric(fsys FS, srcPath, dstPath string) error`: `Stat` src
  (error if missing or a directory — `CopyFileFS` is file-only), `Stat`
  dst (error `fs.ErrExist` if present), then reuse the existing
  `Copy(fsys, srcPath, fsys, dstPath)` helper (`Open` + `Create` +
  `io.Copy`).
- `renameAcrossFS(fsys FS, oldPath, newPath string) error`: same
  existence checks, but branches on `IsDir()`. File: copy then `Remove`
  the source. Directory: `MkdirAll` the destination, walk source files
  via `ForEachFilename` (mirrors `Rsync`'s existing walk), copy each into
  the equivalent destination path (creating parent dirs as needed), then
  `RemoveAll` the source. Not atomic, and (like `Rsync` today) does not
  recreate directories that contain no files — an existing, accepted
  limitation, not a regression introduced here.

Both fallbacks operate through `fsys` itself as both "source FS" and
"dest FS" (same object, different paths), so cross-mount routing is
handled automatically by `nestFS`'s own `Open`/`Create`/`Stat`/`Remove`.

`copyFileGeneric` is also reused directly by `localFS.CopyFile` (see
below), since `localFS` has no native copy optimization to offer beyond
what `io.Copy` already gets for free.

### Correctness gotcha: `os.Root.Rename` does not error on existing destination

`os.Rename` / `os.Root.Rename` follow POSIX `rename(2)` semantics: if
`newpath` exists and is not a directory, it is **silently replaced**, not
rejected. This conflicts with the already-written `RenameFileFS` doc
contract ("returns an error if newPath already exists"). `localFS.Rename`
must `Stat` the destination first and return `fs.ErrExist` if present,
*then* delegate to `osFS.Rename` for the actual atomic move. This adds a
small TOCTOU race, which is acceptable given the interface doc already
states renames are "not guaranteed to be atomic."

`memFS.Rename` does not have this problem — its existence check and the
move happen under the same mutex, so it's actually atomic despite the
interface not requiring it.

## Per-backend plan

| Backend | Rename | CopyFile | Rationale |
|---|---|---|---|
| `nestFS` | dispatch-or-fallback (above) | dispatch-or-fallback (above) | the mount-aware router |
| `localFS` | `Stat(dst)` existence check, then `osFS.Rename` | `copyFileGeneric` shared helper | `os.Root.Rename` is a real atomic optimization; no native bulk-copy primitive exists on `os.Root`, and `io.Copy` between two `*os.File` already gets the `copy_file_range`/sendfile fast path via `os.File`'s `ReaderFrom`/`WriterTo` — confirmed `wrapFile` returns `*os.File` unwrapped since it already satisfies `File` |
| `memFS` | move matching map keys (exact key + `prefix+"/"` for descendants) under one lock, no data copy | duplicate the node's `content` bytes directly (`bytes.Clone`); error if source `isDir` | flat-map storage makes both genuinely cheaper than the generic Open/Create round trip |
| `gcsFS` | file: `CopyFile` + `Remove`. Directory: list objects under the `oldPath+"/"` prefix, server-side copy each to the equivalent `newPath` key, then `RemoveAll(oldPath)` | `bucket.Object(dst).CopierFrom(bucket.Object(src)).Run(ctx)` — GCS server-side copy, no bytes transit through this process | matches its *actual* existing pattern (real API calls, not stubs); confirmed with user this scope is in, including directory/prefix iteration, over the safer ErrPermission-stub alternative |
| `angryFS` | returns `errAngry` | returns `errAngry` | matches every other method on this type |
| `nullFS` | no-op, returns `nil` | no-op, returns `nil` | matches `Remove`/`MkdirAll`/`RemoveAll` |
| `archiveFS` | `fs.ErrPermission` ("archiveFS mounts are read-only, cannot ...") | same | matches existing `Create`/`Remove` stub pattern — this one *is* genuinely read-only |
| `readOnlyFS` | `fs.ErrPermission` | `fs.ErrPermission` | matches `Create`/`Remove` |
| `embedFS` | `fs.ErrPermission` ("embedFS is read-only, cannot ...") | same | matches `Create`/`Remove` |
| `faultFS` | delegates to `inner` through `maybeInjectFault` | same | matches every other passthrough method |
| `tempMountFS` | delegates to `fsys.lfs` | delegates to `fsys.lfs` | thin wrapper around an inner `FS` (always a `*localFS` today) |

Every `var _ WriteFS = (*X)(nil)` compile-time assertion for a type in
this list becomes `var _ FS = (*X)(nil)`, so the compiler enforces full
conformance at the declaration site instead of wherever the type happens
to be returned as `FS`.

## Files to change

| File | Change |
|---|---|
| `ufs.go` | Embed `CopyFileFS`, `RenameFileFS` into `FS`; update doc comment |
| `op.go` | Add `copyFileGeneric` and `renameAcrossFS` helpers |
| `nestfs.go` | Add `Rename`, `CopyFile` with same-mount dispatch + fallback |
| `localfs.go` | Add `Rename` (existence check + `osFS.Rename`), `CopyFile` (via `copyFileGeneric`) |
| `memfs.go` | Add `Rename` (in-place key move), `CopyFile` (in-place content duplicate) |
| `gcsfs.go` | Add `Rename`, `CopyFile` using `CopierFrom` server-side copy |
| `angryfs.go` | Add `Rename`/`CopyFile` returning `errAngry`; upgrade assertion to `FS` |
| `nullfs.go` | Add no-op `Rename`/`CopyFile`; upgrade assertion to `FS` |
| `archivefs.go` | Add `Rename`/`CopyFile` returning `fs.ErrPermission`; upgrade assertion to `FS` |
| `readonlyfs.go` | Add `Rename`/`CopyFile` returning `fs.ErrPermission`; upgrade assertion to `FS` |
| `embedfs.go` | Add `Rename`/`CopyFile` returning `fs.ErrPermission`; upgrade assertion to `FS` |
| `faultfs.go` | Add `Rename`/`CopyFile` delegating through `maybeInjectFault`; upgrade assertion to `FS` |
| `tempmountfs.go` | Add `Rename`/`CopyFile` delegating to `fsys.lfs`; add `FS` assertion |
| `testing_test.go` | Extend shared table-driven harness with rename/copy cases across all backends |
| `nestfs_test.go` | Add a test-only fake `WriteFS` implementing `RenameFileFS`/`CopyFileFS` with call counters, mounted twice, to prove same-mount dispatch and cross-mount fallback |
| `readonlyfs_test.go` | Add `CopyFile`/`Rename` to the existing table-driven permission-denied tests |
| `faultfs_test.go` | Add `CopyFile`/`Rename` to the existing no-fault/always-fault table tests |
| `gcsfs_test.go` | Add `CopyFile`/`Rename` tests against the fake GCS server (file copy, directory/prefix rename, dest-exists error, missing-src error) |

## Test plan

Following the existing shared-harness pattern in `testing_test.go`
(`getAllRegularTestCaseList()` for writable backends, `getAllExceptAngryTestCaseList()`
for the rest, both already expanded to include `nestFS.<backend>`
variants via `appendNestFSTestCase`):

- Rename a file; verify old path gone, new path has the same content.
- Rename a directory with nested files; verify the whole subtree moved.
- Rename onto an existing destination → `fs.ErrExist`.
- Rename a missing source → `fs.ErrNotExist`.
- Rename `"."` → `fs.ErrPermission`.
- Copy a file; verify both paths exist with identical content, source
  untouched.
- Copy a directory → error (file-only interface).
- Copy onto an existing destination → `fs.ErrExist`.
- Copy a missing source → `fs.ErrNotExist`.
- `nestFS`-specific: a fake backend mounted twice under one `nestFS`,
  proving (a) a same-mount rename/copy calls the fake's `Rename`/`CopyFile`
  directly (call counter increments, no `Open`/`Create` calls), and (b) a
  cross-mount rename/copy falls back to the generic path (no call to the
  fake's optimized methods, but the move/copy still lands correctly).
- `gcsFS`-specific: file copy and directory rename against the fake GCS
  server, confirming `CopierFrom` is used (content matches without a
  local round trip) and prefix iteration for directory rename covers all
  child objects.

## Known limitations

- Neither `Rename` nor `CopyFile` is atomic across mounts, or for
  directory renames within a single mount that falls back to the generic
  path (matches the documented contract).
- The generic directory-rename fallback does not recreate wholly empty
  subdirectories at the destination — an existing limitation shared with
  `Rsync`, not a new regression.
- `localFS.Rename`'s destination-exists check has a small TOCTOU race
  against concurrent writers to the same directory; acceptable per the
  documented non-atomicity of `Rename`.
- `gcsFS.Rename` for directories issues one server-side copy per object
  under the prefix (no batch/compose API used) — fine for typical mount
  sizes, but not O(1) for very large trees.

## Status

Design only — no implementation code has landed yet. This spec captures
the brainstorming and discovery from that session (including the interface
survey that expanded scope from "just `nestFS`" to eleven files) so the
next implementation pass can start from a verified plan instead of
re-deriving it via trial compilation.
