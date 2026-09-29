# SimpleCacheFS — Design Spec

## Goal

Add `drivers/simplecachefs`, a lossy two-tier cache file system:

- **hot** tier: an in-memory `memory:` FS.
- **warm** tier: a `bolt:` FS on local disk. Optional: with no storage path
  the cache is memory-only.

Writes land in the hot tier. A write log collects them, dedupes repeated
writes to the same path, and flushes them to the warm tier in one bolt
transaction, on a timer or once enough dirty data accumulates. Entries are
evicted oldest-first (FIFO by modification time), by TTL and by capacity.
When the cache has no room, writes fail with `ErrNoSpace` rather than
blocking.

`Sync` and `Close` persist everything to the warm tier. Only a crash loses
data: whatever was written since the last flush.

simplecachefs is a standalone FS, not a cache in front of an origin. It is a
temporary, purpose-built component and is configured programmatically only;
it has no URI scheme.

Supporting changes:

- `drivers/common/overlay`: a generic N-layer overlay `ufs.FS` with
  tombstones.
- `proto/writelog`: a `WriteLogEntry` protobuf message.
- `ufs` (base package): `WriteLogEmitter` and `BatchWriter` interfaces.
- `drivers/common/writelog`: a deduping write log, and `Apply`, which writes
  entries to any `ufs.FS`.
- `memfs.go`: implements `WriteLogEmitter`.
- `drivers/boltfs`: implements `BatchWriter`.

## Configuration

```go
package simplecachefs // github.com/cloudfra/ufs/drivers/simplecachefs

func New(ctx context.Context, cfg Config) (ufs.FS, error)

type Config struct {
	StoragePath    string  `yaml:"storagePath"`    // bolt file. "" = memory-only.
	MemorySize     string  `yaml:"memorySize"`     // Hot hard limit. Default "256MiB".
	StorageSize    string  `yaml:"storageSize"`    // Warm hard limit. Default "1GiB".
	MaxFileSize    string  `yaml:"maxFileSize"`    // Largest file. Default: see below.
	SoftLimit      float64 `yaml:"softLimit"`      // Fraction of a tier's size that starts a sweep. Default 0.8.
	LowWater       float64 `yaml:"lowWater"`       // Fraction a sweep evicts down to. Default 0.7.
	Policy         string  `yaml:"policy"`         // Eviction policy. Default "fifo" (the only value).
	TTL            string  `yaml:"ttl"`            // Expiry from last write. Default "0" (off).
	SweepInterval  string  `yaml:"sweepInterval"`  // Maintenance period. Default "30s".
	FlushThreshold float64 `yaml:"flushThreshold"` // Flush when dirty bytes reach this fraction of MemorySize. Default 0.05.
}

var (
	ErrNoSpace      = errors.New("simplecachefs: out of space")
	ErrFileTooLarge = errors.New("simplecachefs: file too large")
)
```

- Every field is a YAML-serializable value. Sizes and durations are
  human-readable strings; fractions are floats from 0 to 1.
- Each string field has an exported conversion method that parses it,
  applies the default when it is empty, and reports a parse failure with the
  field name:

  ```go
  func (c Config) MemoryBytes() (int64, error)
  func (c Config) StorageBytes() (int64, error)
  func (c Config) MaxFileBytes() (int64, error)
  func (c Config) TTLDuration() (time.Duration, error)
  func (c Config) SweepIntervalDuration() (time.Duration, error)
  ```

- Sizes are parsed with `github.com/dustin/go-humanize` `ParseBytes`, so
  `256MiB` is 256 × 2²⁰ and `256MB` (or `256mb`) is 256 × 10⁶. A bare number
  is bytes. Durations are parsed with `time.ParseDuration`.
- A zero fraction takes its default.
- The default `MaxFileSize` is half of the soft-to-hard gap of the smallest
  tier: `(1 − SoftLimit) × min(MemorySize, StorageSize) / 2`. With the
  defaults that is 25.6 MiB.
