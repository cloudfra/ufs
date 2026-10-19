# SnapshotFS — Design Spec

## Goal

Let a file system hand out an **immutable, point-in-time view of itself** that
stays stable while the file system keeps being written. The view is an ordinary
`types.ReadFS`, so every existing reader, walker and decorator works on it
unchanged.

`gitfs` is the first implementation. A git repository already stores a series
of snapshots, so it is the natural fit: `Snapshot()` is the commit the
repository is at, and `OpenSnapshot(id)` is any other commit.

Consistency is **in-process only**. A snapshot is consistent against writes
made through the same file system instance inside this process. It is not a
lock against another process, another host, or a remote that moves.

## Non-goals

- **Writable snapshots.** A snapshot is read-only. Staging writes on top of a
  snapshot and flushing them back is a separate feature; `core.NewTempMountFS`
  already gives writable scratch on top of any read-only FS, which covers the
  common case. See "Prior art" for why this spec drops the writable-overlay
  model of the earlier draft.
- **Cross-process or durable snapshots.** No file locks, no on-disk
  checkpoints, no guarantee against a second process writing the host
  directory behind `localFS`, a `gs://` bucket, or a git remote.
- **Snapshotting a nestFS whose writable mounts cannot snapshot.** This is
  refused, not emulated. See "nestFS composition".
- **Change notifications from a snapshot.** An immutable view has no changes
  to report, so a snapshot view does not implement `types.Watcher`.

## Prior art

A SnapshotFS spec was merged on 2026-09-17 as PR #274
(`docs/superpowers/specs/2026-09-14-snapshotfs-design.md`). Its merge commit
`3cde420` is not an ancestor of the current `main`, and the file is absent
from every branch head, so the merged document was lost when `main` was
rewritten. Two stubs survive only in PR refs: `1e3e036` (a non-building
`snapshot.go` stub, marked "needs a rewrite") and `02adb3b` (a
`SnapshotLogEntry` proto message).

That design and this one differ in kind, not just in detail:

| | 2026-09-14 draft | this spec |
|:--|:--|:--|
| What a snapshot is | a read-write overlay on a **frozen** parent | a read-only, immutable view |
| Who provides it | the library, once, inside `nestFS` | each backend, through an optional interface |
| Parent while a snapshot lives | frozen: every write returns `fs.ErrPermission` | untouched: writes keep working |
| Flushing | `Sync()` into the parent, exclusive access | none — there is nothing staged |
| First implementation | `nestFS` + a `memFS` overlay | `gitfs` |

The freeze model cannot serve `gitfs`: a git checkout has no writable parent
to freeze, and its value is precisely that many historical views can coexist
with no effect on anything. Making snapshots a capability a backend *provides*
is what lets a repository, a bolt database and an in-memory tree each answer
the same question with their own cheapest mechanism — the same shape
`BatchCreateFS` uses for atomic batch writes.

## Core API (`drivers/types/snapshot.go`)

Small optional interfaces, in the style of `drivers/types/batchfile.go`. They
are **not** embedded in `ReadFS` or `WriteFS`.

```go
// SnapshotFS is an optional interface for a file system that can hand out an
// immutable view of itself. The view is a plain ReadFS: it satisfies fs.FS and
// friends, and cannot be written because it has no write methods at all.
type SnapshotFS interface {
        // Snapshot returns a read-only view of the file system as of the call.
        // The view does not change afterwards, however the file system is
        // written, until it is closed. Several snapshots may be open at once.
        //
        // The view is consistent with respect to writes made through this file
        // system in this process: a file in the view holds either the content it
        // had when Snapshot was called or the content a later write gave it, and
        // never a mixture. It is not isolated from a writer outside this
        // process.
        //
        // Snapshot must not copy the file system up front. A file system that
        // can only produce a view by copying must say so in its documentation
        // and charge the cost where it takes it.
        //
        // Closing the file system closes every view taken from it: reads through
        // a view then return an *fs.PathError wrapping fs.ErrClosed. Closing a
        // view never affects the file system it came from, or any other view.
        Snapshot() (ReadFS, error)
}

// SnapshotHistoryFS is an optional interface for a SnapshotFS whose views are
// addressable by identifier, so an older view can be named, listed and opened
// again. A git repository is the model: identifiers are commit ids.
type SnapshotHistoryFS interface {
        SnapshotFS

        // Snapshots returns the identifiers of every view this file system can
        // open, newest first. They are opaque to the caller, safe to embed in a
        // URI, and stable for the lifetime of the file system.
        Snapshots() ([]string, error)

        // OpenSnapshot returns the view named id, as it was when Snapshot
        // returned that id. It returns an *fs.PathError wrapping fs.ErrNotExist
        // for an id this file system does not know.
        OpenSnapshot(id string) (ReadFS, error)
}

// SnapshotIDGetter is an optional interface implemented by the view a snapshot
// was taken of, and by a view itself, so that a caller can record which
// revision it is holding and reopen that revision later.
type SnapshotIDGetter interface {
        // SnapshotID returns the identifier of the current revision, or the
        // revision a view shows. It returns "" when the file system has no
        // addressable revisions.
        SnapshotID() string
}
```

