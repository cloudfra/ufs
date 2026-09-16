# Embed RenameFileFS/CopyFileFS in FS — Design Spec

**Revision note:** this spec was first written before PR #281 ("FS Driver
Registration") landed on `main`. Review feedback on the original version
pointed out that #281's direction — pushing every non-`nestFS` backend down
to the `WriteFS` surface — removes most of the "every backend needs a
stub" problem the original version described. This revision replaces that
finding and the per-backend plan; the interface change, the dispatch
algorithm, and the `gcsFS`/`localFS` optimizations are unchanged. It also
adds a guard for a real gap flagged in review: renaming/copying a path
that is itself a mount point, or contains one.

## Goal

`ufs.go` already defines `CopyFileFS` and `RenameFileFS` as optional
interfaces, but `FS` does not embed them yet (the embedding is stubbed out
with a `// TODO: Implement RenameFS` comment). Embed both interfaces into
`FS` so `nestFS` — the mount-aware dispatcher returned by `New` and the
*only* type meant to implement `FS` — exposes `Rename` and `CopyFile`,
detecting when the source and destination of a rename/copy land on the
same underlying backend and that backend implements the optimized
interface, delegating to it directly instead of falling back to a generic
read+write.

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

No backend implements either interface today. Since `FS` embeds `WriteFS`
(not yet `CopyFileFS`/`RenameFileFS`), this compiles fine today.

Separately, `main` gained a real driver registry in PR #281
(`register.go`): `Register(Driver{Name, MatchFunc, CreateFunc, Priority})`,
with every in-tree backend (`angryFS`, `gcsFS`, `localFS`, `memFS`,
`nullFS`, `archiveFS`, `gitFS`/`tempMountFS`) self-registering via its own
`init()`. `construct.go`'s `newBaseFS` is now just
`getRegistrar().match(name)` + `.create(ctx, name)` — the old hardcoded
`isXFSUri` chain is gone. This is orthogonal to (and already solves) the
"how does `New()` find a backend" problem; it does not by itself change
what interface a backend must implement to be usable, which is what this
spec is about.

## Key finding: the backend-facing surface is still typed `FS`, not `WriteFS`

Despite PR #272's title ("Split writable backends into WriteFS, keeping
only nestFS as FS"), the actual field and function types that sit between
a driver and `nestFS` are still `FS` today:

- `nestFS.fsys` is typed `FS`.
- `Driver.CreateFunc` (in `register.go`) is
  `func(context.Context, string) (FS, error)` — every backend's
  registered constructor (`newAngryFS`, `newGCSFS`, `newLocalFS`,
  `newMemFS`, `newNullFS`, the `archive`/`http-archive` closures in
  `archivefs.go`, `newGitFS`) must return `FS` to satisfy it.
- `ReadOnly(inner ReadFS) FS`, `newFaultFS(inner FS, cfg) (FS, error)`,
  and `tempMountFS.lfs FS` — the three wrapper/passthrough layers that sit
  between a raw backend and `nestFS` — are all `FS`-typed too.
- `FSBuilder.MountFS(path string, fsys FS)` and its internal
  `fsBuildMount.fsys FS` field are the same.
- `NewEmbedFS(name string, fsys embed.FS) FS` is the same.

#272 already updated each backend's own `var _ WriteFS = (*X)(nil)`
compile-time assertion (previously `_ FS`), correctly anticipating that
backends themselves should only need `WriteFS`. But the *plumbing that
carries a backend into `nestFS`* was never changed, so today, embedding
`CopyFileFS`/`RenameFileFS` into `FS` would still be a compile-time
forcing function requiring `Rename`/`CopyFile` on every one of: `angryFS`,
`archiveFS`, `embedFS`, `faultFS`, `gcsFS`, `localFS`, `memFS`, `nullFS`,
`readOnlyFS`, `tempMountFS` — ten types, for no reason other than the
plumbing's declared type.