- `New` validates the resolved config and returns an error for any
  nonsensical or ambiguous value:
  - A size or duration that doesn't parse, a size of `0` or more than
    `math.MaxInt64`, a negative `TTL`, or a `SweepInterval` of `0`.
  - A fraction outside `(0, 1)`, or `LowWater ≥ SoftLimit`.
    `FlushThreshold` may be `1`.
  - `MaxFileSize` not strictly smaller than `(1 − SoftLimit) × size` of every
    tier. This guarantees that a single write crossing the soft limit can't
    also cross the hard limit, so the sweep has room to work.
  - With a warm tier: `MemorySize > LowWater × StorageSize`, since a flush of
    a full hot tier must fit after a sweep.
  - Memory-only: `StorageSize` or `FlushThreshold` set, since neither has a
    meaning without a warm tier.
  - A `Policy` other than `fifo` (including `lru`, which is not implemented).
- The config also carries an unexported clock (`now func() time.Time`) so
  tests control TTL and timers.

## Architecture

```text
              ┌──────────────────── simplecachefs ────────────────────┐
              │  overlay.FS                                            │
 caller ────▶ │    layer 0 = memory:  (hot)  ──emits──▶ writelog.Log   │
              │    layer 1 = bolt:    (warm) ◀── BatchWrite ── flush   │
              │  FIFO index per tier (size, modTime)                   │
              │  maintenance goroutine ◀── timer / signal              │
              │  hard-evict goroutine  ◀── on demand                   │
              └────────────────────────────────────────────────────────┘
```

In memory-only mode there is no overlay, write log or warm tier: the cache
wraps the memory FS directly.

### Files

| File | Purpose |
|:--|:--|
| `proto/writelog/writelog.proto` | `WriteLogEntry` message (Go package `github.com/cloudfra/ufs/proto/writelogpb`) |
| `writelog.go` (base `ufs`) | `WriteLogEmitter`, `WriteLogSink` and `BatchWriter` interfaces |
| `memfs.go` | `(*memFS).SetWriteLog`: emits an entry for every mutation |
| `drivers/boltfs/batch.go` | `(*boltFS).BatchWrite`: applies entries in one bolt transaction |
| `drivers/common/writelog/writelog.go` | `writelog.Log` (dedupe, drain, restore) and `writelog.Apply` |
| `drivers/common/overlay/overlay.go` | `overlay.FS`: N-layer union with tombstones |
| `drivers/simplecachefs/simplecachefs.go` | `New`, `cacheFS`, the `ufs.FS` methods, `Sync` |
| `drivers/simplecachefs/config.go` | `Config`, conversion methods, defaults and validation |
| `drivers/simplecachefs/index.go` | Per-tier FIFO index and byte totals |
| `drivers/simplecachefs/maintain.go` | Maintenance goroutine (flush, TTL, soft sweep) and hard-evict goroutine |

## Write log

### `WriteLogEntry` (protobuf)

A new file `proto/writelog/writelog.proto`, with the same edition and options
as `proto/ufs.proto`. It gets its own Go package so the base `ufs` package can
import it without pulling in the gRPC and gateway code generated for
`proto/ufs.proto`. The Makefile's `PROTOS` list gains its generated file.

```proto
message WriteLogEntry {
  enum Op {
    OP_UNSPECIFIED = 0;
    OP_PUT = 1;        // Write the file's full content, creating parents.
    OP_MKDIR = 2;      // MkdirAll.
    OP_REMOVE_ALL = 3; // RemoveAll; no error if missing.
  }
  Op op = 1;
  string name = 2;
  uint32 mode = 3;                           // io/fs.FileMode bits.
  google.protobuf.Timestamp mod_time = 4;
  bytes content = 5;                         // OP_PUT only.
}
```

### Interfaces (base `ufs`)