Invariants every implementation must hold, and that the conformance suite
checks:

- A view is immutable. It implements `types.ReadFS` and must not implement
  `types.WriteFS`, `types.SnapshotFS`'s write-free counterpart, or
  `types.Watcher`.
- A view is itself a `types.SnapshotFS` whose `Snapshot` returns an equivalent
  view — taking a snapshot of a snapshot is a no-op, not an error.
- Two views taken with nothing written in between are interchangeable.
- Views are safe for concurrent use by several goroutines.
- `Close` on a view releases its resources and is idempotent; `Close` on the
  source invalidates every view taken from it.

## Entry points (`snapshot.go`, new root-package file)

Helpers that dispatch on the optional interfaces, mirroring how `batch.go`
wraps `BatchCreateFS`:

```go
// Snapshot returns an immutable view of fsys. It returns fsys.Snapshot when
// fsys implements types.SnapshotFS. Otherwise it returns an error naming the
// type: this library does not fake a snapshot by copying a file system behind
// the caller's back.
func Snapshot(fsys types.ReadFS) (types.ReadFS, error)

// Snapshots and OpenSnapshot return the identifiers of fsys's addressable
// views and one of those views. They report an error when fsys does not
// implement types.SnapshotHistoryFS.
func Snapshots(fsys types.ReadFS) ([]string, error)
func OpenSnapshot(fsys types.ReadFS, id string) (types.ReadFS, error)

// SnapshotID returns the current revision identifier, or "" when fsys has no
// addressable revisions.
func SnapshotID(fsys types.ReadFS) string
```

A file system that cannot snapshot is not a bug, and copying it into a
`tempMountFS` is the documented workaround when a caller wants a frozen view
anyway:

```go
view, err := ufs.Snapshot(fsys)
if err != nil {
        // fsys does not support snapshots; a copy is the caller's choice,
        // with the copy's cost visible at the call site.
        view, err = core.NewTempMountFS(ctx, scratchDir, copyInto(fsys))
}
```

## nestFS composition

`nestFS` implements `types.SnapshotFS`, and this is where the feature's main
limitation lives.

A nestFS view is only as stable as its least stable mount. Before taking any
snapshot, `Snapshot` walks the base file system and every entry of the
`mountMap` — including mounts `nestFS` creates itself, such as the virtual
`name.zip.d` directories over archives — and classifies each one:

| Mount | Accepted? | Why |
|:--|:--|:--|
| Implements only `types.ReadFS` (`nullFS`, `archiveFS`, `gcsFS`, `embedFS`, a gitfs view) | yes, used as it stands | nothing in this process can write it, so it cannot move under the view |
| Implements `types.SnapshotFS` (`localFS`, `memFS`, `boltFS`, `gitFS`, another `nestFS`) | yes, snapshotted recursively | the backend freezes its own state |
| Implements `types.WriteFS` but not `types.SnapshotFS` | **no — the whole call fails** | writes through that mount would leak into a view meant to be frozen |

The failure is returned before any snapshot is taken, so a rejected call
leaves no views open behind the caller's back. The error names the mount path
and the file system, using the `String()` every backend already provides:

```text
snapshot: mount "data/cache" (boltFS(/tmp/x.db)) is writable and does not
support snapshots; make it read-only, use a driver that supports snapshots,
or snapshot it separately and mount the view
```

The three ways a caller resolves it, in the order they should be tried:

1. Mount a driver that supports snapshots.
2. Snapshot the offending file system on its own and mount the returned view.
   A view implements only `types.ReadFS`, so it is accepted unconditionally —
   which makes any snapshot a legal mount, and is how a nestFS of snapshots is
   built.
