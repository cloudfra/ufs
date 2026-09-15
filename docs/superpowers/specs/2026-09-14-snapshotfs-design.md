# SnapshotFS — Design Spec

## Goal

Extend the existing `Snapshot(fsys FS) SnapshotFS` stub (`snapshot.go`) into a
usable feature: taking a snapshot of a writable `FS` freezes that FS (all
further `Create`/`MkdirAll`/`Remove`/`RemoveAll` calls made through it return
`fs.ErrPermission`) until every snapshot holding the freeze has released it,
and hands the caller back a read-write `SnapshotFS` that behaves like the
frozen FS but lets them stage new writes on top of it. Staged writes can be
flushed to the frozen parent with `Sync`. `Close` releases this snapshot's
hold on the freeze; it never commits staged writes — that is exclusively
`Sync`'s job — so any writes never passed to `Sync` are simply dropped when
`Close` runs.

Multiple snapshots may hold the freeze on the same parent concurrently (each
with its own independent overlay and tombstones), but only one may `Sync` at
a time: `Sync` requires *exclusive* access to the parent (no other snapshot
currently sharing the freeze) and fails immediately, without blocking, if
that's not the case.

All new logic is scoped to `snapshot.go` (and its test file) with one
narrow, unavoidable exception: the freeze itself has to live on `nestFS`,
because every `FS` returned by `New()`/`CreateURI()` is a `*nestFS`
(`ufs.go`), and `nestFS` is the single choke point every write passes
through regardless of which backend is mounted underneath.

## Architecture

```text
Snapshot(fsys FS) (SnapshotFS, error)
  → type-assert fsys to *nestFS
  → parent.acquireWriteLock()        // increments a shared count; always succeeds
  → return &snapshotFS{parent: parent, ...}

snapshotFS (implements FS + SnapshotFS; *snapshotFS also implements the optional Watcher interface)
  reads  → in overlay?      → serve from overlay (memFS)     // overlay always wins: it reflects the latest op
         → tombstoned?      → fs.ErrNotExist
         → else             → serve from parent (frozen, safe to read directly)

  writes → Create/MkdirAll  → write into overlay, clear any tombstone, notify watchers (NotifyCreate)
         → Remove/RemoveAll → tombstone the path(s), best-effort remove from overlay,
                               notify watchers (NotifyRemove) once per deleted node

  Sync()  → requires exclusive access (no other snapshot currently holds the write lock on parent);
            fails immediately otherwise. Applies tombstoned removals to parent, then copies overlay
            contents into parent, then clears overlay + tombstones (repeatable). Does not release
            this snapshot's hold on the write lock.
  Close() → drop overlay + tombstones unconditionally (never commits — that's Sync's job only),
            release this snapshot's hold on the parent's write lock (idempotent)
  Watch()  → (optional Watcher interface) live NotifyCreate/NotifyRemove events for writes staged
             through this snapshot
```

### Why the parent is always frozen, with no read-write mode underneath