```go
// WriteLogSink receives write log entries.
type WriteLogSink interface {
	Append(entry *writelogpb.WriteLogEntry)
}

// WriteLogEmitter is implemented by an FS that can report every mutation
// synchronously, in the order it is applied.
type WriteLogEmitter interface {
	// SetWriteLog sets the sink that receives an entry for every mutation.
	// A nil sink stops emission.
	SetWriteLog(sink WriteLogSink)
}

// BatchWriter is implemented by an FS that can apply entries atomically.
type BatchWriter interface {
	// BatchWrite applies entries in order, all or nothing.
	BatchWrite(entries []*writelogpb.WriteLogEntry) error
}
```

- Emission is **synchronous**, not built on `Watch`. `Watch` delivers events
  from a background goroutine, so a `Sync` could run before the last write's
  event had arrived and miss it.
- `Append` is called with the emitter's lock held. It must be fast and must
  not call back into the FS.

### memfs as a `WriteLogEmitter`

- memfs calls the sink where it already calls `notify`:
  - `OP_PUT` on `Create` and on every `Write`/`WriteString`.
  - `OP_MKDIR` for each directory `MkdirAll` creates.
  - `OP_REMOVE_ALL` on `Remove` and `RemoveAll`.
- `content` **aliases** the memfs node's buffer; nothing is copied. This is
  safe because memfs never mutates a node's buffer once published: each write
  replaces `node.content` with a new clone (`syncToFSLocked`). The
  implementation documents this invariant on `memNode.content` and tests it.
- The change to memfs is one `sink` field, one setter and a few `Append`
  calls next to the existing `notify` calls.

### boltfs as a `BatchWriter`

- `BatchWrite` runs every entry in a single `db.Update` transaction, in
  order. Any failure rolls back the whole batch.
- It reuses the existing helpers (`parentBucket`, `createDirBucket`,
  `removeAllChildren`, `encodeBoltRecord`), so it adds a loop over entries,
  not new storage logic.
- `mod_time` is preserved, so FIFO order and TTL survive a flush and a reopen.
- Notifications for each entry fire after the commit.
- The wasm stub returns `errors.ErrUnsupported`.

### `writelog.Log` (dedupe)

`drivers/common/writelog.Log` implements `ufs.WriteLogSink` and holds pending
entries.

- It dedupes as entries arrive, keeping at most one pending entry per path:
  - `OP_PUT` replaces any earlier entry for the same path. Only the last
    content of a file that is written many times is kept, and the superseded
    buffers can be garbage collected.
  - `OP_REMOVE_ALL` drops every pending entry at or below the path, then
    records itself.
  - `OP_MKDIR` is dropped when a pending entry for the same path already
    exists.
- After dedupe, no pending entry is covered by a later remove. Applying all
  removes first, then mkdirs (sorted, parents first), then puts is therefore
  equivalent to applying the entries in arrival order.
- `Drain()` swaps out the pending set and returns it in that order. Writes
  during a flush go to the fresh set.
- `Restore(drained)` puts a failed batch back. Each entry is re-added only if
  nothing newer is pending for its path.
- `Has(name)` and `Bytes()` report whether a path is dirty and the total
  pending content size.

`writelog.Apply(fsys ufs.FS, entries)` applies entries to any FS. It uses
`BatchWrite` when `fsys` is a `ufs.BatchWriter`, and otherwise falls back to
`MkdirAll`, `Create`+`Write`+`Close` and `RemoveAll` one entry at a time
(not atomic).

## Overlay (`drivers/common/overlay`)

A generic union of N `ufs.FS` layers. It has no knowledge of caching.

```go
package overlay // github.com/cloudfra/ufs/drivers/common/overlay

type FS struct { /* layers []ufs.FS; tombstones */ }

// New returns an overlay of layers, top first. layers[0] receives all writes.
func New(layers ...ufs.FS) (*FS, error)

// Tombstones returns a snapshot of the tombstones and their sequence numbers.
func (o *FS) Tombstones() map[string]uint64
// ClearTombstones removes each tombstone whose sequence number still matches the snapshot.
func (o *FS) ClearTombstones(snapshot map[string]uint64)

func (o *FS) Layer(i int) ufs.FS
```

