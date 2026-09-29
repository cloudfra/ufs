# SimpleCacheFS — Design Spec

## Goal

Add `drivers/simplecachefs`, a lossy two-tier cache file system:

- **hot** tier: an in-memory `memory:` FS.
- **warm** tier: a `bolt:` FS on local disk. Optional: with no storage path
  the cache is memory-only.

Writes land in the hot tier. A write log records them and flushes them to the
warm tier in batches, on a timer or once enough dirty data accumulates. Warm
hits are promoted back into the hot tier with `ufs.Chtimes`. Entries are
evicted oldest-first (FIFO by modification time), by TTL and by capacity. When the cache has no room, writes fail with
`ErrNoSpace` rather than blocking.

The cache is **not durable**. `Sync` and `Close` persist the hot tier to the
warm tier, but a crash loses whatever was written since the last flush, and
eviction may drop any entry at any time.

simplecachefs is a standalone FS, not a cache in front of an origin. It is a
temporary, purpose-built component and is configured programmatically only;
it has no URI scheme.

Supporting changes. Each package's code lives in a file inside its own
directory; nothing is added directly to `drivers/common/`.

- `drivers/common/overlay/overlay.go` (package `overlay`): a generic N-layer
  overlay `ufs.FS` with tombstones.
- `drivers/common/writelog/` (package `writelog`): the `writelog.FS` wrapper
  that records writes to any FS, two write logs (in-memory and on-disk), and
  the `BatchWriter` interface.
- `proto/writelog.proto`: a `WriteLogEntry` message in the existing
  `cloudfra.ufs` proto package.
