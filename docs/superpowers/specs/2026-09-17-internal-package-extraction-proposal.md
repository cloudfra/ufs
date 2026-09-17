# Internal Package Extraction Proposal

Branch: `internal-package-extraction-proposal`.

## Context

`ufs` is heading toward splitting its drivers into smaller,
conditionally-importable packages (à la `database/sql` drivers). Two
precedents already exist for pulling low-coupling code out of the
monolithic `package ufs` into `internal/`:

- `internal/osutil` — thin, path-cleaning wrappers around `os`.
- `internal/mathutil` — three clamp functions, pure `math`.

Both share the same shape: no dependency on any `ufs`-specific type
(`FS`, `File`, `ReadFS`, ...), just stdlib in, stdlib out. This proposal
surveys the rest of the root package (starting from the `*util.go`-style
files, per your request) for more code with that same shape, and groups
the candidates logically. Nothing here has been implemented — it's a
proposal to react to before any extraction starts.

## How candidates were found

Grepped every root-package `.go` file (production code, not `_test.go`)
for functions/structs whose bodies reference only stdlib types plus
(at most) each other — never `FS`, `File`, `ReadFS`, or a concrete
backend (`memFS`, `localFS`, ...). Call-site counts below are from the
current tree, to size the mechanical rename/import-path cost of each
extraction (the code itself has "no or little" dependency either way —
the caller count only affects how many files change import lines).

## Candidates, most to least ready

### 1. `internal/download` — from `osutil.go` (root)

This is the file you flagged as the model to follow. Everything in it —
`newHTTPClient`, `dialControl`, `validateDownloadURL`, `isBlockedIP`,
`sanitizeFilename`, `downloadFile`/`downloadFileWith`, `maxDownloadSize`
— is SSRF-hardened HTTP download logic with zero reference to any `ufs`
type. It's used by `archivefs.go` (to fetch a remote archive before
mounting it). Straightforward move; only the two call sites in
`archivefs.go` need updating to `download.File(ctx, dir, uri)` (or
whatever the exported name becomes — today's `downloadFile` is
unexported, so this move also decides its exported name/shape).

### 2. `internal/notifybus` — from `memfs_notify.go` + `boltfs_notify.go`

Not a `*util.go` file, but the strongest "little dependency" case in the
production (non-test) code: `memfs_notify.go`'s `memWatcher`/
`memNotifyEvent` and `boltfs_notify.go`'s `boltWatcher`/`boltNotifyEvent`
are **structurally identical** — same fields, same `matches`/`send`/
`loop`/`Close` bodies, same `removeWatcher`/`notify` broadcast pattern on
the owning FS. The only backend-specific part is the `fsys *memFS` /
`fsys *boltFS` back-reference used solely to call `removeWatcher` on
close. `gcsfs_notify.go` is *not* part of this — it's built on real GCS
Pub/Sub subscriptions, a genuinely different mechanism.

Proposed shape: a generic prefix-matching broadcaster —

```go
package notifybus

type Op int
type Hook func(op Op, path string)

type Bus struct { /* mu + []*subscription */ }
func New() *Bus
func (b *Bus) Subscribe(ctx context.Context, prefix string, hook Hook) *Subscription
func (b *Bus) Publish(op Op, path string)
```

`memFS`/`boltFS` each hold a `*notifybus.Bus` instead of hand-rolling
`watchers`/`watchersMu`/`removeWatcher`; `Watch()` keeps its
backend-specific validity checks (does the path exist, is it a
directory) and then just calls `bus.Subscribe`. `NotifyOp` (in `ufs.go`,
public API) converts to/from `notifybus.Op` via a plain `int` cast at the
two call sites — the internal package never needs to know about `ufs`'s
exported `NotifyOp` type. This deletes ~80 lines of exact duplication.

### 3. `internal/pathutil` — from `path.go`

Pure string/path helpers, stdlib-only (`path`, `path/filepath`,
`strings`, `io/fs`, `runtime`): `removePathPrefix`, `trimSlash`,
`splitPath`, `coerceUnix`, `isDirName`, `isCwd`, `validPath`,
`pathError`, `joinErrors`, plus the separator/`"."` constants. Call
counts are the widest of any candidate here (`pathError`: 20 files,
`validPath`: 18 files), so this is the most mechanical, highest-line-diff
move, but each call site's change is a trivial rename
(`pathError(...)` → `pathutil.Error(...)`, etc.) — no logic changes.

