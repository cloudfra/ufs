# Key Material Guard Wrapper — Design Spec

**Date:** 2026-11-16
**Issue:** <https://github.com/cloudfra/ufs/issues/689>
**Status:** Proposed

## Goal

Add a wrapper that sits over another ufs file system and refuses access to the
files that hold security key material, or that contain it: private keys,
keystores, `.env` files, PKCS#12 and JWK documents, SSH keys, and any file
whose content carries a private-key marker.

The wrapper exists so that a caller handed a mounted tree — an agent tool
layer, a CLI, an embedder that exposes a nest to untrusted logic — can be given
a file system on which key material simply cannot be read, without asking every
caller to write its own check.

## Non-goals

- Not a sandbox. The wrapper is in-process advice to other in-process code. A
  caller that never uses it, or that unwraps it, or that reaches the same bytes
  through `os.Open` on the backing directory, is not stopped. This is stated in
  the package documentation, not only here.
- No content rewriting. The wrapper refuses; it never returns a redacted,
  masked, or truncated view of a protected file.
- No key generation, no encryption at rest, no key parsing, no certificate
  validation, no trust store handling.
- No general deny policy. A rule language for "one declarative policy per
  mount" is issue #514. This wrapper ships one narrow policy over one narrow
  catalog, and is designed so #514 can reuse its matching layer.
- No work in `cloudfra/agentbox`. That repo pins ufs v0.16.1, which predates
  `drivers/types`, `ops`, and `drivers/polyfills`, so it cannot consume this
  wrapper until it re-pins. The design keeps the constructor shape something
  agentbox can call once it does.

## Naming

Package `drivers/wrappers/keyguardfs`, package name `keyguardfs`, unexported
type `keyGuardFS`, decorator option name `keyGuard` (a later phase). This
follows the existing `drivers/wrappers/readwrapfs` and
`drivers/decorators/readonlyfs` shape: a lower-case `<thing>fs` package holding
an unexported wrapper type and an exported constructor.

Rejected:

- `secretfs` — the catalog covers non-secret material (certificate and CA
  bundles, public-key-adjacent keystores), and "secret" promises a general
  secrets scanner this wrapper is not.
- `redactfs` — promises content rewriting, which is a non-goal.
- `restrictfs`, `denyfs`, `policyfs` — promise the general rule language of
  #514. Naming this narrow thing narrowly keeps that name free.
- `keysfs` — reads as a file system *of* keys, like `boltfs` is a file system
  of bolt data.

## Architecture

### Placement and shape

The first phase is a read-side wrapper, placed exactly where
`readwrapfs` sits: it wraps a `types.ReadFS` and returns a `types.ReadFS`.

```go
type keyGuardFS struct {
	inner       types.ReadFS
	patterns    []string
	markers     []string
	prefixBytes int
	hide        bool
}

var (
	_ types.ReadFS    = (*keyGuardFS)(nil)
	_ types.WrapperFS = (*keyGuardFS)(nil)
)
```

The struct holds `inner` as a field, not an embedded interface. Embedding
`types.ReadFS` would promote only read methods, so it would be safe today, but
a field makes "this type implements exactly the methods written in this file"
true, which is what keeps a future write-side phase from inheriting an
unpoliced method by accident.

Because the phase-1 return type is `types.ReadFS`, the wrapper exposes no
`Create`, `OpenFile`, `MkdirAll`, `Remove`, `Rename`, or `CopyFile` at all. That
closes two bypasses by construction rather than by check: nothing can be
created over a key, and `ops.Copy` out of the tree has to read through the
guard, because `ops.Copy` falls back to `srcFS.Open` when the source is not a
`types.CopyFileFS` and this type is not one.

Rejected:

- Wrapping `ufs.FS` and re-exporting the write side (issue #689's "wrapper
  over another `ufs.FS`"). It drags the write-path refusals of phase 4 into
  phase 1, and it makes the first PR cover eleven methods instead of the read
  set. The decorator form of #514 needs `types.WriteFS` anyway, so the write
  side gets its own package and PR later.
- Putting the wrapper in `drivers/decorators/` now. `ufs.NewDecorator` takes and
  returns `types.WriteFS`, so a read-side guard cannot register as a decorator
  without changing that signature. Importing the wrapper package and calling
  `New` is how it is used until the decorator signature is widened (that
  widening is issue #513's territory, not this PR's).

### Enforcement point

The wrapper polices every operation that can return a protected file's content
or confirm its state. Operations that return neither are forwarded, and the
reason is given in the table rather than left implicit.

| Operation                       | `refuse` mode (default)                                   | `hide` mode                                                  |
| :------------------------------ | :-------------------------------------------------------- | :----------------------------------------------------------- |
| `Open`                          | `*fs.PathError`, `fs.ErrPermission`                       | `*fs.PathError`, `fs.ErrNotExist`                            |
| `ReadFile`                      | refused, as `Open`                                        | missing, as `Open`                                           |
| `Stat`, `Lstat`                 | forwards the real `size`, `mode`, and `modTime` (a leak)  | `fs.ErrNotExist`                                             |
| `ReadLink`                      | forwards the target name (a leak)                         | `fs.ErrNotExist`                                             |
| `ReadDir`                       | forwards every entry, protected ones included             | protected entries removed from the slice                     |
| `Close`                         | forwards to `inner`                                       | forwards to `inner`                                          |
| `DeviceInfo`                    | forwards                                                  | forwards                                                     |
| `URI`                           | forwards unchanged                                        | forwards unchanged                                           |
| `String`                        | describes the guard, its mode, and its pattern count      | same                                                         |
| `IsSame`, `InnerFS`             | forwards (see the WrapperFS contract below)               | forwards                                                     |
| `IsMountedArchiveDir`           | forwards                                                  | forwards                                                     |

`fs.Glob`, the `types.ListFilenames`, `types.ForEachFilenameIter`, and
`types.ForEachFileInfoIter` enumerators, and `types.Watcher` are not
implemented by `keyGuardFS` in phase 1. Not implementing them is a decision,
not an oversight:

- Not implementing `fs.GlobFS` means `fs.Glob` on the guard falls back to the
  standard library walk over the guard's own `ReadDir`, so `hide` mode filters
  it for free. Implementing it by forwarding to an inner `fs.GlobFS` would leak
  protected names in `hide` mode and duplicate the filtering in a second place.
- Not implementing the enumerator interfaces means `ops.ForEachFilename` and
  friends fall back to `fs.WalkDir` over `ReadDir`. Same reasoning, same cost:
  a slower path that cannot disagree with `ReadDir`.
- Not implementing `types.Watcher` means `ops` sees no watcher at all rather
  than a watcher that reports notifications for protected paths. Reporting a
  change under a protected subtree leaks its name. `hide` mode needs a
  notification filter, which is a separate decision (see Open decisions).

The two modes are chosen so that `ReadDir`, `Stat`, and `Open` agree with each
other. Three combinations are coherent; two of them are shipped:

1. Listed, statable, unopenable. Shipped as `refuse`. This is what a file with
   no read permission looks like, and it is what `readonlyfs` does to mode
   bits.
2. Absent from listings, missing to every operation. Shipped as `hide`. This is
   what a file that was never there looks like.
3. Absent from listings, statable. Not shipped.

The rejected fourth combination — absent from listings, but `Open` answers
`fs.ErrPermission` — is the tempting one ("the file disappears from listings,
and if you happen to know its name you learn it is protected"). It is rejected
because it makes a directory listing and a `Stat` of the same name contradict
each other, which is exactly the kind of read-path disagreement the read
consistency work in PR #688 exists to catch, and because `fstest.TestFS` (run
by `drivers/testing/writefs.go`) walks listings and opens every entry it
finds, so a listed-but-unopenable entry is reported as a broken file system.
`hide` mode has the same problem in reverse and is therefore not the default.

`refuse` is the default for three reasons: it keeps the wrapper's listings
identical to its inner's, which the `types.WrapperFS` conformance suite
requires (see Testing); it never tells a lie about whether a path exists, so a
caller syncing or mirroring the tree does not silently drop a file; and an
operator debugging a mount can see what is being protected.

### Matching by name

A new internal package holds the matcher, because the same rule is needed by
the content guard of #513 and by a declarative policy in #514, and neither of
them should reach into a wrapper package for it.

```go
// Package patternutil matches file system paths against a list of patterns.
package patternutil

// Match reports whether name matches any pattern. A pattern matches when it
// matches the whole path, or when it matches any single slash-delimited
// component of it. An empty list matches nothing.
func Match(patterns []string, name string) bool

// Validate reports whether every pattern is a well-formed path.Match pattern,
// so a bad policy fails at construction instead of matching nothing forever.
func Validate(patterns []string) error
```

Component-level matching is what gives directory scoping without a `**`
extension: the pattern `.ssh` protects `.ssh/id_ed25519`, and `*.key` protects
`deploy/prod/api.key`. Whole-path matching is kept in the same rule so that a
pattern like `deploy/api.key` still means what it looks like.

Patterns are validated when the wrapper is constructed, so `New` returns an
error for a malformed pattern instead of silently protecting nothing. That
changes the constructor's signature to `(types.ReadFS, error)`, which is the
price of failing loudly; the alternative of panicking is what `RegisterDecorator`
does for a duplicate name, and a user-authored policy string is not a
programmer error.

Rejected:

- `github.com/bmatcuk/doublestar` or any `**` support. Nothing on main matches
  with `**`: `internal/globutil.GlobFS` and every driver's `Glob` use
  `path.Match`. A policy pattern that means something different from what the
  same file system's `Glob` means is a trap, and a new dependency for a feature
  component matching already covers is not worth it.
- Reusing `internal/globutil`. It walks a file system and returns matching
  paths; the guard needs to decide about one already-known path without
  walking. Wrong shape, and using it would make the guard read the tree it is
  trying to protect.
- Adding the matcher to `internal/pathutil`. `pathutil` is validity and
  spelling (`Validate`, `TrimSlash`, `CoerceUnix`) and has no pattern support
  today; a matcher is a different subject and deserves its own package.
- Caller-supplied predicates (`func(name string) bool`). A predicate cannot be
  written in the YAML a mount option takes, cannot be shown in `String()` so an
  operator can audit what is protected, and cannot be reused by #514's
  declarative policy. Declarative data is the whole point of the exercise.
- Regular expressions. Anchoring and escaping rules differ from `path.Match`,
  the YAML becomes unreadable, and the catalog needs "ends with `.key`", not
  a parser.

### Matching by content

Name matching fails on a key renamed to `notes.txt`, on a key copied to a
scratch name, and on a key reached through a link whose name says nothing.
Content detection is what makes the guard name-independent, so it is part of
the design and not a follow-up.

```go
// pemProtected reports whether the prefix holds a PEM block whose label
// contains PRIVATE KEY, which is how OpenSSH, PKCS#1, PKCS#8, PGP, SEC1 and
// encrypted keys announce themselves.
func pemProtected(prefix []byte) bool

// markerProtected reports whether any configured marker appears in the prefix.
func markerProtected(prefix []byte, markers []string) bool
```

PEM is handled structurally rather than as an exact-label list, so an unusual
label such as `-----BEGIN X9.42 ENCRYPTED PRIVATE KEY-----` is caught without
being enumerated. The extra markers are exact byte sequences for formats that
have no PEM wrapper:

```go
// DefaultMarkers are byte sequences sought in a file's leading bytes. PEM is
// detected structurally and is not listed here.
var DefaultMarkers = []string{
	"AGE-SECRET-KEY-",
	"\"private_key\"",
}
```

Detection runs in `Open` and `OpenFile`-style paths only, on the first
`prefixBytes` bytes read from the inner file system. When no marker or PEM block
is found, the bytes already consumed are replayed in front of the handle, so the
caller still reads the whole file:

```go
type replayFile struct {
	fs.File
	prefix []byte
}
```

`replayFile.Read` drains `prefix` before delegating. The handle's `Seek`
behavior is whatever the inner handle offers, with the prefix counted: seeking
is only offered when the inner handle is an `io.Seeker`, and the offset is
shifted by `len(prefix)` — and a guard configured to sniff must accept that a
sniffing handle is a wrapped handle, which is a cost stated in Known
limitations.

The default prefix is 8 KiB: PEM and age markers sit at offset 0, a JWK or
service-account document puts its private field early, and 8 KiB bounds the
cost to one short read per open. A negative `PrefixBytes` disables content
detection, which is the documented way to get name-only enforcement.

Rejected:

- Name-only enforcement. It fails on exactly the cases the issue names ("files
  that contain keys"), and a renamed or linked key is the interesting case.
- Sniffing during `ReadDir`. It would make hiding content-detected files
  coherent, at the cost of one read per entry per listing. Rejected for phase
  1 as a cost that scales with directory size, and left as an open decision.
- A content index or cache of detection results, keyed by path or content hash.
  That is a persistent state machine in its own right (the shape of
  `simplecachefs`), it has to be invalidated on every write, and it becomes a
  second source of truth about what is protected.
- Sniffing only when the name looks plausible. That is name matching with
  extra steps, and gives up the name-independence that justifies the read.

### Errors

A refusal is a `*fs.PathError` built by `ufserrors.NewPathError`, whose `Err`
wraps both the file-system sentinel and the policy reason:

```go
// ErrProtected names the reason a guarded file system refused: the path is
// protected by its key material policy. It unwraps to fs.ErrPermission.
var ErrProtected = errors.New("protected by the key material policy")
```

`errors.Is(err, fs.ErrPermission)` and `errors.Is(err, ErrProtected)` both
hold. The `Op` field carries the operation being refused (`open`, `readfile`),
matching the `Op` strings `pathutil.Validate` and `readonlyfs` already use, so
a refusal looks like any other ufs error and a `host` mount can map it to
`EACCES`. In `hide` mode the error is `fs.ErrNotExist` and carries no
`ErrProtected`, because claiming a protected file exists would undo the mode.

Rejected:

- Plain `fs.ErrPermission` with no policy sentinel. A caller cannot tell a
  guard refusal from a backend that genuinely cannot read the file, and #548's
  audit is moving the repo toward named sentinels, not away from them.
- A dedicated error type with fields for the matched pattern and the match kind.
  It reports *which* rule matched, which helps an operator author a policy and
  tells a caller what to avoid naming; the repo's convention is
  `fs.PathError`/`os.LinkError`, and deciding whether the matched rule is
  disclosed to the caller is a policy question, not a style one. Left in Open
  decisions.
- A sentinel that does not unwrap to a standard one. Callers in this repo match
  on the standard sentinels, and issue #548 exists because that already breaks
  in places.

### Core API

```go
// Package keyguardfs wraps a file system so that files holding security key
// material cannot be read through it. The guard is in-process: a caller that
// reaches the same bytes another way is not restricted by it.
package keyguardfs

// Modes select how a protected file is presented to a caller.
const (
	// ModeRefuse lists and stats a protected file, and refuses to open it.
	ModeRefuse = "refuse"
	// ModeHide removes a protected file from listings and reports it as
	// missing to every operation.
	ModeHide = "hide"
)

// DefaultPrefixBytes is how many leading bytes of a file are read looking for
// key material when Options.PrefixBytes is zero.
const DefaultPrefixBytes = 8192

// Options declares which files the wrapper protects. The zero value protects
// the built-in catalog in refuse mode, with content detection on.
type Options struct {
	// Patterns are path.Match patterns. A name is protected when a pattern
	// matches the whole path or any single component of it, so ".ssh"
	// protects everything under a .ssh directory. Empty selects
	// DefaultPatterns.
	Patterns []string `yaml:"patterns"`
	// Markers are byte sequences sought in the leading bytes of a file, in
	// addition to the structural PEM private-key check. A match protects the
	// file whatever its name is. Empty selects DefaultMarkers.
	Markers []string `yaml:"markers"`
	// PrefixBytes bounds the leading bytes content detection reads. Zero
	// selects DefaultPrefixBytes, a negative value disables detection.
	PrefixBytes int `yaml:"prefixBytes"`
	// Mode is ModeRefuse or ModeHide. Empty selects ModeRefuse.
	Mode string `yaml:"mode"`
}

// DefaultPatterns is the built-in name catalog.
var DefaultPatterns = []string{
	".env", ".env.*",
	"*.key", "*.pem", "*.p12", "*.pfx", "*.pkcs12",
	"*.jks", "*.keystore", "*.kdb", "*.kdbx",
	"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "id_xmss", "id_sk_*",
	"credentials.json", "service-account*.json",
	".ssh", "secrets",
}

// DefaultMarkers is the built-in content catalog other than PEM.
var DefaultMarkers = []string{
	"AGE-SECRET-KEY-",
	"\"private_key\"",
}

// ErrProtected names the reason a guarded file system refused a protected path.
var ErrProtected = errors.New("protected by the key material policy")

// New returns a wrapper around inner that refuses protected paths. It reports
// an error for an unknown Mode or a malformed pattern, so a policy that does
// not compile fails when it is built rather than protecting nothing.
func New(inner types.ReadFS, opts Options) (types.ReadFS, error)
```

`String()` returns JSON through `types.Description` and `types.Nested`, as the
description policy requires, and names the guard, its mode, and the pattern and
marker counts so an operator can see the policy is present without dumping it.

## Bypass surfaces

Each of these is reachable today; what closes it, or why it stays open, is
stated rather than assumed.

1. **Not using the guard, or unwrapping it.** `types.InnerFS()` and
   `types.InnermostFS` hand back the unguarded file system, and the caller who
   passed `inner` to `New` still holds it. The guard restricts what a caller
   *handed the guard* can do. A caller that keeps or recovers the inner handle
   is outside its reach. `InnerFS()` returning the raw inner is required by the
   `types.WrapperFS` contract and by PR #684's ruling on unwrapping, so the
   guard cannot close this without breaking that contract; the package
   documentation states the limit instead.
2. **A mounted sub-file system.** The guard covers the tree it wraps. In a nest,
   reads through a sibling mount reach the same bytes without passing through
   this guard, and a guard applied *below* a mount point guards only that mount.
   The rule the documentation gives: wrap the file system at or above the point
   where the protected tree is mounted, and a test seeds a nest with a mount
   under a guarded directory to pin that reads through that mount are refused.
3. **Path escapes.** `pathutil.Validate` runs first in every method, so
   absolute paths, `..`, `.`, trailing slashes and the eleven names in
   `drivers/testing`'s shared invalid-path list fail with `fs.ErrInvalid`
   before any policy decision, exactly as `readwrapfs` does. There is no
   policy-relevant path spelling left to exploit.
4. **Symbolic links inside the wrapped tree.** Name matching sees only the link
   path, so `notes/link -> keys/id_ed25519` is not caught by a name rule.
   Content detection *does* catch it, because detection runs on the bytes the
   opened path actually returns, which is the main reason it is in the design.
   A marker-less binary key behind a clean name is not caught; resolving the
   link target and re-checking it is an open decision.
5. **Metadata leaks in `refuse` mode.** `ReadDir`, `Stat`, and `ReadLink` report
   a protected file's name, size, mode, and mod-time; `URI`, `DeviceInfo`, and
   `IsMountedArchiveDir` confirm the path exists. `hide` mode closes this, at
   the WrapperFS-contract cost described in Testing. Masking size and times in
   `refuse` mode (the `readonlyfs` precedent for mode bits) is an open decision
   and more likely the right answer than `hide` mode is.
6. **Copy or rename into an unprotected name.** Phase 1 has no write surface, so
   the guard cannot be used to copy a key to a scratch name. The decorator
   phase, which does expose writes, must refuse when *either* endpoint of a
   `CopyFile` or `Rename` is protected — checking only the destination is the
   bug that turns a copy into a read.
7. **A cache above the guard.** If a caching layer (`simplecachefs`, #613) sits
   outside the guard, key content already fetched stays readable through that
   layer. The documented ordering rule is that the guard goes outermost on the
   read path; decorator ordering is an open decision.
8. **Change notifications.** A watcher on an ancestor of a protected subtree
   reports that subtree's names. Phase 1 implements no watcher, so a guarded
   file system is simply not watchable rather than watchable and leaky; the
   cost (a guarded mount loses `ops` notification support) is stated in Known
   limitations.
9. **Outside ufs entirely.** `os.Open` on the backing directory, or any other
   process with access to it, is unaffected. A `host` mount handed the guarded
   file system does get the guard (its refusals become `EACCES`), because
   `host.Mount` takes a `types.ReadFS` and reads through it.

## Changes

Phase 1 (the implementation PR this spec precedes):

| Path                                                     | Change                                                                                                                                                     |
| :------------------------------------------------------- | :--------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/patternutil/patternutil.go`                    | New. `Match` and `Validate` over `path.Match`, whole-path and per-component.                                                                               |
| `internal/patternutil/patternutil_test.go`               | New. Component and whole-path matching, malformed pattern reporting, empty list matching nothing.                                                          |
| `drivers/wrappers/keyguardfs/keyguardfs.go`              | New. `Options` with `Patterns` and `Mode` only, defaults, `New`, the read methods in the enforcement table, `String`, `URI`, `IsSame`, `InnerFS`, `Close`. |
| `drivers/wrappers/keyguardfs/keyguardfs_test.go`         | New. Policy tables, refusals for every op in both modes, and mode and pattern validation errors.                                                           |
| `drivers/wrappers/keyguardfs/keyguardfs_driver_test.go`  | New. `ufsdriversTesting.FS(t, ufsdriversTesting.Args{ReadOnly: true}, …)` over a seeded fixture, so the shared suites run here.                            |
| `docs/superpowers/specs/2026-11-16-keyguardfs-design.md` | This file.                                                                                                                                                 |

No existing file changes in phase 1: the wrapper registers nothing, so
`drivers/all/all.go` is untouched, and no caller is modified.

Later phases (each its own PR, see Implementation plan):

| Path                                        | Phase | Change                                                                                                            |
| :------------------------------------------ | :---- | :---------------------------------------------------------------------------------------------------------------- |
| `drivers/wrappers/keyguardfs/sniff.go`      | 2     | New. Prefix read, `pemProtected`, `markerProtected`, `replayFile`; adds `Markers` and `PrefixBytes` to `Options`. |
| `drivers/wrappers/keyguardfs/sniff_test.go` | 2     | New. PEM labels, marker placement, the prefix boundary, and byte-identical replay.                                |
| `drivers/wrappers/keyguardfs/watch.go`      | 3     | New. `types.Watcher` forwarding with protected-name refusal and notification filtering in `hide` mode.            |
| `drivers/decorators/keyguardfs/`            | 4     | New. YAML `Options`, `init()` registering the `keyGuard` decorator, delegating to `keyguardfs`.                   |
| `drivers/all/all.go`                        | 4     | Adds the blank import and the `keyGuard` entry in the package comment's decorator list.                           |
| `drivers/testing/wrapperfs.go`              | 2     | Possibly an `Args` field for a wrapper that does not list what its inner lists. Owner decision.                   |

## Testing

- `keyguardfs_driver_test.go` runs the shared conformance funnel exactly the way
  the `readwrapfs` and `readonlyfs` driver tests do, with `Args{ReadOnly: true}`
  over a seeded file system, not an empty one. The fixture holds `keys/` with a
  PEM private key, a `server.key`, a `.env` file with a marker inside it,
  `notes.txt` with a PEM block past the 8 KiB mark (the documented miss), a
  marker-free binary blob, and a directory. The content assertions in the
  shared suites are only meaningful with non-empty fixtures, so the seed matters.
- The fixture keeps protected names out of the wrapper's root directory while
  the default mode is under test. `drivers/testing/wrapperfs.go`'s
  `innerListsSameEntries` compares `ReadDir(".")` on the wrapper with
  `ReadDir(".")` on `InnerFS()` and requires the two to be equal, so a guard
  that hides a root-level entry fails that suite. `refuse` mode passes it
  because its listings are unchanged; `hide` mode is exercised only in
  package-local tests, and making it pass the shared suite needs either a
  policy that protects no root entry or an `Args` opt-out the owner has to
  accept. That conflict is stated here rather than designed around silently.
- Refusal coverage, in `keyguardfs_test.go`, one case per operation per mode:
  `Open`, `ReadFile`, `Stat`, `Lstat`, `ReadLink`, `ReadDir` filtering, plus
  `errors.Is` checks against `fs.ErrPermission` and `ErrProtected` in `refuse`
  mode and against `fs.ErrNotExist` in `hide` mode. Each guard method validates
  the path first, so the shared invalid-path names must still produce
  `fs.ErrInvalid`, not a refusal — a policy that swallows `ErrInvalid` is a
  contract break.
- Matching coverage: whole-path patterns, component patterns (`.ssh` covering
  `.ssh/id_ed25519`), a pattern that matches nothing, a malformed pattern
  rejected by `New`, an unknown `Mode` rejected by `New`, content detection on a
  PEM key, on an unusual PEM label, on a marker, off when `PrefixBytes` is
  negative, and the prefix boundary (a marker ending exactly at the limit, and
  one starting one byte past it).
- Replay coverage: reading a protected-adjacent file in one read, in many small
  reads, and via `io.ReadAll` returns byte-identical content whether or not
  sniffing consumed a prefix, which is what keeps the wrapper from eating the
  first 8 KiB of every file.
- Cost coverage: a benchmark of `Open` with detection off, on a clean file, and
  on a protected file, added with phase 2 and reported in that PR. A change that
  adds per-open cost gets measured numbers, not an argument.
- Every new test is checked by breaking the implementation: delete the
  `ReadFile` guard and its case must fail; match whole-path only and the
  component case must fail; drop the replay and the byte-identity case must
  fail. The PR body states which mutations were tried.
- Locally: `go test ./drivers/wrappers/keyguardfs/ ./internal/patternutil/`,
  then `make testassets` once and a full `go test ./...`, and `make lint-go`
  before pushing. `make lint-vuln` is not run here because the local toolchain
  makes it red on every repo. CI's `make presubmit` on Linux and Windows is
  authoritative.
- Not provable locally: `host` mount behavior (no `/dev/fuse` here), so the
  `EACCES` claim in bypass surface 9 is inference from `host.Mount` reading
  through the `types.ReadFS` it is given, not a mount-verified fact.

## Known limitations

- Content sniffing reads the first `PrefixBytes` (8 KiB by default) and can be
  fooled in both directions. A key whose marker sits past the prefix is read. A
  file that merely quotes `-----BEGIN OPENSSH PRIVATE KEY-----` — a README, a
  test fixture, this spec's own examples — is refused.
- Formats with no textual marker are name-only. A raw 32-byte Ed25519 seed, a
  binary PKCS#12 blob, and an encrypted keystore with no recognizable header are
  protected only if their name matches.
- In-process only. Unwrapping, not using the guard, or reading the backing
  directory from another process defeats it entirely.
- `refuse` mode leaks name, size, mode, and mod-time through `ReadDir`, `Stat`,
  `Lstat`, and `ReadLink`.
- `hide` mode breaks the `types.WrapperFS` conformance expectation that a
  wrapper lists what its inner lists, whenever the policy protects a root entry.
- Phase 1 implements no watcher and no `fs.GlobFS`, so a guarded file system
  loses notification support and `Glob` takes the slower walk.
- Every opened file pays up to 8 KiB of read and, when the inner handle is
  re-wrapped for replay, one extra indirection.
- Symlink targets are not resolved, so a marker-less key behind a clean name is
  reachable.
- Patterns are `path.Match`: no `**`, no case-insensitivity, no brace
  expansion, and the semantics are case-sensitive exactly as every driver's
  `Glob` is.

## Out of scope

- The `agentbox` and ADK tool-layer integration, and `posixcli`.
- A general or declarative deny policy, rule composition, or per-mount policy
  merging (#514).
- Guarding non-key content (#513's general content guard), redaction, DLP on
  writes, and key rotation or management.
- Cross-process or OS-level enforcement, mount namespaces, and file permissions.
- Parsing, validating, or evaluating keys and certificates.
- Any change to the `ufs.NewDecorator` signature or to decorator ordering.

## Implementation plan

| Phase | PR                                                                                                                                             | Deliberately cut                                                                                        |
| :---- | :--------------------------------------------------------------------------------------------------------------------------------------------- | :------------------------------------------------------------------------------------------------------ |
| 0     | This spec.                                                                                                                                     | Any code.                                                                                               |
| 1     | `internal/patternutil` + `keyguardfs` with name matching, both modes, refusals, conformance, tests.                                            | Content detection, so the first PR is reviewable as a policy surface and the cost question is deferred. |
| 2     | Content detection: prefix read, PEM, markers, replay handle, benchmarks.                                                                       | Watching, decorator registration, metadata masking.                                                     |
| 3     | `types.Watcher` forwarding with filtering, plus metadata masking in `refuse` mode if the owner wants it.                                       | Decorator registration and the write path.                                                              |
| 4     | `drivers/decorators/keyguardfs` with YAML options, `keyGuard` registration in `drivers/all/all.go`, and write-path refusal on either endpoint. | Any change to decorator ordering or to the decorator signature.                                         |

Phase 1 may be split further into `internal/patternutil` and the wrapper if the
first PR reads as too large. Each PR is one squash commit on top of latest
`main`.

## Open decisions

1. Is `refuse` the right default, or should the guard mask a protected entry's
   size and mod-time while keeping it listed? Masking keeps listings equal to
   the inner's (so the WrapperFS suite stays green) and closes most of the
   metadata leak; `hide` closes the leak but contradicts that suite.
2. Should `hide` mode be offered at all, given that it breaks
   `drivers/testing/wrapperfs.go`'s equal-listings assertion when a policy
   protects a root entry? The options are an `Args` opt-out, keeping the mode
   untested by the shared suites, or dropping the mode.
3. Which catalog is default? Whether certificate and CA bundles (`.crt`,
   `.cer`, `.pem` bundles) and `*.pub` are protected, and whether directory
   names (`keys`, `credentials`) belong in the defaults or stay opt-in. The
   issue names cert/CA bundles; those files hold public keys, which argues for
   protecting them for integrity rather than secrecy, and that is a
   product call.
4. Is the matched rule disclosed in the error? Naming the pattern helps an
   operator author a policy and tells an agent what to stop trying; it also
   hands the caller information about the policy shape.
5. Should the guard annotate the inner's URI the way `readwrapfs` and
   `readonlyfs` add `ro=true`, or leave it untouched? The URI suite in PR #679
   pins what a wrapper may change, and this wrapper changes no data identity.
6. Should `New` return an error for a bad pattern, or accept a pattern list it
   validates later? The spec assumes an error, which forces the two-value
   constructor; a one-value constructor would have to panic or misprotect.
7. Should symlink targets be resolved and re-checked at open, and at what cost
   for file systems whose `ReadLink` is expensive?
8. Where does the guard sit relative to caches and to `readOnly` in decorator
   ordering, once phase 4 registers it? This is the ordering half of #514.
