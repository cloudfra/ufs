# SimpleCacheFS — Design Spec

## Goal

Add `drivers/simplecachefs`, a lossy two-tier cache file system:

- **hot** tier: an in-memory `memory:` FS.
- **warm** tier: a `bolt:` FS on local disk.

Writes land in the hot tier and are flushed to the warm tier in batches, on a
timer or once enough dirty data accumulates. Entries are evicted by TTL and by
capacity (LRU). Data written since the last flush is lost if the process
exits without `Sync` or `Close`. When the cache has no room, writes fail with
`ErrNoSpace` rather than blocking.

This replaces the earlier `cachefs` design (read-through cache in front of an
origin), which was never implemented. There is no origin and no cold tier:
simplecachefs is a standalone FS whose contents may be dropped under
pressure.

Supporting changes:

- `drivers/common/overlay`: a generic upper/lower overlay `ufs.FS` with
  tombstones.
- `drivers/boltfs`: one exported batch-write method.
- `memfs.go`: no changes.

## Configuration

Programmatic only for now. The driver does not call `ufs.Register`, so there is
no URI scheme (like `drivers/embedfs`). A URI form can follow once `Driver.Params`
(#347) lands.

```go
package simplecachefs // github.com/cloudfra/ufs/drivers/simplecachefs

func New(ctx context.Context, cfg Config) (ufs.FS, error)

type Config struct {
	StoragePath      string        // bolt file path. Required.
	MemorySize       int64         // Hot quota. Default 256 MiB.
	StorageSize      int64         // Warm hard limit. Default 1 GiB.
	StorageSoftLimit int64         // Background sweep threshold. Default 80% of StorageSize.
	StorageLowWater  int64         // Sweeps evict down to this. Default 70% of StorageSize.
	Policy           Policy        // Eviction policy. Default PolicyLRU (the only value).
	TTL              time.Duration // Expiry measured from last write. Default 0 (off).
	FlushInterval    time.Duration // Periodic flush. Default 30s.
	FlushThreshold   float64       // Flush when dirty bytes ≥ this × MemorySize. Default 0.05.
}

var ErrNoSpace = errors.New("simplecachefs: out of space")
```

- Zero values take the defaults above.
- Sizes count **file content bytes** only; directories count as zero.
- `New` returns an error when `StoragePath` is empty, a size or duration is
  negative, `FlushThreshold` is outside `(0, 1]`, `Policy` is unknown, or the
  limits are not ordered `StorageLowWater < StorageSoftLimit ≤ StorageSize`.
- The config also carries an unexported clock (`now func() time.Time`) so
  tests control TTL and timers.

## Architecture

```text
             ┌─────────────── simplecachefs ────────────────┐
 caller ───▶ │  index (LRU, sizes, dirty set)                │
             │  overlay.FS                                   │
             │    upper = memory:   (hot)                    │
             │    lower = bolt:     (warm)                   │
             │  maintenance goroutine ◀── timer / signal     │
             │  hard-evict goroutine  ◀── on demand          │
             └──────────────────────────────────────────────┘
```

### Files

| File | Purpose |
|:--|:--|
| `drivers/common/overlay/overlay.go` | `overlay.FS`: upper/lower union with tombstones |
| `drivers/simplecachefs/simplecachefs.go` | `New`, `cacheFS`, the `ufs.FS` methods, `Sync` |
| `drivers/simplecachefs/config.go` | `Config`, `Policy`, defaults and validation |
| `drivers/simplecachefs/index.go` | Per-tier LRU lists, byte totals, dirty set and generations |
| `drivers/simplecachefs/maintain.go` | Maintenance goroutine (flush, TTL, soft sweep) and hard-evict goroutine |
| `drivers/boltfs/batch.go` | `BatchOp`, `Batcher`, `(*boltFS).ApplyBatch` |

## Overlay (`drivers/common/overlay`)

A generic union of two writable `ufs.FS` layers. It has no knowledge of
caching and can be reused elsewhere.

```go
package overlay // github.com/cloudfra/ufs/drivers/common/overlay

type FS struct { /* upper, lower ufs.FS; tombstones */ }

func New(upper, lower ufs.FS) *FS

// Tombstones returns a snapshot of the current tombstones and their sequence numbers.
func (o *FS) Tombstones() map[string]uint64
// ClearTombstones removes each tombstone whose sequence number still matches the snapshot.
func (o *FS) ClearTombstones(snapshot map[string]uint64)

func (o *FS) Upper() ufs.FS
func (o *FS) Lower() ufs.FS
```

`*FS` implements `ufs.FS`.

### Tombstones

- A tombstone on a path hides that path **and everything below it** in the
  lower layer. It never hides anything in the upper layer.
- Tombstones live in memory in the overlay (a map from path to sequence
  number, guarded by a mutex). They are not persisted: the owner of the
  overlay is expected to apply them to the lower layer and then clear them.
- A path is *hidden* when it or any ancestor has a tombstone. The check is
  O(depth).
- Recording a tombstone on a path that already has one assigns a new sequence
  number, so `ClearTombstones` with an older snapshot leaves it in place.

### Operations

- **`Open`, `ReadFile`, `Stat`, `Lstat`, `ReadLink`:** upper first; on
  `fs.ErrNotExist`, lower unless hidden; otherwise `fs.ErrNotExist`. Other
  errors from upper are returned without consulting lower.
- **`ReadDir`:** merge of upper's and lower's entries, sorted by name. Upper
  wins on duplicate names. Lower entries that are hidden are omitted. The
  directory exists if it exists in upper or (unhidden) in lower. A file in one
  layer and a directory of the same name in the other resolves to upper's.
- **`Open` on a directory:** returns a directory file whose `ReadDir` is the
  merged listing, snapshotted at open.
- **`Glob`:** `fs.Glob` over the merged `ReadDir` (`internal/globutil`).
- **`Create`, `MkdirAll`:** go to upper. Missing parents are created in upper
  with `MkdirAll`. Creating a path does not clear tombstones, since upper is
  always visible.
- **`Remove`:** fails with `fs.ErrNotExist` if the path is visible in neither
  layer, and with `ErrDirNotEmpty` if it is a directory whose merged listing
  is non-empty. Otherwise it removes the path from upper (ignoring
  `fs.ErrNotExist`) and records a tombstone.
- **`RemoveAll`:** `RemoveAll` on upper and records a tombstone. Succeeds
  when the path does not exist, like `os.RemoveAll`.
- A tombstone is recorded on every remove, even when lower does not hold the
  path. This keeps removal race-free while the lower layer is being written
  to concurrently (see [Flush](#flush)). The owner drops unneeded tombstones
  when it clears them.
- **`Close`:** closes upper then lower and joins their errors.
- **`Watch`:** returns `errors.ErrUnsupported` in v1.
- **`GetDeviceInfo`, `URI`, `String`:** those of upper, with `String()` as
  `overlay(<upper>, <lower>)`.

## boltfs batch writes

One exported addition to `drivers/boltfs`:

```go
type BatchOpKind int

const (
	BatchPut       BatchOpKind = iota // write file content, creating parents
	BatchMkdir                        // MkdirAll
	BatchRemoveAll                    // RemoveAll; no error if missing
)

type BatchOp struct {
	Kind    BatchOpKind
	Name    string
	Mode    fs.FileMode
	ModTime time.Time
	Content []byte
}

type Batcher interface {
	ApplyBatch(ops []BatchOp) error
}

func (fsys *boltFS) ApplyBatch(ops []BatchOp) error
```

- All ops run in a single `db.Update` transaction, in order. Any failure
  rolls back the whole batch.
- It reuses the existing helpers (`parentBucket`, `createDirBucket`,
  `removeAllChildren`, `encodeBoltRecord`), so it adds a loop, not new
  storage logic.
- `ModTime` is preserved so TTL survives a flush and a reopen.
- Notifications for each op fire after the commit.
- The wasm stub returns `errors.ErrUnsupported`.

## Data model (`index.go`)

Per path, the cache tracks:

| Field | Meaning |
|:--|:--|
| `size` | Content bytes |
| `modTime` | Last write, used for TTL |
| `inHot`, `inWarm` | Which tiers hold the content |
| `dirty` | Hot content not yet flushed to warm |
| `gen` | Incremented on every write; used to detect writes that race a flush |

- One `container/list` LRU per tier, with the most recently accessed entry at
  the front. Reads and writes move the entry to the front of each tier
  holding it.
- `hotBytes`, `warmBytes` and `dirtyBytes` are running totals.
- One mutex guards the index. Tier I/O happens outside it.
- On `New`, the warm tier is walked once (`fs.WalkDir`) to rebuild the warm
  entries and `warmBytes`, with LRU order seeded from `modTime`. The hot tier
  starts empty.

## Data flow

### Writes

- `Create` returns a wrapper around the hot tier's file.
- Before each `Write`/`WriteString`, the wrapper reserves the growth in
  `hotBytes`:
  1. If the reservation fits under `MemorySize`, it proceeds.
  2. Otherwise, clean hot entries are evicted in LRU order (removed from the
     memory FS only; warm still holds them) until it fits.
  3. If there is still no room, the write fails with `ErrNoSpace` wrapped in
     `fs.PathError`, and the maintenance goroutine is signalled to flush.
- While the hard-evict goroutine runs, `Create`, `MkdirAll` and writes fail
  with `ErrNoSpace`.
- On `Close`, the entry is marked dirty, `gen` is incremented, `modTime` is
  updated and `dirtyBytes` is adjusted. If
  `dirtyBytes ≥ FlushThreshold × MemorySize`, the maintenance goroutine is
  signalled.
- `MkdirAll` creates the directory in hot and queues a `BatchMkdir` for the
  next flush, so empty directories persist.
- A file larger than `MemorySize` can never be written, and fails with
  `ErrNoSpace`.

### Reads

- `Open`/`ReadFile`/`Stat`/`ReadDir` go through the overlay.
- A hit updates the LRU.
- A warm-only hit on a file is promoted: its content is copied into hot as a
  **clean** entry if it fits after evicting clean hot entries. Otherwise it is
  served from warm without promotion. Promotion never evicts dirty entries.
- An entry whose TTL has expired is treated as absent, even before the sweep
  removes it.

### Removes

- `Remove`/`RemoveAll` go through the overlay, which deletes from hot
  immediately and records a tombstone.
- The index drops the hot entries (and their dirty state) at once. Warm
  entries stay counted in `warmBytes` until the flush applies the tombstone.

### Flush

The maintenance goroutine flushes on each wake-up when anything is dirty or
tombstoned:

1. Snapshot the overlay's tombstones and the dirty entries (path, `gen`), and
   read each dirty file's content from hot.
2. If `warmBytes` plus the net growth would exceed `StorageSize`, start the
   hard-evict goroutine (if it isn't already running) and drop from the batch
   the dirty entries that don't fit. They stay dirty for the next flush.
3. Build one batch: `BatchRemoveAll` for each tombstone, then `BatchMkdir`
   ops, then `BatchPut` ops. Removes run first so that a path removed and
   re-created before this flush ends up with the new content.
4. `ApplyBatch` the batch.
5. On success, clear dirty only for entries whose `gen` still matches the
   snapshot, update `warmBytes`, and call
   `ClearTombstones(snapshot)`. The sequence-number check means a remove that
   raced the flush keeps its tombstone and is applied by the next flush.
6. On failure, log at `slog.Warn` and leave everything dirty for retry.

`Sync()` runs a flush of everything and blocks until it completes, returning
its error. It is exposed as a method on the returned FS, and simplecachefs
exports `Sync(ufs.FS) error` for callers holding the interface.

### Maintenance goroutine

One goroutine, woken by `FlushInterval` or by a signal on a buffered channel
of size 1 (so signalling never blocks). Each wake-up:

1. Flush (above).
2. TTL: if `TTL > 0`, remove every entry with `now − modTime ≥ TTL` from both
   tiers (hot directly; warm via a batch).
3. Soft sweep: if `warmBytes ≥ StorageSoftLimit`, evict warm entries in LRU
   order until `warmBytes ≤ StorageLowWater`, as one batch. A warm entry
   evicted this way is also dropped from hot if it is clean there.

A write signals the goroutine when dirty bytes cross the flush threshold. A
flush signals it again when `warmBytes` crosses `StorageSoftLimit`.

### Hard-evict goroutine

- Started on demand by a flush that would exceed `StorageSize`. An atomic
  flag ensures at most one runs.
- While it runs, the hard-evict flag makes writes fail with `ErrNoSpace`.
- It evicts warm entries in LRU order down to `StorageLowWater`, clears the
  flag, and signals the maintenance goroutine to retry the flush.

### Space reclamation

bbolt never shrinks its file, but it reuses freed pages for new writes. The
limits count live content bytes, so the `.db` file stays close to its
high-water mark and does not grow without bound. There is no compaction.

### Close

`Close` stops the maintenance goroutine, waits for any hard-evict goroutine,
runs a final flush, then closes the overlay (hot, then warm). It joins their
errors. Methods called after `Close` return `fs.ErrClosed`.

## Error handling

- `ErrNoSpace` is returned (wrapped in `fs.PathError`) by writes that do not
  fit the hot quota and by all writes during hard eviction. Test for it with
  `errors.Is`.
- Flush, TTL and sweep failures in background goroutines are logged with
  `slog` and retried on the next wake-up. They never fail a read.
- A warm read that fails with anything other than `fs.ErrNotExist` returns
  that error.
- Invalid paths fail through `pathutil.Validate` with `fs.PathError`, as in
  every other driver.

## Testing

- **Conformance** (`drivers/testing`):
  - `WriteFS` against `overlay.FS` over two `memory:` layers.
  - `WriteFS` against simplecachefs with the bolt file in `t.TempDir()`.
- **overlay unit tests:**
  - Upper shadows lower; `ReadDir` merge, ordering and duplicates;
    file-vs-directory conflicts.
  - `Remove` of a lower-only file, upper-only file and file in both; a
    non-empty merged directory; `RemoveAll` hiding a subtree; creating a file
    under a tombstoned directory.
  - `ClearTombstones` leaves a tombstone that was re-recorded after the
    snapshot.
- **boltfs:** `ApplyBatch` ordering, atomic rollback on failure, preserved
  `ModTime`, and notifications after commit.
- **simplecachefs unit tests** (injected clock; tests call `Sync` and trigger
  the maintenance goroutine directly rather than sleeping):
  - Config defaults and each validation error.
  - A write over `MemorySize` evicts clean entries; with only dirty entries it
    returns `ErrNoSpace` and signals a flush.
  - Flush at the threshold and on the interval; `Sync` persists everything.
  - A write that races a flush stays dirty (generation check).
  - A remove that races a flush is not resurrected (tombstone sequence).
  - Warm hit promotion, and no promotion when hot has only dirty entries.
  - TTL expiry in both tiers, and expired entries hidden before the sweep.
  - Soft sweep to low water; the hard-evict window returns `ErrNoSpace` and
    the deferred entries flush afterwards.
  - Reopening an existing bolt file rebuilds `warmBytes` and the LRU.
  - `Close` flushes; methods after `Close` return `fs.ErrClosed`.
- All tests run under `make test` (race detector on) and `make lint`.

## Out of scope

- A cold tier or origin, and read-through or write-through to one.
- URI and `MountSpecOptions` configuration (waits for #347 and #348).
- Eviction policies other than LRU.
- `Watch` on the overlay or the cache.
- Compaction of the bolt file.
- Crash durability for data not yet flushed.