**Stays behind:** `AbsPath` (exported public API), `realAbsPathGet`,
`realAbsPathNotSupported` — these encode `ufs`'s own "resolve to a real
OS path" contract via the unexported `getAbsPath` interface, not a
generic path utility.

### 4. `internal/fsinfo` — from `info.go`

`fsInfo` (a plain `fs.FileInfo` struct) and `virtualDirEntry`/
`makeVirtualDirEntry` (a synthetic directory `fs.DirEntry`+`fs.FileInfo`,
used for implicit/virtual directories) depend only on `io/fs` and `time`.
7 files construct an `fsInfo{...}` literal today; exporting the struct's
fields (`Name`→`name`, etc.) or keeping constructor functions
(`fsinfo.New(name, size, mode, modTime, isDir, sys)`) is a real decision
to make during implementation, since call sites build it as a struct
literal, not via a constructor, today.

**Stays behind:** `readDirFile`/`makeReadDirFile` — takes a `ReadFS`
field, genuinely `ufs`-specific.

### 5. `internal/deviceinfo` — from `deviceinfo.go` (medium effort)

`deviceInfo` (struct), `combineDeviceInfo`, `getParentDeviceInfo`,
`newDeviceInfoMap`, `getDeviceInfoOrDefault` are all self-contained
(string/map manipulation over a `map[string]deviceInfo]`, `getDeviceInfoOrDefault`
takes only a stdlib `fs.FS`). The catch is breadth, not coupling: 14
files implement the `getDeviceInfo() map[string]deviceInfo` interface or
construct a `deviceInfo{}` literal (`angryfs.go`, `boltfs.go`,
`embedfs.go`, `nestfs.go`, `memfs.go`, `readwrapfs.go`, `faultfs.go`,
`archivefs.go`, `gcsfs.go`, `nullfs.go`, `tempmountfs.go`, plus the three
`localfs_deviceinfo_*.go` platform files). Every implementer's method
signature changes from `map[string]deviceInfo` to
`map[string]deviceinfo.Info`, so this is the widest blast radius here
even though the extracted code itself has no real dependency on the rest
of the package. I'd sequence this *after* 1–4 land, once the
extract-to-`internal/` pattern is proven on lower-traffic code.

### 6. `internal/buffile` — from `buffile.go` (flagged, not "little dependency" as-is)

`bufFile` (shared by `memFile` and `boltFile`) only imports stdlib
(`fmt`, `io`, `io/fs`, `path`, `sync`, `time`) and calls into candidates
#3/#4 above (`pathError`, `fsInfo`) — build-wise it looks like another
easy win. But `memFile`/`boltFile` don't go through a method API: they
embed `bufFile` and reach directly into its **unexported fields**
(`f.mu`, `f.content`, `f.path`, `f.mode`, `f.modTime`, `f.dirty`) and call
`f.writeAtOffsetLocked` from their own `Write`/`WriteString`/`Close`.
That's real, tight coupling — just expressed as field access instead of
an import. Extracting this cleanly means first turning `bufFile` into a
real public base type (exported fields or an exported mutation API), a
small API-design task in its own right, not a pure move. I'd treat this
as a follow-on to #1–4, not a starting point.

## Suggested sequencing

1. `internal/download` (osutil.go) — matches the pattern you asked for
   almost exactly, single caller file, no design decisions needed.
2. `internal/notifybus` (memfs_notify.go + boltfs_notify.go) — highest
   value per line changed, since it deletes real duplication rather than
   just relocating code.
3. `internal/pathutil` (path.go) — mechanical, but touches the most
   files; good to do once the review pattern from #1–2 is settled.
4. `internal/fsinfo` (info.go) — small, but needs a constructor-vs-export
   decision first.
5. `internal/deviceinfo` (deviceinfo.go) — same shape as #4, wider blast
   radius (14 files).
6. `internal/buffile` (buffile.go) — needs an API redesign before it's a
   clean move; not ready to just "pull out."

Nothing has been extracted yet. Let me know which of these you want to
proceed with (I'd suggest starting with #1 and #2), and whether you want
each as its own PR/branch or bundled.