`*FS` implements `ufs.FS`. `New` fails when given no layers.

### Why N layers

Compared with a fixed upper/lower pair, only the loops change. Reads walk the
layers top-down, `ReadDir` merges N listings instead of two, and `Remove`'s
emptiness check reads the merged listing either way. Tombstones don't change:
a tombstone always hides the path in every layer below the top. It costs a
few lines, so the overlay is N-layer even though simplecachefs uses two.

### Tombstones

- A tombstone on a path hides that path **and everything below it** in
  layers 1..N−1. It never hides anything in layer 0.
- Tombstones live in memory (a map from path to sequence number, guarded by a
  mutex). They are not persisted. The owner applies each removal to every
  lower layer and then calls `ClearTombstones`.
- A path is *hidden* when it or any ancestor has a tombstone. The check is
  O(depth).
- Recording a tombstone on a path that already has one assigns a new sequence
  number, so `ClearTombstones` with an older snapshot leaves it in place.

### Operations

- **`Open`, `ReadFile`, `Stat`, `Lstat`, `ReadLink`:** layer 0 first. On
  `fs.ErrNotExist`, try each lower layer in turn unless the path is hidden;
  return `fs.ErrNotExist` when no layer has it. Any other error is returned
  without consulting lower layers.
- **`ReadDir`:** the merge of every layer's entries, sorted by name. The
  highest layer wins on duplicate names, and hidden entries from lower layers
  are omitted. The directory exists if any layer that is allowed to show it
  has it. A file in one layer and a directory of the same name in a lower
  layer resolves to the higher one.
- **`Open` on a directory:** returns a directory file whose `ReadDir` is the
  merged listing, snapshotted at open.
- **`Glob`:** `fs.Glob` over the merged `ReadDir` (`internal/globutil`).
- **`Create`, `MkdirAll`:** go to layer 0. Missing parents are created in
  layer 0 with `MkdirAll`. Creating a path does not clear tombstones, since
  layer 0 is always visible.
- **`Remove`:** fails with `fs.ErrNotExist` if the path is visible in no
  layer, and with `ErrDirNotEmpty` if it is a directory whose merged listing
  is non-empty. Otherwise it removes the path from layer 0 (ignoring
  `fs.ErrNotExist`) and records a tombstone.
- **`RemoveAll`:** `RemoveAll` on layer 0 and records a tombstone. Succeeds
  when the path does not exist, like `os.RemoveAll`.