**Fix (this spec's actual scope, per review feedback): re-type the
plumbing as `WriteFS`.** Change:

| Symbol | From | To |
|---|---|---|
| `nestFS.fsys` (nestfs.go) | `FS` | `WriteFS` |
| `Driver.CreateFunc` (register.go) | `func(context.Context, string) (FS, error)` | `func(context.Context, string) (WriteFS, error)` |
| Every backend's registered constructor (`newAngryFS`, `newGCSFS`, `newLocalFS`, `newMemFS`, `newNullFS`, `newGitFS`, the two closures in `archivefs.go`'s `init()`, `newTempMountRemoteArchiveFS`) | return `(FS, error)` | return `(WriteFS, error)` |
| `ReadOnly` (readonlyfs.go) | `func ReadOnly(inner ReadFS) FS` | `func ReadOnly(inner ReadFS) WriteFS` |
| `newFaultFS` / `faultFS.inner` (faultfs.go) | `FS` | `WriteFS` |
| `tempMountFS.lfs` (tempmountfs.go) | `FS` | `WriteFS` |
| `FSBuilder.fsBuildMount.fsys`, `FSBuilder.MountFS` (construct.go) | `FS` | `WriteFS` |
| `applyWrappers` (construct.go) | `func(fsys FS, opts MountSpecOptions) (FS, error)` | `func(fsys WriteFS, opts MountSpecOptions) (WriteFS, error)` |
| `makeNestFS` (nestfs.go) | `func(ctx context.Context, fsys FS, args ...FSArgs) *nestFS` | `func(ctx context.Context, fsys WriteFS, args ...FSArgs) *nestFS` |
| `NewEmbedFS` (embedfs.go) | `func NewEmbedFS(name string, fsys embed.FS) FS` | `func NewEmbedFS(name string, fsys embed.FS) WriteFS` |

None of these ten backend/wrapper types need code changes beyond their
return-type annotations — they already implement everything `WriteFS`
requires. `nestFS` becomes the *only* type in the package required to
implement `CopyFileFS`/`RenameFileFS`, matching its existing sole-`FS`
role (`var _ FS = (*nestFS)(nil)`).