3. Mount it read-only. Note that this is **not** enough on its own today:
   `readOnlyFS` asserts `_ types.WriteFS = (*readOnlyFS)(nil)`, so a
   `readOnly:` mount still type-asserts as writable and is refused unless the
   file system underneath supports snapshots. `readOnlyFS` forwards
   `Snapshot` to what it wraps, so `readOnly` over `gitfs` works and
   `readOnly` over `localFS` does not. That is deliberate: `readOnlyFS` stops
   writes made through the mount, not writes made through another handle to
   the same backend, and `nestFS` cannot prove no such handle exists.

The returned view is a `snapshotNestFS`: the same dispatch table as `nestFS`
(`getClosestMount`, the archive-directory rules, `Glob`), reading from the
frozen views instead of the live mounts. It is a `types.ReadFS`, so
`Create`, `MkdirAll`, `Remove`, `RemoveAll` and `Rename` are absent by type
rather than returning permission errors.

`Snapshot` of a nestFS whose mounts are all read-only is nearly free: no
mount is copied, and the view holds references to the same backends.

## gitfs as the first implementation

`drivers/gitfs/gitfs.go` today answers a `.git` URI by cloning into a
temporary directory and returning `core.NewTempMountFS` over it:
`prepareGitDirectory` runs `git.PlainCloneContext` with `Depth: 1` and
`SingleBranch: true`, then deletes `.git`, `.gitignore` and `.gitmodules`. So
the driver keeps no history, and the object store it would need is deleted
before the caller ever sees the file system. Implementing `SnapshotFS` starts
with not deleting it.

```text
drivers/gitfs/
  gitfs.go            clone; keeps .git; parses the new ufs.* options
  gitfs.go            gitFS: Snapshot, Snapshots, OpenSnapshot, SnapshotID
  snapshot.go         gitSnapshotFS: a ReadFS over one commit's tree
  internal/gitobj/    go-git plumbing wrapper: repo open, rev parse, tree walk,
                      blob read, commit log
```

- **Views are read from the object store, not checked out.** `Open`/`Stat`/
  `ReadDir`/`ReadFile`/`ReadLink` resolve a path against the commit's tree and
  stream the blob. No per-snapshot working tree, so a repository with a hundred
  open snapshots costs one clone. This follows the "minimize work" priority in
  `CLAUDE.md`; a checkout per snapshot is the fallback, not the default.
- **`GetAbsPath` on a gitfs view returns the not-supported error, which is a
  change from the current gitFS.** A view has no host path because nothing is
  checked out. Today's gitFS does answer `ufs.AbsPath`: `New` returns
  `core.NewTempMountFS` over a `localFS`, and the wrapper forwards the call, so
  a name resolves inside the temporary clone directory — a path that exists
  only while that file system is open. A view must stop answering it, so a
  caller that needs real paths (host mounts, `ExternalPathGet`) asks for
  checkout mode instead, a separate option because it changes the cost model.
- **Identifiers are lowercase hex commit ids.** `SnapshotID()` is the commit
  the configured ref points at. `Snapshots()` walks commits reachable from
  that ref. Ref names (`main`, `v1.2.3`) are accepted by `OpenSnapshot` and
  resolved to an id; the resolved id is what `SnapshotID` on the view reports.
- **History costs a deeper clone, so it is opt-in.** `Depth: 1` yields exactly
  one commit, so `Snapshots()` on today's clone returns one id. New URI
  options, parsed with `httputil.ParseURI` as bolt's options are:
  `ufs.depth=<n>` and `ufs.fullhistory=true`.
- **A view is reopenable by URI.** `URI()` on a view appends the reserved
  `ufs.snapshot=<id>` query parameter, the same convention as bolt's
  `ufs.readonly`, so `ufs.New(ctx, view.URI().String())` reproduces it.
- **A gitfs view is a pure `types.ReadFS`**, so `nestFS` accepts it as a
  stable mount without asking it for another snapshot.

## Later implementations

Named here because the interface must not be shaped around git alone.

- **`memFS`** — the cheapest case. Its tree is a pointer-based node map, and
  open handles already take a detached copy of a file's content and stamp it
  back on sync (`syncToFS` in `drivers/core/memfs.go`), so nothing mutates a
  node in place. A snapshot can therefore be a new root pointer plus node
  sharing rather than a copy, provided writes keep replacing nodes instead of
  editing them.
- **`boltFS`** — a view holds a read-only bolt `Tx`. Bolt gives exactly the
  in-process, point-in-time semantics this interface needs, and the view stays
  valid until closed even while the database is written. It pins the
  transaction, so a long-lived view delays freelist reuse; that cost belongs
  in the driver's documentation.