- A tombstone is recorded on every remove, even when no lower layer holds the
  path. This keeps removal race-free while lower layers are written to
  concurrently (see [Flush](#flush)).
- **`Close`:** closes the layers top-down and joins their errors.
- **`Watch`:** returns `errors.ErrUnsupported` in v1.
- **`GetDeviceInfo`, `URI`:** those of layer 0. `String()` is
  `overlay(<layer0>, <layer1>, …)`.

## Index (`index.go`)

### Why FIFO

LRU needs a write to the index on every read to bump the entry, which puts a
lock on the read path. FIFO by modification time only changes the index on
writes and removes, and deleting strictly by modification time is also what
TTL needs.

The per-entry metadata is the same either way: path, size and modification
time. That is roughly 100 bytes per file (about 10 MB for 100k files). It is
small enough that the metadata does not by itself argue for LRU. `Policy`
exists so LRU can be added later.

### Structure

- One index per tier: a map from path to `{size, modTime, *list.Element}`
  and a `container/list` ordered by `modTime`, oldest first.
- A write moves the entry to the back, since it becomes the newest. A remove
  unlinks it. Eviction and TTL pop from the front. Every operation is O(1).
- Running totals: `hotBytes` and `warmBytes`. Dirty bytes come from
  `writelog.Log.Bytes()`.
- One mutex guards both indexes. Tier I/O happens outside it.
- The read path does not touch the index.
- On `New`, the warm tier is walked once (`fs.WalkDir`) and its entries are
  sorted by `modTime` to rebuild the warm index and `warmBytes`. The hot tier
  starts empty.

## Data flow

### Writes

- `Create` returns a wrapper around the hot tier's file. Before each
  `Write`/`WriteString` it checks:
  1. If the file would exceed `MaxFileSize`: fail with `ErrFileTooLarge`.
  2. If the hard-evict goroutine is running: fail with `ErrNoSpace`.
  3. If `hotBytes` plus the growth would exceed `MemorySize`: fail with
     `ErrNoSpace` and start the hard-evict goroutine.
  4. Otherwise reserve the growth and write to the memory FS, which emits an
     `OP_PUT` into the write log.
- Errors are wrapped in `fs.PathError`.
- After a write, if `hotBytes ≥ SoftLimit × MemorySize` or dirty bytes
  `≥ FlushThreshold × MemorySize`, the maintenance goroutine is signalled.
- `Create` and `MkdirAll` also fail with `ErrNoSpace` while the hard-evict
  goroutine runs.

### Reads

- `Open`, `ReadFile`, `Stat` and `ReadDir` go through the overlay: hot first,
  then warm.
- Warm hits are served from bolt without being copied into hot. Under FIFO a
  promoted copy would carry its original, old `modTime` and be the first
  thing evicted, so promotion would only add memory churn.
- TTL is enforced by the sweep, not on read. An expired entry can remain
  readable until the next sweep, at most `SweepInterval` late.

### Removes

- `Remove`/`RemoveAll` go through the overlay, which deletes from hot right
  away and records a tombstone.
- memfs emits `OP_REMOVE_ALL`, which drops any pending writes for the path
  from the write log.
- The hot index drops the entries at once. Warm entries stay counted in
  `warmBytes` until a flush applies the tombstone.

### Flush

A flush writes the write log and tombstones to the warm tier and makes room
there in the same transaction:

1. Snapshot the overlay's tombstones and `Drain()` the write log.
2. Compute the warm tier's size after the batch. If it would exceed
   `SoftLimit × StorageSize`, pick the oldest warm entries (from the front of
   the warm index) to evict until it would be at or below
   `LowWater × StorageSize`. Validation guarantees a full hot tier fits after
   this.
3. Build one batch: `OP_REMOVE_ALL` for each tombstone and each evicted
   entry, then the drained entries (their own removes, then mkdirs, then
   puts).
4. `writelog.Apply` the batch to the warm tier. Bolt applies it as one
   transaction.
5. On success:
   - Update the warm index and `warmBytes`.
   - Call `ClearTombstones(snapshot)`.
   - Hot entries that were in the batch are now clean. A path written again
     during the flush is back in the fresh write log, so it stays dirty.
   - The sequence-number check means a remove that raced the flush keeps its
     tombstone, and the next flush applies it.
6. On failure, `Restore` the drained entries, keep the tombstones, and log at
   `slog.Warn`. The next wake-up retries.

A hot entry is **clean** when it is neither pending in the write log nor in a
batch being flushed. Only clean hot entries can be evicted.

### Sweep

A sweep runs a flush, then:

1. **TTL:** if `TTL > 0`, pop entries whose `now − modTime ≥ TTL` from the
   front of each tier's index. Expired warm entries are removed in one batch.
   Expired hot entries are removed from the memory FS. A dirty hot entry that
   has expired is dropped and its write-log entry discarded.
2. **Hot soft limit:** if `hotBytes ≥ SoftLimit × MemorySize`, drop clean
   hot entries oldest-first until `hotBytes ≤ LowWater × MemorySize`. Warm
   still holds them.

In memory-only mode there is nothing to flush: step 2 drops the oldest hot
entries regardless, since none are dirty.

### Maintenance goroutine

One goroutine runs a sweep each time it wakes. It wakes every
`SweepInterval`, or when signalled on a buffered channel of size 1, so
signalling never blocks.

### Hard-evict goroutine

- Started on demand when a write would exceed `MemorySize`. An atomic flag
  ensures at most one runs.
- While the flag is set, writes, `Create` and `MkdirAll` fail with
  `ErrNoSpace`.
- It runs a sweep, but drops clean hot entries down to
  `LowWater × MemorySize` even when the soft limit wasn't reached. Then it
  clears the flag.
- If the flush inside it fails, the flag is still cleared and the error is
  logged. The next write that doesn't fit starts it again.

### `Sync`

`Sync()` runs a full sweep (flush, TTL and soft limits) and blocks until it
completes, returning the flush error if any. It is serialized with the
maintenance and hard-evict goroutines. The FS returned by `New` has a
`Sync() error` method, and the package exports `Sync(ufs.FS) error` for
callers that hold the interface.

### Space reclamation

bbolt never shrinks its file, but it reuses freed pages for new writes. The
limits count live content bytes, so the `.db` file stays close to its
high-water mark and does not grow without bound. There is no compaction.

### Close

`Close` stops the maintenance goroutine, waits for any hard-evict goroutine,
runs a final `Sync`, then closes the overlay (hot, then warm). It joins their
errors. Methods called after `Close` return `fs.ErrClosed`.

## Error handling

- `ErrNoSpace` and `ErrFileTooLarge` are wrapped in `fs.PathError`. Test for
  them with `errors.Is`.
- Background flush and sweep failures are logged with `slog` and retried on
  the next wake-up. They never fail a read.
- A warm read that fails with anything other than `fs.ErrNotExist` returns
  that error.
- Invalid paths fail through `pathutil.Validate` with `fs.PathError`, as in
  every other driver.

## Testing

- **Conformance** (`drivers/testing` `WriteFS`):
  - `overlay.FS` over two and three `memory:` layers.
  - simplecachefs memory-only, and with the bolt file in `t.TempDir()`.
- **memfs:**
  - The emitted sequence for `Create`, `Write`, `MkdirAll`, `Remove` and
    `RemoveAll`.
  - An emitted buffer stays unchanged after later writes (the aliasing
    invariant).
  - `SetWriteLog(nil)` stops emission.
- **boltfs `BatchWrite`:** entries applied in order, rollback on a failing
  entry, `mod_time` preserved, notifications only after commit.
- **writelog:**
  - Dedupe of put/put, put/remove, remove/put, mkdir/put, and remove of a
    parent.
  - `Drain` order.
  - `Restore` not overriding newer entries.
  - `Apply` with and without a `BatchWriter`.
- **overlay:**
  - Shadowing across three layers; `ReadDir` merge, ordering and
    duplicates; file-vs-directory conflicts.
  - `Remove` of files in various layers; a non-empty merged directory;
    `RemoveAll` hiding a subtree; creating a file under a tombstoned
    directory.
  - `ClearTombstones` leaves a tombstone that was re-recorded after the
    snapshot.
- **simplecachefs** (injected clock; tests call `Sync` or signal the
  maintenance goroutine rather than sleeping):
  - Every `Config` conversion method: defaults, humanize units, parse errors
    naming the field. Every validation rule, and both modes.
  - `ErrFileTooLarge`.
  - `ErrNoSpace` when hot is full of dirty data, the hard-evict window, and
    writes succeeding after it.
  - Flush at `FlushThreshold` and on `SweepInterval`. `Sync` persists
    everything and sweeps. `Close` persists everything.
  - A write that races a flush stays dirty. A remove that races a flush is
    not resurrected.
  - The flush evicts the oldest warm entries when the soft limit would be
    crossed.
  - TTL expiry in both tiers, including a dirty expired entry.
  - Memory-only FIFO eviction.
  - Reopening an existing bolt file rebuilds `warmBytes` and FIFO order.
  - Methods after `Close` return `fs.ErrClosed`.
- All tests run under `make test` (race detector on) and `make lint`.

## Out of scope

- A cold tier or origin.
- URI and `MountSpecOptions` configuration.
- LRU or other eviction policies.
- Promotion of warm hits into the hot tier.
- `Watch` on the overlay or the cache.
- Compaction of the bolt file.
- Crash durability for data not yet flushed.