The overlay only stores *deltas*; reads for anything not yet in the overlay
fall through live to the parent. This is what keeps an idle snapshot free
(no upfront copy, per the CLAUDE.md work-minimization priority). But it only
gives a correct point-in-time view if the parent cannot change out from
under it — a parent write to a path the snapshot hasn't touched would leak
straight through the merge-read logic. A copy-on-first-read alternative
(materialize a path into the overlay the first time it's read) would allow
concurrent parent writes, but costs a copy on every first read and extra
"have I already materialized this" bookkeeping. Per direction from the
design discussion, this spec keeps the always-frozen model and does not
build copy-on-first-read.

### Why multiple snapshots can share the freeze, but only one can Sync

Two snapshots opened concurrently on the same parent each get their own
independent overlay and tombstones — they don't interact with each other at
all while staging writes, they just both prevent outside writers from
touching the frozen parent. But letting two snapshots `Sync` concurrently
would let their writes race against each other in the parent with no
defined outcome. So the write lock is a shared counter (any number of
snapshots may hold it) while `Sync` additionally requires that this
snapshot is the *only* current holder — i.e. exclusive access — checked and
enforced under the same lock used to acquire/release it, so a concurrent
`Snapshot()`/`Close()` can't race a `Sync()` in progress.

### Data flow — write then Sync

```text
snap, _ := Snapshot(fsys)          // fsys frozen (write lock count: 1)
snap.Create("a/b.txt")             // written into snap's overlay only; fsys still shows old/missing a/b.txt
snap.Remove("c.txt")               // tombstoned in snap; fsys still shows c.txt
snap.Sync()                        // exclusive (count == 1): fsys now has a/b.txt and c.txt is gone; overlay/tombstones cleared
snap.Close()                       // fsys unfrozen (write lock count: 0); nothing left staged to drop
```

```text
snap, _ := Snapshot(fsys)          // fsys frozen
snap.Create("a/b.txt")             // staged only
snap.Close()                       // fsys unfrozen; a/b.txt was never Sync'd, so it is dropped, not written to fsys
```

```text
snapA, _ := Snapshot(fsys)         // write lock count: 1
snapB, _ := Snapshot(fsys)         // write lock count: 2; fsys still frozen for outside writers
snapA.Sync()                       // fails: count == 2, snapA does not have exclusive access
snapB.Close()                      // write lock count: 1
snapA.Sync()                       // succeeds: snapA is now the sole holder
```

## Constraints

- No changes to the public `FS`/`WriteFS`/`ReadFS` interfaces in `ufs.go`.
- No changes to any backend (`localFS`, `memFS`, `gcsFS`, ...) — the freeze
  is enforced once, in `nestFS`.
- Must not regress existing `nestFS` behavior or its `fstest.TestFS`
  conformance suite.
- Any number of snapshots may hold the write lock on a given `*nestFS`
  concurrently; `Sync` enforces exclusivity itself (see above) rather than
  the lock acquisition step.
- No `SnapshotLogEntry`/proto changes in this design (see "Notifications,
  not a log" below) — PR #275, which added `SnapshotLogEntry`, is being
  closed as unneeded now that snapshot changes are surfaced via `Watch`
  instead of a retrievable log.

## Changes

### `nestfs.go` — the write lock

```go
type nestFS struct {
    fsys   FS
    ctx    context.Context
    mounts *mountMap
    args   FSArgs

    writeLockMu    sync.Mutex // guards writeLockCount and gates exclusive Sync access
    writeLockCount int        // number of snapshots currently holding the write lock; 0 = writable
}

func (fsys *nestFS) acquireWriteLock() {
    fsys.writeLockMu.Lock()
    fsys.writeLockCount++
    fsys.writeLockMu.Unlock()
}

func (fsys *nestFS) releaseWriteLock() {
    fsys.writeLockMu.Lock()
    fsys.writeLockCount--
    fsys.writeLockMu.Unlock()
}

func (fsys *nestFS) checkWriteLock(op, name string) error {
    fsys.writeLockMu.Lock()
    held := fsys.writeLockCount > 0
    fsys.writeLockMu.Unlock()
    if held {
        return pathError(op, name, fs.ErrPermission)
    }
    return nil
}

// withExclusiveWriteLock runs fn while holding writeLockMu, but only if the
// caller is currently the sole holder of the write lock (writeLockCount ==
// 1). Used by Sync: refuses to run fn (and returns an error immediately,
// without blocking) if any other snapshot currently shares the freeze, and
// holds the same mutex used by acquireWriteLock/releaseWriteLock for the
// duration of fn so no concurrent Snapshot()/Close() can change the count
// mid-Sync.
func (fsys *nestFS) withExclusiveWriteLock(fn func() error) error {
    fsys.writeLockMu.Lock()
    defer fsys.writeLockMu.Unlock()
    if fsys.writeLockCount != 1 {
        return fmt.Errorf("snapshot: Sync requires exclusive access to the parent, but the write lock is shared by %d snapshot(s)", fsys.writeLockCount)
    }
    return fn()
}
```

`Create`, `MkdirAll`, `Remove`, `RemoveAll` each call `fsys.checkWriteLock(op,
name)` immediately after their existing `fsys.validPath(op, name)` call,
returning the error if non-nil — the same shape `readOnlyFS` already uses
for its permission errors.

Scoping note (matches `readOnlyFS`'s existing scope): the write lock is
checked on whichever `*nestFS` value the caller writes through. A nested
mount's own `*nestFS`, if obtained and used independently of the frozen
top-level handle, is not separately frozen. This is an existing, accepted
limitation of the wrapper pattern in this codebase, not new to snapshots.

### `snapshot.go` — full rewrite

```go
type SnapshotFS interface {
    FS

    // Sync copies all staged writes into the parent FS and applies all
    // staged removals to it. It may be called multiple times; a Sync with
    // nothing staged is a no-op. It does not release this snapshot's hold
    // on the write lock.
    //
    // Sync requires exclusive access to the parent's write lock: it fails
    // immediately, without blocking, if any other snapshot currently
    // shares it.
    Sync() error

    // Close drops any staged writes and removals that were never passed to
    // Sync, without applying them, and releases this snapshot's hold on
    // the parent's write lock. Close never commits anything — that is
    // exclusively Sync's job. Close is idempotent.
    //
    // Close intentionally shadows the Close inherited from FS/ReadFS: it
    // does not tear down the parent FS the way a normal FS.Close would
    // (the parent is owned and shared by the caller outside this
    // snapshot's lifecycle) — it only releases this snapshot's hold on the
    // write lock and discards its own staged state.
    Close() error
}

// *snapshotFS also implements the optional Watcher interface (see
// "Notifications, not a log" below); it is not embedded in SnapshotFS
// itself, matching how Watcher is an optional, type-asserted capability
// everywhere else in this codebase (memFS, localFS, gcsFS, nestFS).
var _ Watcher = (*snapshotFS)(nil)

type snapshotFS struct {
    parent *nestFS

    mu         sync.Mutex
    overlay    FS                  // lazily created; nil until first write or first Watch. See
                                    // "Overlay backing store" below for why this is typed FS, not memFS.
    tombstones map[string]struct{} // fs.ValidPath-form paths removed but not yet synced
    closed     bool

    watchersMu sync.RWMutex
    watchers   []*snapshotWatcher
}
```

`Snapshot(fsys FS) (SnapshotFS, error)`:

1. Type-assert `fsys` to `*nestFS`; return a descriptive error if it isn't
   one (mirrors the existing `ReadOnly`/`FaultInjector` pattern of working
   against the library's own types).
2. Call `parent.acquireWriteLock()` (always succeeds — see "Why multiple
   snapshots can share the freeze" above).
3. Return a `*snapshotFS` wrapping `parent`.

Read methods (`Open`, `Stat`, `Lstat`, `ReadFile`, `ReadLink`, `ReadDir`,
`Glob`): under `fsys.mu`, check the overlay (if non-nil) first → serve from
there if present; else check tombstones (exact path, or an ancestor
directory tombstoned by `RemoveAll`) → `fs.ErrNotExist`; else serve from
`parent`. The overlay is checked first specifically so that re-creating a
path underneath a `RemoveAll`-tombstoned directory (e.g. `RemoveAll("dir")`
then `Create("dir/new.txt")`) makes `dir/new.txt` visible again, even
though `dir` itself is still tombstoned. `ReadDir`/`Glob` union parent
entries (minus tombstoned names) with overlay entries for that directory,
overlay winning on name collisions.

Write methods:

- `Create(name)` / `MkdirAll(name, perm)`: lazily create the overlay
  (`memFS`) on first use, create any needed parent directories in the
  overlay, clear a tombstone on `name` if present, delegate to the overlay,
  then `fsys.notify(NotifyCreate, name)`.
- `Remove(name)`: add `name` to `tombstones`, best-effort remove it from
  the overlay if present (ignore `fs.ErrNotExist`), then
  `fsys.notify(NotifyRemove, name)`.
- `RemoveAll(name)`: walk the merged snapshot view rooted at `name` first
  (via `fs.WalkDir(fsys, name, ...)`, so the walk itself sees the
  overlay+tombstone+parent merge like any other read), collecting the path
  of every node under it (files and directories, `name` included; a plain
  `DirEntry` per node is enough — no `Stat`/`FileInfo` needed, since
  notifications only carry an op and a path, matching `NotifyHook`'s
  existing signature everywhere else in this codebase). Mark `name` as a
  prefix tombstone in `tombstones` and best-effort remove it from the
  overlay if present, then `fsys.notify(NotifyRemove, p)` once for each
  collected path — so a `RemoveAll` on a directory with descendants fires
  one event per deleted node, not just one for the root. This is an
  intentional O(subtree size) cost (the walk), accepted so a watcher sees a
  complete record of everything a `RemoveAll` deleted, the same way a
  recursive delete through a real filesystem watcher would. If `name`
  doesn't exist, `fs.WalkDir` reports that and `RemoveAll` is a no-op (per
  `ufs.go`), consistent with other backends.

All methods — read and write — return `pathError(op, name, fs.ErrClosed)`
if `closed` is true, rather than silently falling through to `parent`
(which may no longer even be frozen by this snapshot at that point).

`Sync()`: calls `parent.withExclusiveWriteLock(func() error { ... })`; if
that returns the exclusivity error, `Sync` returns it unchanged without
touching `overlay`/`tombstones`. Inside the exclusive section, under
`fsys.mu`, applies tombstoned removals to `parent` **before** copying the
overlay, so that a path resurrected in the overlay underneath a tombstoned
ancestor directory (e.g. `RemoveAll("dir")` then `Create("dir/new.txt")`)
survives the sync instead of being deleted by its ancestor's removal:

1. For each tombstoned path, call `parent.RemoveAll` (paths tombstoned via
   `RemoveAll`) or `parent.Remove` (paths tombstoned via `Remove`),
   treating `fs.ErrNotExist` as success (the path may already be gone from
   the parent, or this may be a retried `Sync` after a prior partial
   failure).
2. If the overlay is non-nil, walk it with `ForEachFilename` and, for each
   file, `parent.MkdirAll` its directory then `Copy(overlay, name, parent,
   name)` (reusing `op.go`'s existing helpers).

On success, discard the overlay (drop the reference; the next write
recreates it) and clear `tombstones`.

`Close()`: under `fsys.mu`, if already `closed`, return `nil` (idempotent);
otherwise drop the overlay and clear `tombstones` unconditionally — without
ever calling anything Sync-like — close and clear any active watchers, then
`parent.releaseWriteLock()` and set `closed = true`. `Close` never fails
because of staged, unsynced changes; it simply discards them. Callers who
want staged writes to persist must call `Sync()` themselves before
`Close()`.

### Notifications, not a log

`SnapshotLogEntry` (added to `proto/ufs.proto` in the now-closed PR #275)
is dropped from this design. Rather than accumulating a retrievable log,
`*snapshotFS` implements the existing, optional `Watcher` interface
(`Watch(ctx, name, hook) (io.Closer, error)`, `ufs.go`) exactly the way
`memFS`, `localFS`, `gcsFS`, and `nestFS` already do — callers who want to
observe changes made through a snapshot subscribe with `Watch`, the same
mechanism they'd already use to watch any other backend. This is
implemented as its own small in-process fan-out (`watchersMu` +
`watchers []*snapshotWatcher` on `snapshotFS`, a per-watcher buffered event
channel and goroutine, non-blocking best-effort delivery) copying
`memfs_notify.go`'s existing `memWatcher` pattern rather than sharing code
with it, since `memFS`'s watcher is tied to `memFS`'s own internal node
map. `Watch`'s `name` argument scopes the subscription to a subtree exactly
like the other backends' implementations.

Watch events reflect writes as they're staged into *this snapshot* — they
fire at the same points `Create`/`MkdirAll`/`Remove`/`RemoveAll` above
call `fsys.notify`, not when `Sync` later applies those changes to the
parent. If the parent itself implements `Watcher` (e.g. it's a `memFS`),
its own watchers fire separately, when `Sync`'s `Copy`/`Remove`/`RemoveAll`
calls actually touch it — the snapshot's watch stream and the parent's are
two independent notification domains, the same way the snapshot's read
view and the parent's are independent until `Sync`.

### Overlay backing store

`overlay` is typed as the `FS` interface (not concretely `*memFS`)
specifically so the backing store is swappable later without an interface
change here — e.g. a persistent, disk-backed store (bolt-backed FS,
tracked separately) instead of `memFS`, for snapshots whose staged writes
need to survive a process restart. This design uses `memFS` for v1; nothing
about `snapshotFS` assumes more than the `FS` interface from its overlay.

## Testing

New `snapshot_test.go`:

- `Snapshot` on a memory-backed `FS`: parent `Create`/`MkdirAll`/`Remove`/
  `RemoveAll` all return `fs.ErrPermission` while the write lock is held.
- Two concurrent snapshots on the same parent: both freeze the parent
  (outside writes still fail); `snapA.Sync()` fails with an exclusivity
  error while `snapB` is still open; `snapB.Close()` (without Sync) then
  lets `snapA.Sync()` succeed; the parent still isn't writable until both
  are closed.
- Read-your-writes: `snap.Create` then `snap.Open`/`snap.ReadFile` see the
  new content; parent is unaffected until `Sync`.
- `snap.Remove` on a file that exists only in the parent hides it from
  `snap.Open`/`snap.ReadDir` before `Sync`, and actually removes it from the
  parent after `Sync`.
- `RemoveAll` on a directory hides the whole subtree from the snapshot's
  merged `ReadDir` before `Sync`, and removes it from the parent after.
- `RemoveAll` on a directory followed by `Create` of a file underneath it
  (before `Sync`): the new file is visible through the snapshot immediately
  (overlay wins over the ancestor tombstone), and after `Sync` the parent
  has exactly that one file under the directory — its removed siblings do
  not reappear and the new file is not deleted by the directory removal.
- `Watch` on a snapshot: `snap.Create`/`snap.MkdirAll` fire `NotifyCreate`;
  `snap.Remove` fires one `NotifyRemove`; `snap.RemoveAll` on a directory
  with several files and a nested subdirectory fires one `NotifyRemove` per
  deleted node (root, subdirectory, and every file) — not just one for the
  root. Events fire at staging time, before `Sync`.
- `Sync` is repeatable: call it twice in a row with nothing staged between
  is a no-op and returns nil both times.
- Interleaved `Sync` calls: stage a write, `Sync`, stage another write,
  `Sync` again — parent ends up with both.
- `Close` after staging writes with no `Sync`: the parent never receives
  those writes, and the write lock is released.
- `Close` after `Sync`: releases the write lock; nothing left to drop.
- `Close` is idempotent: calling it twice returns nil both times and does
  not panic or double-release the write lock.
- `Snapshot()` on an `FS` that is not a `*nestFS` (if constructible in a
  test) returns a descriptive error rather than panicking.
- Concurrent writers to the same `snapshotFS` from multiple goroutines don't
  race (run under `go test -race`, per `make test`).

## Known limitations

- **No concurrent read-write access to the parent while any snapshot holds
  the freeze.** This is intentional, not a gap — see "Why the parent is
  always frozen" above. A future copy-on-first-read mode could relax this
  if a concrete use case needs it, but it is out of scope here.
- **A nested mount's own `*nestFS`, if held and used independently of the
  top-level handle that was snapshotted, is not frozen.** Matches the
  existing scoping of `readOnlyFS`/`FaultInjector`; not new to snapshots.
- **Not atomic across the whole `Sync`.** If an error occurs partway
  through applying staged writes/removals to the parent, the parent may
  end up partially updated (same caveat `op.go`'s `Rsync` already
  documents). The overlay/tombstones for the parts that did apply are not
  cleared in that case, so a retried `Sync` will attempt the remainder;
  this is safe because removals treat `fs.ErrNotExist` as success and
  copies simply overwrite.
- **`RemoveAll` on a large subtree costs a full walk**, to fire one
  `NotifyRemove` per deleted node. Callers removing very large trees
  through a snapshot should expect `RemoveAll`'s cost to scale with subtree
  size, not be O(1) as bare `RemoveAll` on most backends is.
- **The freeze is best-effort for v1: it does not retroactively invalidate
  file handles that were already open (via `Create`) before the snapshot
  was taken.** A caller holding such a handle can keep writing through it,
  bypassing the freeze, for as long as it stays open. Closing this gap
  (e.g. gating already-open handles' `Write`/`WriteAt`/`WriteString` on the
  same write lock) is real, straightforward future work, deliberately
  deferred out of v1 to keep this change scoped — tracked as a follow-up
  issue rather than built here.

## Future work (explicitly out of scope for this design)

- **Invalidating already-open write handles** across a freeze (see Known
  limitations above). Tracked in a follow-up GitHub issue.
- **Best-effort permission pre-check before Sync.** Before writing staged
  changes back, `Sync` could conditionally probe write permission on the
  parent to reduce the risk of failing partway through (see "Not atomic
  across the whole Sync" above) — e.g. a cheap preflight check rather than
  discovering a permission error mid-copy. Explicitly fine to skip for v1
  if it turns out complex; the longer-term goal is a conservative,
  best-effort check, not a guarantee.
