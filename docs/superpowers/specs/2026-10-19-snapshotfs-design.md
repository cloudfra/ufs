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
        // Snapshot must not copy the file system up front: it returns without
        // reading the tree it is snapshotting. A file system that can only
        // produce a view by copying says so in its own documentation, charges
        // the cost in the call that takes it, and is exempt from the no-read
        // assertion the conformance suite makes.
        //
        // Closing the file system closes every view taken from it: reads through
        // a view then return an *fs.PathError wrapping fs.ErrClosed. Closing a
        // view never affects the file system it came from, or any other view.
        //
        // Release, for a file system that also implements ReleaseFS, does not
        // close any view. A view keeps whatever the source let go of — an
        // archive handle, a cached remote file — and reacquires it on the next
        // read through the view. Only Close releases those resources for good.
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
  `types.WriteFS` or `types.Watcher`.
- A view is itself a `types.SnapshotFS`: `Snapshot` on a view returns an
  equivalent view, so snapshotting a snapshot is a no-op rather than an error.
  An implementation states that with the compile-time assertion the repo uses,
  `_ types.SnapshotFS = (*gitSnapshotFS)(nil)`, next to the same
  `_ types.ReadFS = (*gitSnapshotFS)(nil)` it already needs.
- Two views taken with nothing written in between are interchangeable.
- Views are safe for concurrent use by several goroutines.
- `Close` on a view releases its resources and is idempotent; `Close` on the
  source invalidates every view taken from it.
- `Release`, where the source implements `types.ReleaseFS`, closes no view:
  the source keeps the resources reacquired on the next read through a view,
  so `Release` never turns an open view into `fs.ErrClosed`. A view that
  implements `types.ReleaseFS` itself — a `boltFS` view holding a read-only
  transaction, a `gitfs` view holding an object-store handle — lets go of what
  it acquired for its own use and reacquires it on its next read, and `Release`
  on a view never touches its source or a sibling view. A released view is
  still a valid view, so it can be snapshotted again.

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
        // fsys cannot snapshot. The copy is the caller's choice, with its cost
        // visible at the call site. NewTempMountFS takes the URI the result
        // reports, not a directory — gitfs passes the git URI it was given, at
        // drivers/gitfs/gitfs.go:93 — plus a prepare function called with the
        // temporary directory it created:
        //   NewTempMountFS(ctx context.Context, uri string,
        //           prepare func(string) error) (types.WriteFS, error)
        // (drivers/core/tempmountfs.go:113). copyInto walks fsys into the
        // directory it is handed.
        scratch, err := core.NewTempMountFS(ctx, "scratch://snapshot", copyInto(fsys))
        if err != nil {
                return nil, err
        }
        // The result is writable, so the frozen view the caller wanted is the
        // readOnly decorator over it: writes return fs.ErrPermission and the
        // reported modes lose their write bits.
        view = readonlyfs.New(scratch)
}
```

The URI argument is a label, not a location: `tempMountFS.URI()` returns
whatever string it was handed, so the copy cannot be reopened with `ufs.New`.
That is the second way it is weaker than a real view — a gitfs view's `URI()`
round-trips, a scratch copy's does not.

The copy is a workaround, not a snapshot: the temporary directory stays
writable underneath, `Snapshot` of the result copies again rather than freezing,
and `Close` deletes the copy, so the caller must keep the file system open for
as long as it holds the view — the same lifetime rule as a real view whose
source it closed.

## nestFS composition

`nestFS` implements `types.SnapshotFS`, and this is where the feature's main
limitation lives.

A nestFS view is only as stable as its least stable mount. Before taking any
snapshot, `Snapshot` walks the base file system and every entry of the
`mountMap` — including the virtual `name.zip.d` content directories `nestFS`
creates over archives — and classifies each one by whether anything in this
process can still write through it. The test is behavioral: an interface check
is not enough, because on `main` every driver named in the first two rows
asserts `_ types.WriteFS` whether or not its write methods do anything
(`drivers/core/nullfs.go:41`, `drivers/core/archivefs.go:38`,
`drivers/gcsfs/gcsfs.go:51`, `drivers/embedfs/embedfs.go:30`), so a table keyed
on "implements only `types.ReadFS`" would match nothing and every `file://`
mount would be refused by construction.

| Mount | Accepted? | Why |
|:--|:--|:--|
| Reads come from an immutable image the driver holds open, and its write methods return `fs.ErrPermission` (`archiveFS`, `embedFS`, and the `name.zip.d` content directory over either) | yes, used as it stands | there is no writable path into this mount, so nothing in this process can move it under the view |
| Implements `types.SnapshotFS` (`localFS`, `memFS`, `boltFS`, `gitFS`, another `nestFS`) | yes, snapshotted recursively | the backend freezes its own state |
| Writes reach something: the mount itself accepts them (`gcsFS`, `nullFS`, `localFS`, `memFS`) or it wraps a file system that does | **no — the whole call fails** | writes through that mount would leak into a view meant to be frozen |

A driver cannot report "read-only" by its interface, so `nestFS` keys the first
row on the mounts whose write path it can prove dead: `archiveFS` and
`embedFS`, which is all `mountArchive` can put under a `name.zip.d` entry. A
new driver that refuses every write needs to join that list explicitly, which
is the cost of a behavioral rule; `drivers/core/archivefs.go` shows the error
shape the check expects.

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

`drivers/testing/snapshot.go` (new) exports conformance helpers in the shape of
`drivers/testing/batchcreate.go`:

```go
// Snapshot verifies that fsys hands out immutable views: a view shows the state
// as of Snapshot, survives later writes to fsys, and reports fs.ErrClosed once
// its source is closed.
func Snapshot(t *testing.T, createFS func(*testing.T) types.WriteFS)

// SnapshotReadOnly is the subset of Snapshot that needs no writes: the view
// matches its source exactly and reports fs.ErrClosed after the source is
// closed. It is what a read-only source — a view, or a nestFS over read-only
// mounts — is run through.
func SnapshotReadOnly(t *testing.T, createFS func(*testing.T) types.ReadFS)

// SnapshotHistory is Snapshot for file systems that implement
// types.SnapshotHistoryFS: ids are listed, reopened, and stable.
func SnapshotHistory(t *testing.T, createFS func(*testing.T) types.WriteFS)
```

`Snapshot` takes a `types.WriteFS` fixture for the same reason `BatchCreate`
does (`drivers/testing/batchcreate.go:35`): every case that proves a view is
*frozen* writes to the source after the snapshot is taken, and a
`func(*testing.T) types.ReadFS` fixture cannot express those cases at all — it
would leave the suite's central claim untested. Read-only sources are not
dropped; they run through `SnapshotReadOnly`, which is also how a driver checks
that a view of a view is equivalent to the first.

Cases:

- No reads up front: a counting fixture wrapped around the source records its
  `Open`, `ReadFile` and `ReadDir` counts, and fails if any of them changes
  between `Snapshot` and the first read through the view. This is the case
  that makes the "it returns without reading the tree it is snapshotting"
  promise in the `Snapshot` doc comment checkable, and it is what separates a
  backend that freezes its own state from one that fakes a snapshot by copying
  through the public read API. The same fixture proves `ufs.Snapshot` refuses
  rather than copies: for a file system without `types.SnapshotFS` it must
  return an error and still have changed no count.
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