- **`localFS`** — needs a real decision: hardlink/copy-on-write tree, or a
  watched and replayed change log. Neither is in-process-consistent for free,
  because another process writes the same host directory. Deferred until a
  consumer needs it.
- **Decorators** — `telemetryFS` and `faultFS` forward `Snapshot` to what they
  wrap and return the error unchanged when it does not support snapshots;
  `readOnlyFS` does the same, as described above. A decorator that cannot
  forward must not implement the interface, so that `nestFS`'s check sees the
  truth.

## Testing

`drivers/testing/snapshot.go` (new) exports a conformance helper in the shape
of `drivers/testing/batchcreate.go`:

```go
// Snapshot verifies that fsys hands out immutable views: a view shows the
// state as of Snapshot, survives later writes, and reports fs.ErrClosed once
// its source is closed.
func Snapshot(t *testing.T, createFS func(*testing.T) types.ReadFS)

// SnapshotHistory is Snapshot for file systems that implement
// types.SnapshotHistoryFS: ids are listed, reopened, and stable.
func SnapshotHistory(t *testing.T, createFS func(*testing.T) types.ReadFS)
```

Cases:

- View immutability: write `a.txt`, snapshot, overwrite `a.txt`, delete it,
  create `b.txt`. The view still shows the original `a.txt` and no `b.txt`;
  the live file system shows the new state. `ReadDir`, `Stat`, `ReadFile`,
  `Open` and `Glob` all agree with each other.
- Deep view: a snapshot taken before a `RemoveAll` of a subtree still lists
  that subtree in full; the live file system no longer has it.
- Snapshot of a snapshot: the second view is equivalent to the first.
- Two views at once, written to between: each shows its own state.
- Source closed: every open view returns an `*fs.PathError` wrapping
  `fs.ErrClosed` from `Open`, `Stat`, `ReadDir` and `ReadFile`.
- View closed: idempotent; the source and sibling views keep working.
- Concurrency: snapshots taken and read while another goroutine writes, run
  under `go test -race` as `make test` does.
- nestFS composition: a nestFS over read-only mounts snapshots successfully;
  one over a writable mount that supports snapshots snapshots successfully
  and shows both frozen; one over a writable mount that does not support
  snapshots fails with an error naming that mount, and leaves no view open.
- nestFS of snapshots: mounting a view inside a nestFS is accepted, and the
  resulting nestFS can itself be snapshotted.
- gitfs: served from a repository fixture with several commits — `Snapshots`
  lists them newest first, `OpenSnapshot` on an older id shows the older
  content, `OpenSnapshot` on an unknown id reports `fs.ErrNotExist`, and a
  view's `URI()` round-trips through `ufs.New`.

## Known limitations

- **A nestFS with a writable mount that does not support snapshots cannot be
  snapshotted at all.** Refusing is the only honest option: a partial view
  that quietly tracks a live mount would be worse than no view. The three
  resolutions are listed under "nestFS composition".
- **A `readOnly:` mount does not by itself satisfy the check**, because
  `readOnlyFS` is a `types.WriteFS` and cannot prove that no other handle to
  the wrapped backend exists in this process.
- **In-process only.** Another process writing the host directory behind
  `localFS`, a `gs://` bucket, or a git remote is not excluded, and a view
  may reflect such a change in part. Nothing in this design pretends
  otherwise; there is no locking at the backend boundary.
- **Views do not survive `Close` of their source**, and nothing survives
  process exit. A durable snapshot is a different feature.
- **Views do not notify.** A view does not implement `types.Watcher`; code
  that watches must hold the live file system instead. Watching a live file
  system and reading a view are separate concerns.
- **`gitfs` history costs a deeper clone.** With today's `Depth: 1` clone,
  `Snapshots()` returns one id.

## Out of scope

- Writable snapshots, staging overlays, and anything equivalent to the old
  draft's `Sync`.
- A `snapshot` decorator that adds an overlay to a view. If it is wanted, it
  belongs in `drivers/decorators/` and registers like `readOnly` and `fault`.
- Cross-process snapshot locks, durable snapshot files, and snapshot
  replication.
- `localFS` snapshots, which need their own design.
- Host mounts (`host/`) of a snapshot view. A view is a read-only `ReadFS`,
  so the read-only FUSE path is the natural starting point when someone needs
  it.