- `drivers/boltfs/batch.go`: boltfs implements `writelog.BatchWriter`.
- `op.go`: a `ufs.Chtimes` function, named after `os.Chtimes`, that sets a
  file's modification time to now by rewriting it through the FS's normal
  write path. It is a plain function, not an FS interface. No driver, memfs
  included, is changed to support it (see [Promotion](#promotion)).

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
	SyncLog        bool    `yaml:"syncLog"`        // Record writes synchronously. Default false (async).
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
  - Memory-only: `StorageSize`, `FlushThreshold` or `SyncLog` set, since
    none has a meaning without a warm tier.
  - A `Policy` other than `fifo` (including `lru`, which is not implemented).
- The config also carries an unexported clock (`now func() time.Time`) so
  tests control TTL and timers.

## Architecture

```text
              ┌────────────────────────── simplecachefs ──────────────────────────┐
              │  overlay.FS                                                        │
 caller ────▶ │    layer 0 = writelog.FS(memory:) ── records ──▶ writelog.MemoryLog│
              │    layer 1 = bolt:  ◀── BatchWrite (background) ── flush           │
              │  FIFO index per tier                                               │
              │  maintenance goroutine ◀── timer / signal                          │
              │  hard-evict goroutine  ◀── on demand                               │
              └────────────────────────────────────────────────────────────────────┘
```

In memory-only mode there is no overlay, write log or warm tier: the cache
wraps the memory FS directly.

### Files

| File | Purpose |
|:--|:--|
| `proto/writelog.proto` | `WriteLogEntry` message (package `cloudfra.ufs`, Go package `github.com/cloudfra/ufs/proto`) |
| `drivers/common/writelog/writelog.go` | `Log` and `BatchWriter` interfaces, `Apply` |
| `drivers/common/writelog/fs.go` | `writelog.FS`: wraps a `ufs.FS` and records successful writes |
| `drivers/common/writelog/memorylog.go` | `MemoryLog`: deduped entries that reference payloads in the source FS |
| `drivers/common/writelog/filelog.go` | `FileLog`: appends entries with payloads to segment files on disk |
| `drivers/common/overlay/overlay.go` | `overlay.FS`: N-layer union with tombstones |
| `drivers/boltfs/batch.go` | `(*boltFS).BatchWrite`: applies entries in one bolt transaction |
| `op.go` | `ufs.Chtimes` |
| `drivers/simplecachefs/simplecachefs.go` | `New`, `cacheFS`, the `ufs.FS` methods, `Sync` |
| `drivers/simplecachefs/config.go` | `Config`, conversion methods, defaults and validation |
| `drivers/simplecachefs/index.go` | Per-tier FIFO index and byte totals |
| `drivers/simplecachefs/maintain.go` | Maintenance goroutine (flush, TTL, soft sweep), hard-evict goroutine, promotion |

## Write log (`drivers/common/writelog`)

### `WriteLogEntry` (protobuf)

A new file `proto/writelog.proto` in the flat `proto/` directory, with the
same proto package (`cloudfra.ufs`), `go_package`, edition and options as
`proto/ufs.proto`. The proto build rules gain its generated
`proto/writelog.pb.go`.

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
  int64 size = 5;                            // OP_PUT: content length.
  bytes content = 6;                         // OP_PUT: set only when the payload is attached.
}
```

### Interfaces

```go
package writelog // github.com/cloudfra/ufs/drivers/common/writelog

// Log records write log entries.
type Log interface {
	// Append records entry. entry.Content is empty; a log that needs the
	// payload reads it from the source FS it was created with.
	Append(entry *pb.WriteLogEntry) error
	// Snapshot returns the pending entries with payloads attached, ordered
	// for replay: removes, then mkdirs (parents first), then puts.
	Snapshot() (*Snapshot, error)
	// Commit removes the snapshot's entries that nothing newer has replaced.
	Commit(s *Snapshot) error
	// Pending reports whether name has an uncommitted entry.
	Pending(name string) bool
	// Bytes reports the total size of pending OP_PUT entries.
	Bytes() int64
}

// BatchWriter is implemented by an FS that can apply entries atomically.
type BatchWriter interface {
	// BatchWrite applies entries in order, all or nothing.
	BatchWrite(entries []*pb.WriteLogEntry) error
}

// Apply writes entries to fsys: with BatchWrite when fsys is a BatchWriter,
// otherwise one entry at a time (MkdirAll, Create+Write+Close, RemoveAll),
// which is not atomic.
func Apply(fsys ufs.FS, entries []*pb.WriteLogEntry) error
```

In every replay path (`Apply`, `BatchWrite`, and snapshots), a missing file is
not an error: a remove of a path that does not exist succeeds, and a put whose
payload can no longer be read from the source because the file was removed
is skipped (its remove is recorded after it).

### `writelog.FS` (recording wrapper)

`writelog.FS` wraps any `ufs.FS` and records the writes made through it. The
wrapped FS is unmodified, so this works for memfs and every other driver.

```go
type Mode int

const (
	Async Mode = iota // Record on a background goroutine.
	Sync              // Record in the calling goroutine before returning.
)

func NewFS(inner ufs.FS, log Log, mode Mode) *FS

// Barrier blocks until every write that completed before the call has been recorded.
func (f *FS) Barrier()
```

- **What it records.** Only operations that succeed on the inner FS:
  - `OP_PUT` when a file from `Create` is closed. The entry carries name,
    mode, modTime and size (from `Stat` after close), not the content.
  - `OP_MKDIR` on `MkdirAll`.
  - `OP_REMOVE_ALL` on `Remove` and `RemoveAll`. A remove that fails with
    `fs.ErrNotExist` still counts as a success and is recorded, since the
    path may exist in a lower layer that this FS doesn't see.
  - A write or close that returns any other error is not recorded.
- **Async** (the default): recording happens on a background goroutine fed
  by a bounded channel. A full channel blocks the writer rather than
  dropping a record. Records can trail the writes, but `Barrier` makes a
  flush see every write that completed before it, so nothing is missed.
- **Sync**: recording happens before the write call returns, so `Barrier`
  is a no-op.
- Reads, `Stat`, `ReadDir` and the rest pass straight through.
- `Watch` passes through when the inner FS implements `ufs.Watcher`.
  Watch-driven recording isn't used: events can be coalesced or dropped, and
  a remove of a path the inner FS never held produces no event.

### `MemoryLog` (in-memory, deduped)

`NewMemoryLog(src ufs.ReadFS) *MemoryLog`

- It holds entries only, never payloads. An entry **references** its
  payload in the source FS (the memfs that `writelog.FS` wraps), so pending
  data isn't held twice. `Snapshot` reads each payload from `src` with
  `ReadFile`. That copy is short-lived and bounded by the batch being
  flushed.
- It dedupes as entries arrive, keeping at most one pending entry per path:
  - `OP_PUT` replaces any earlier entry for the same path.
  - `OP_REMOVE_ALL` drops every pending entry at or below the path, then
    records itself.
  - `OP_MKDIR` is dropped when a pending entry for the same path already
    exists.
- After dedupe, no pending entry is covered by a later remove. Replaying
  removes, then mkdirs, then puts is therefore equivalent to replaying the
  entries in arrival order.
- Each pending entry carries a sequence number. `Commit` removes an entry
  only if its sequence number still matches the snapshot. An entry written
  again after the snapshot stays pending.
- A payload read in `Snapshot` sees the newest content, which may be newer
  than the entry. That's harmless: the newer write has its own pending entry
  and is committed or rewritten on the next flush.

### `FileLog` (on disk, segmented)

`NewFileLog(dir string, src ufs.ReadFS, segmentSize int64) (*FileLog, error)`

- Appends each entry **with its payload** (read from `src` at append time) to
  the current segment file in `dir`. Entries are length-delimited
  `WriteLogEntry` protos (`protodelim`).
- Segment files are named `NNNNNNNNNN.wlog` in increasing order. The current
  segment is rotated once it reaches `segmentSize` (default 64 MiB).
- `Snapshot` rotates the current segment, reads every closed segment, and
  dedupes the entries in memory with the same rules as `MemoryLog`.
  `Commit` deletes the segments the snapshot covered.
- On open, existing segments are kept. A torn final record, from a crash
  mid-append, is truncated.
- `FileLog` is a separate struct from `MemoryLog`; both implement
  `writelog.Log`. simplecachefs does not use `FileLog`, because the cache is
  not durable. It is built and tested alongside `MemoryLog`.

## boltfs as a `writelog.BatchWriter`

- `BatchWrite` runs every entry in a single `db.Update` transaction, in
  order. Any failure rolls back the whole batch, and the caller leaves the
  entries in its write log.
- It reuses the existing helpers (`parentBucket`, `createDirBucket`,
  `removeAllChildren`, `encodeBoltRecord`), so it adds a loop over entries,
  not new storage logic.
- `mod_time` is preserved, so FIFO order and TTL survive a flush and a reopen.
- Notifications for each entry fire after the commit.
- The wasm stub returns `errors.ErrUnsupported`.

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
// Hidden reports whether name, or an ancestor of it, has a tombstone.
func (o *FS) Hidden(name string) bool

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
  O(depth) map lookups, and is skipped when the map is empty.
- Recording a tombstone on a path that already has one assigns a new sequence
  number, so `ClearTombstones` with an older snapshot leaves it in place.

### Operations

Every operation that consults a lower layer checks tombstones first, so a
removed path never reaches a lower layer.

- **`Open`, `ReadFile`, `Stat`, `Lstat`, `ReadLink`:**
  1. Try layer 0 and return its result unless it is `fs.ErrNotExist`. Any
     other error is returned as is.
  2. If `name` is hidden, return `fs.ErrNotExist` without touching the lower
     layers.
  3. Try each lower layer in turn. Return `fs.ErrNotExist` when none has it.
- **`ReadDir`:**
  - If `name` is hidden and layer 0 doesn't have it, return `fs.ErrNotExist`.
  - Otherwise, merge layer 0's entries with the lower layers' entries.
    Skip lower layers entirely when `name` is hidden. Skip any lower-layer
    entry whose child path is hidden: a tombstone on `d/x` removes `x` from
    the listing of `d`.
  - Sort the merged entries by name. The highest layer wins on duplicate
    names.
  - A file in one layer and a directory of the same name in a lower layer
    resolves to the higher one.
- **`Open` on a directory:** returns a directory file whose `ReadDir` is the
  merged listing, snapshotted at open.
- **`Glob`:** `fs.Glob` over the merged `ReadDir` (`internal/globutil`), so
  hidden paths never match.
- **`Create`, `MkdirAll`:** go to layer 0. Missing parents are created in
  layer 0 with `MkdirAll`. Creating a path does not clear tombstones: layer 0
  is always visible, and the tombstone must keep hiding the lower layers'
  stale content under that path, such as other files in a directory that
  was removed with `RemoveAll`.
- **`Remove`:** fails with `fs.ErrNotExist` if the path is visible in no
  layer, and with `ErrDirNotEmpty` if it is a directory whose merged
  (tombstone-filtered) listing is non-empty. Otherwise it removes the path
  from layer 0 (ignoring `fs.ErrNotExist`) and records a tombstone.
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
lock on the read path. FIFO only changes the index on writes (including
promotions, which go through `Chtimes`) and removes.

The per-entry metadata is the same either way: path, size and a timestamp.
That is roughly 100 bytes per file (about 10 MB for 100k files). It is small
enough that the metadata does not by itself argue for LRU. `Policy` exists so
LRU can be added later.

### Structure

- One index per tier: a map from path to `{size, modTime, *list.Element}`
  plus a `container/list`, oldest first.
  - Both tiers are ordered by modification time. Eviction and TTL delete
    strictly by modification time, popping from the front.
  - Every entry enters hot through a write or `Chtimes`, both of which set its
    modification time to now, so appending to the back keeps the order.
- A write moves the entry to the back. A remove unlinks it. Eviction and TTL
  pop from the front. Every operation is O(1).
- Running totals: `hotBytes` and `warmBytes`. Dirty bytes come from
  `MemoryLog.Bytes()`.
- One mutex guards both indexes. Tier I/O happens outside it.
- The read path touches the index only when it promotes.
- On `New`, the warm tier is walked once (`fs.WalkDir`) and its entries are
  sorted by modTime to rebuild the warm index and `warmBytes`. The hot tier
  starts empty.

## Data flow

### Writes

- `Create` returns a wrapper around the file from layer 0. Before each
  `Write`/`WriteString` it checks:
  1. If the file would exceed `MaxFileSize`: fail with `ErrFileTooLarge`.
  2. If the hard-evict goroutine is running: fail with `ErrNoSpace`.
  3. If `hotBytes` plus the growth would exceed `MemorySize`: fail with
     `ErrNoSpace` and start the hard-evict goroutine.
  4. Otherwise reserve the growth and write through `writelog.FS` to memfs.
     On `Close`, `writelog.FS` records an `OP_PUT`.
- Errors are wrapped in `fs.PathError`. A failed write is not recorded.
- After a write, if `hotBytes ≥ SoftLimit × MemorySize` or dirty bytes
  `≥ FlushThreshold × MemorySize`, the maintenance goroutine is signalled.
- `Create` and `MkdirAll` also fail with `ErrNoSpace` while the hard-evict
  goroutine runs.
- Writes and removes hold the cache's promotion lock in read mode (see
  [Promotion](#promotion)).

### Reads

- `Open`, `ReadFile`, `Stat` and `ReadDir` go through the overlay: hot first,
  then warm.
- A warm hit on a file read with `Open` or `ReadFile` is promoted into hot
  (below). `Stat` and `ReadDir` never promote. The read returns the content
  and `ModTime` as they were before the promotion.
- TTL is enforced by the sweep, not on read. An expired entry can remain
  readable until the next sweep, at most `SweepInterval` late.

### Chtimes (`op.go`)

```go
// Chtimes sets the modification time of the file name to now by rewriting
// its content through fsys (ReadFile, then Create, Write and Close). It is
// named after os.Chtimes but always uses the current time. A missing file
// returns fs.ErrNotExist, and a directory returns fs.ErrInvalid.
func Chtimes(fsys FS, name string) error
```

- `Chtimes` uses only the `ufs.FS` write path, so it works on every writable
  driver and needs no new driver capability. The FS's own `Create` sets the
  new modification time.
- It never sets an arbitrary modification time: there is no time argument,
  and no driver or FS interface gains a `Chtimes` method. A modification
  time only ever moves to *now*.
- Cost: one read and one full rewrite of the file. That is fine for the
  cache's bounded file sizes, and the reason it is not a general-purpose
  `Chtimes` for large files.

### Promotion

A file served from warm is passed through `Chtimes` on the cache, so repeated reads
come from memory:

- It is promoted only when `hotBytes + size ≤ SoftLimit × MemorySize`.
  Promotion never evicts anything and never triggers the hard-evict
  goroutine. When there is no room, the file is served from warm and left
  alone.
- Promotion does what `ufs.Chtimes` does on the overlay, reusing the content it
  just read instead of reading it twice. It rewrites the file through the
  overlay's layer 0, the `writelog.FS` over memfs. The hot copy therefore
  gets a modification time of now, and the write log records an `OP_PUT`.
- The next flush writes the file back to warm with the new modification
  time, so both tiers agree. A later `Stat` reports the same `ModTime`
  whichever tier serves it, and the time never moves backwards when the hot
  copy is dropped. Warm bytes are unchanged, since the record is replaced
  in place.
- Because a promotion is a write:
  - It moves the file to the back of both FIFOs, so frequently read files
    survive eviction. That gives LRU-like behavior without touching the
    index on hot hits.
  - It restarts the file's TTL. TTL counts from the last write or
    promotion.
  - It costs one warm rewrite per promoted file per flush, deduped with
    other writes to the same file.
- Promotion is skipped while the hard-evict goroutine runs.
- **Race safety:** promotion holds the promotion lock in write mode while it
  re-checks that the path is still not in hot (`Stat` on the memory FS) and
  not hidden (`overlay.Hidden`), and then rewrites it. Writes and removes
  hold the lock in read mode. A promotion therefore can't overwrite a newer
  write or bring back a removed file. The promotion reads from warm before
  taking the lock, so the lock is held only for the in-memory rewrite.
- A promotion failure is logged at `slog.Debug` and the read still succeeds.
  It never fails a read.

### Removes

- `Remove`/`RemoveAll` go through the overlay, which deletes from hot right
  away and records a tombstone. `writelog.FS` records `OP_REMOVE_ALL`, even
  when hot didn't hold the path, and that drops any pending writes for the
  path.
- The hot index drops the entries at once. Warm entries stay counted in
  `warmBytes` until a flush applies the removal.

### Flush

A flush writes the write log and tombstones to the warm tier and makes room
there in the same transaction. It runs on the maintenance goroutine (or the
hard-evict goroutine, or `Sync`), so writers never wait on bolt.

1. `writelog.FS.Barrier()`, so every completed write is in the log.
2. Snapshot the overlay's tombstones, then `MemoryLog.Snapshot()`, which
   attaches payloads by reading them from hot.
3. TTL: drop from the batch any `OP_PUT` whose modTime has expired, and
   remove that file from hot.
4. Compute the warm tier's size after the batch. If it would exceed
   `SoftLimit × StorageSize`, pick the oldest warm entries (from the front of
   the warm index) to evict until it would be at or below
   `LowWater × StorageSize`. Validation guarantees a full hot tier fits after
   this.
5. Build one batch: `OP_REMOVE_ALL` for each tombstone and each evicted
   entry, then the snapshot's entries (removes, mkdirs, puts). Duplicate
   removes are harmless.
6. `BatchWrite` it. Bolt commits it as one transaction.
7. On success:
   - `MemoryLog.Commit(snapshot)`, `ClearTombstones(tombstone snapshot)`,
     and update the warm index and `warmBytes`.
   - Hot copies of files evicted from warm in step 4 are dropped only if
     they are clean.
   - A path written or removed again during the flush has a newer sequence
     number, so it stays pending or tombstoned for the next flush.
8. On failure, nothing is committed or cleared: the entries stay in the write
   log and the tombstones stay in place. The failure is logged at
   `slog.Warn`, and the next wake-up retries.

A hot entry is **clean** when `MemoryLog.Pending` is false for it. Only clean
hot entries can be dropped by a sweep.

### Sweep

A sweep runs a flush, then:

1. **Warm TTL:** if `TTL > 0`, pop warm entries whose `now − modTime ≥ TTL`
   from the front of the warm index and remove them in one batch. Clean hot
   copies of them are dropped too.
2. **Hot soft limit:** if `hotBytes ≥ SoftLimit × MemorySize`, drop clean
   hot entries oldest-first until `hotBytes ≤ LowWater × MemorySize`. Warm
   still holds them.

In memory-only mode there is nothing to flush. TTL pops hot entries directly,
and step 2 drops the oldest hot entries regardless, since none are dirty.

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
runs a final `Sync`, stops the `writelog.FS` recorder, then closes the
overlay (hot, then warm). It joins their errors. Methods called after `Close`
return `fs.ErrClosed`.

## Error handling

- `ErrNoSpace` and `ErrFileTooLarge` are wrapped in `fs.PathError`. Test for
  them with `errors.Is`.
- Background flush and sweep failures are logged with `slog` and retried on
  the next wake-up. They never fail a read.
- In the write log and flush path, `fs.ErrNotExist` on a remove is success,
  and a put whose payload is gone is skipped (see [Interfaces](#interfaces)).
  The public `Remove` still reports `fs.ErrNotExist` for a path that is
  visible in no layer, as the `fs.FS` contract requires.
- A warm read that fails with anything other than `fs.ErrNotExist` returns
  that error.
- Invalid paths fail through `pathutil.Validate` with `fs.PathError`, as in
  every other driver.

## Testing

- **Conformance** (`drivers/testing` `WriteFS`):
  - `overlay.FS` over two and three `memory:` layers.
  - `writelog.FS` over `memory:` in both modes.
  - simplecachefs memory-only, and with the bolt file in `t.TempDir()`.
  - The cache may drop data by design, so the cache conformance runs use
    sizes far above what the suite writes and `TTL` off. In that
    configuration nothing is evicted, and the cache must pass unchanged. A
    helper asserts after each run that no eviction happened, so a
    misconfigured test fails loudly rather than flaking.
- **`ufs.Chtimes`** (on `memory:` and `file:`):
  - An existing file keeps its content and gets a later `ModTime`.
  - A missing file returns `fs.ErrNotExist`.
  - A directory returns `fs.ErrInvalid`.
  - A read-only FS returns `fs.ErrPermission`.
- **writelog:**
  - `writelog.FS` records only successful operations, records a remove that
    returned `fs.ErrNotExist`, and doesn't record a failed `Close`.
  - `Barrier` in async mode; a full channel blocks rather than drops.
  - `MemoryLog`: dedupe of put/put, put/remove, remove/put, mkdir/put, and
    remove of a parent. Snapshot order. `Commit` keeps entries rewritten
    after the snapshot. A put whose source file is gone is skipped.
  - `FileLog`: append, rotation at `segmentSize`, snapshot dedupe, `Commit`
    deleting segments, reopen with existing segments, and truncation of a
    torn final record.
  - `Apply` with and without a `BatchWriter`; a remove of a missing path
    succeeds.
- **boltfs `BatchWrite`:** entries applied in order, rollback on a failing
  entry, `mod_time` preserved, notifications only after commit.
- **overlay:**
  - Shadowing across three layers; `ReadDir` merge, ordering and
    duplicates; file-vs-directory conflicts.
  - Tombstones in every operation: a hidden file, a file under a hidden
    directory, `ReadDir` of a directory with a hidden child and of a hidden
    directory recreated in layer 0, `Glob` skipping hidden paths.
  - `Remove` of files in various layers; a non-empty merged directory;
    `RemoveAll` hiding a subtree; creating a file under a tombstoned
    directory without exposing its old siblings.
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
    everything and sweeps. `Close` persists everything. A failing
    `BatchWrite` leaves the entries pending, and they are flushed on retry.
  - A write that races a flush stays pending. A remove that races a flush is
    not resurrected.
  - Promotion:
    - A warm hit is promoted. The read returns the pre-promotion
      `ModTime`, and afterwards `Stat` reports the `Chtimes` time in both tiers
      once flushed.
    - No promotion above the soft limit or during hard eviction.
    - A promotion racing a write or a remove doesn't overwrite or resurrect.
    - A promoted file moves to the back of both FIFOs and its TTL restarts.
  - The flush evicts the oldest warm entries when the soft limit would be
    crossed.
  - TTL expiry in warm (cascading to clean hot copies), a dirty expired
    entry dropped at flush, and memory-only TTL.
  - Memory-only FIFO eviction.
  - Reopening an existing bolt file rebuilds `warmBytes` and FIFO order.
  - Methods after `Close` return `fs.ErrClosed`.
- All tests run under `make test` (race detector on) and `make lint`.

## Out of scope

- A cold tier or origin.
- URI and `MountSpecOptions` configuration.
- LRU or other eviction policies.
- Using `FileLog` in simplecachefs.
- `Watch` on the overlay or the cache.
- Compaction of the bolt file.