**Compatibility note:** `ReadOnly`, `FSBuilder.MountFS`, and `NewEmbedFS`
are public API. This narrows their return/parameter type from `FS` to
`WriteFS`. A caller passing an `FS` value in is unaffected (`FS` embeds
`WriteFS`, so it's still assignable); a caller that declared
`var x ufs.FS = ufs.ReadOnly(...)` breaks and must change the declared
type to `ufs.WriteFS`. Given #272 already signaled this direction and the
package is mid-refactor (multiple breaking interface changes landed
recently: #272, #281), this is treated as an acceptable breaking change
rather than something to shim around.

`gcsFS` still gets its own optimized `CopyFile`/`Rename` (see below) —
that finding from the original spec is unchanged: its `Create`/`Remove`
are real GCS API calls (covered by `TestGCSFS`, `TestGCSFSRemove`,
`TestGCSFSRemoveAll` against a fake GCS server), so its `Rename`/`CopyFile`
should be real GCS server-side operations, not stubs — this is now purely
an *optional* addition (`gcsFS` implementing `CopyFileFS`/`RenameFileFS`
for `nestFS` to detect), not something required for `gcsFS` to compile.

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
	// reject if oldPath is, or contains, a mount boundary (see below)
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

`copyFileGeneric` is the only option for `localFS.CopyFile` too if
`localFS` chooses to implement `CopyFileFS` — see below, `localFS` does
not implement it at all, so this is purely an `op.go` helper for the
`nestFS` fallback.

### Correctness gotcha #1: mount boundaries (review feedback)

Review flagged: *"what if we are attempting to move a directory that is
actually a mounted file?"* Two distinct hazards, both handled the same
way — reject up front rather than silently corrupting mount state:

1. **`oldPath` is itself a mount point**, or a directory *containing* one
   (e.g. renaming `"ab"` when a separate `FS` is mounted at `"ab/nested"`).
   `nestFS`'s directory-walk fallback (`ForEachFilename` via `fs.WalkDir`)
   transparently descends into mounted sub-FSes when reading, so it would
   happily copy files that live in a *different* backend into the
   destination — silently changing which backend owns that data — and
   then `RemoveAll(oldPath)` would delete the source side while the
   mount's registration in `fsys.mounts.m` still points at the
   now-nonexistent old path. Fix: before doing anything, check whether
   `oldPath` equals or prefixes any entry in `fsys.mounts.m` (a small
   helper alongside `mountMap.getDirectoryList`/`getClosestMount`); if so,
   return an error wrapping `fs.ErrInvalid` ("cannot rename/copy a path
   that is or contains a mount point").
2. **`oldPath` is a virtual archive-mount directory** (`isMountedArchiveDir`,
   e.g. `"foo.zip.d"`) — synthetic, backed by the real file `"foo.zip"`,
   not a normal directory. Same fix: reject with the same error before
   proceeding.

This is a deliberate scope limitation, not full support for relocating a
mount — moving a mount's registration correctly (updating `mountMap.m`,
handling the mounted `*nestFS`'s own lifecycle) is a materially bigger
feature nobody has asked for. Rejecting clearly is safe; silently
corrupting mount state is not.

### Correctness gotcha #2: `os.Root.Rename` does not error on existing destination

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

Only three types need any code beyond the type-signature changes in the
table above — and for all three, implementing `CopyFileFS`/`RenameFileFS`
is now purely optional (an opt-in optimization `nestFS` detects), not
required for anything to compile:

| Backend | Rename | CopyFile | Rationale |
|---|---|---|---|
| `nestFS` | dispatch-or-fallback (above), with the mount-boundary guard | dispatch-or-fallback (above) | the only type required to implement `FS` |
| `localFS` | `Stat(dst)` existence check, then `osFS.Rename` | *(not implemented — falls back to `nestFS`'s generic `copyFileGeneric`)* | `os.Root.Rename` is a real atomic optimization; no native bulk-copy primitive exists on `os.Root`, and `io.Copy` between two `*os.File` already gets the `copy_file_range`/sendfile fast path via `os.File`'s `ReaderFrom`/`WriterTo` — confirmed `wrapFile` returns `*os.File` unwrapped since it already satisfies `File`, so the generic fallback is already about as fast as a bespoke implementation would be |
| `memFS` | move matching map keys (exact key + `prefix+"/"` for descendants) under one lock, no data copy | duplicate the node's `content` bytes directly (`bytes.Clone`); error if source `isDir` | flat-map storage makes both genuinely cheaper than the generic Open/Create round trip |
| `gcsFS` | file: `CopyFile` + `Remove`. Directory: list objects under the `oldPath+"/"` prefix, server-side copy each to the equivalent `newPath` key, then `RemoveAll(oldPath)` | `bucket.Object(dst).CopierFrom(bucket.Object(src)).Run(ctx)` — GCS server-side copy, no bytes transit through this process | matches its *actual* existing pattern (real API calls, not stubs); confirmed with user this scope is in, including directory/prefix iteration, over a permission-denied stub |

Every other backend (`angryFS`, `archiveFS`, `embedFS`, `faultFS`,
`nullFS`, `readOnlyFS`, `tempMountFS`) needs **no changes** once the
plumbing is `WriteFS`-typed — they simply don't implement
`CopyFileFS`/`RenameFileFS`, and `nestFS`'s generic fallback (which only
needs `WriteFS`: `Open`/`Create`/`Stat`/`Remove`/`RemoveAll`/`MkdirAll`)
handles them correctly, including read-only ones (the fallback's own
`Create`/`Remove` calls surface the backend's existing `fs.ErrPermission`
naturally).

## Files to change

| File | Change |
|---|---|
| `ufs.go` | Embed `CopyFileFS`, `RenameFileFS` into `FS`; update doc comment |
| `register.go` | `Driver.CreateFunc` return type `FS` → `WriteFS` |
| `nestfs.go` | `nestFS.fsys` and `makeNestFS` param type `FS` → `WriteFS`; add `Rename`, `CopyFile` with same-mount dispatch + fallback; add the mount-boundary check helper used by both |
| `construct.go` | `applyWrappers`, `FSBuilder.fsBuildMount.fsys`, `FSBuilder.MountFS` type `FS` → `WriteFS` |
| `readonlyfs.go` | `ReadOnly` return type `FS` → `WriteFS` |
| `faultfs.go` | `faultFS.inner`, `newFaultFS` types `FS` → `WriteFS` |
| `tempmountfs.go` | `tempMountFS.lfs` type `FS` → `WriteFS`; update `newTempMountRemoteArchiveFS`/related return types |
| `embedfs.go` | `NewEmbedFS` return type `FS` → `WriteFS` |
| `angryfs.go`, `gcsfs.go`, `localfs.go`, `memfs.go`, `nullfs.go`, `archivefs.go`, `gitfs.go` | Registered constructor return type `FS` → `WriteFS` (no behavior change) |
| `op.go` | Add `copyFileGeneric` and `renameAcrossFS` helpers |
| `localfs.go` | Add `Rename` (existence check + `osFS.Rename`) |
| `memfs.go` | Add `Rename` (in-place key move), `CopyFile` (in-place content duplicate) |
| `gcsfs.go` | Add `Rename`, `CopyFile` using `CopierFrom` server-side copy |
| `testing_test.go` | Extend shared table-driven harness with rename/copy cases across all backends |
| `nestfs_test.go` | Add a test-only fake `WriteFS` implementing `RenameFileFS`/`CopyFileFS` with call counters, mounted twice, to prove same-mount dispatch and cross-mount fallback; add a mount-boundary rejection test |
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
- Rename/copy a path that is, or contains, a mount point → rejected with
  the mount-boundary error, mount state left untouched.
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
- Renaming/copying a path that is or contains a mount point is rejected
  outright rather than supported — see "Correctness gotcha #1" above.
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

Design only — no implementation code has landed yet. This revision
incorporates PR #280 review feedback: the `WriteFS`-vs-`FS` plumbing fix
that shrinks the required-change set from ten backend types down to just
`nestFS`, plus the two optional optimizers (`localFS`/`memFS`) and
`gcsFS`; and the mount-boundary guard for rename/copy. Next step is the
implementation pass against this plan.
