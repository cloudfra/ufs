# Restricted-Access Wrapper (RestrictFS) — Design Spec

**Date:** 2026-11-16
**Issue:** <https://github.com/cloudfra/ufs/issues/689>
**Status:** Proposed, revised after review (2026-11-21)

## What the review changed

The first revision described a read-side `keyguardfs` with two presentation
modes over a `types.ReadFS`. The review replaced that shape, and this spec now
follows it:

- The driver is `RestrictFS`: a policy surface whose shipped policy restricts
  security-sensitive files, so a different sensitive-content policy configures
  it rather than duplicating it.
- It wraps `ufs.FS` and returns a `ufs.FS`, because it is meant to be applied
  on top of the `nestFS` tree that `ufs.New` builds.
- It implements every method, including the optional interfaces, instead of
  leaving them to `drivers/polyfills`.
- There is one mode, `restrict`: an entry stays visible and is inaccessible.
  `hide` and the two-mode table are gone.
- `Stat` and `Lstat` on a restricted entry report size zero.
- Content detection is generalized to sensitive content and runs during the
  read, so a file whose name says nothing fails in the middle of a read instead
  of being delivered whole.

The consequence of the last two rulings for the shared conformance suite is
spelled out in "What the read-consistency suite says about this"; it needs an
owner decision.

## Goal

Add a driver that restricts access to the entries its policy marks as
sensitive. A restricted entry keeps its presence: it is still listed, a stat of
it still answers, and its type is still reported, so a caller can see that
something is there and that it is blocked. What it does not get is access —
opening, reading, writing, copying, renaming or removing that entry fails.

The policy is configurable, and the driver ships one: the security-sensitive
file policy, which marks private keys, keystores, `.env` files, PKCS#12 and JWK
documents and SSH keys by name, and marks by content any file that holds
sensitive material such as a private key. Restricting that catalog is what
issue #689 asks for; the driver around it is general, so a policy for other
sensitive content configures the same driver instead of adding a second
wrapper.

The driver exists so that a caller handed a mounted tree — an agent tool layer,
a CLI, an embedder that exposes a nest to untrusted logic — can be given a file
system on which sensitive material cannot be touched, without asking every
caller to write its own check.

## Non-goals

- Not a sandbox. The driver is in-process advice to other in-process code. A
  caller that never uses it, or that unwraps it, or that reaches the same bytes
  through `os.Open` on the backing directory, is not stopped. This is stated in
  the package documentation, not only here.
- No content rewriting. The driver refuses; it never returns a redacted,
  masked, or truncated view of a restricted file's content.
- No key generation, no encryption at rest, no key parsing, no certificate
  validation, no trust store handling.
- No rule language. The policy is a Go interface plus a shipped catalog. A
  declarative rule language for "one policy per mount" is issue #514, which is
  expected to reuse this driver's matching layer and its `Policy` seam.
- No work in `cloudfra/agentbox`. That repo pins ufs v0.16.1, which predates
  `drivers/types`, `ops`, and `drivers/polyfills`, so it cannot consume this
  driver until it re-pins. The design keeps the constructor shape something
  agentbox can call once it does.

## Naming

Package `drivers/wrappers/restrictfs`, package name `restrictfs`, unexported
type `restrictFS`, exported policy seam `Policy`, decorator option name
`restrict` (a later phase). This follows the existing `drivers/wrappers/readwrapfs`
and `drivers/decorators/readonlyfs` shape: a lower-case `<thing>fs` package
holding an unexported wrapper type and an exported constructor.

The shipped security-sensitive file policy is `SensitiveFilePolicy`, built by
`NewSensitiveFilePolicy`, and the zero `Options` selects it.

Rejected:

- `keyguardfs`, "key material guard" — this spec's first revision, and the name
  PR #691 uses. It named the catalog rather than the driver: the thing being
  designed is a policy surface over sensitive entries, and key material is one
  policy's subject.
- `secretfs` — "secret" promises a general secrets scanner this driver is not,
  and the shipped catalog also marks non-secret material (certificate and CA
  bundles, public-key-adjacent keystores).
- `redactfs` — promises content rewriting, which is a non-goal.
- `denyfs`, `policyfs` — promise the declarative rule language of #514.
- `keysfs` — reads as a file system *of* keys, like `boltfs` is a file system
  of bolt data.

## Architecture

### Placement and shape

The driver wraps a `ufs.FS` and returns a `ufs.FS`. It is meant to sit over the
tree `ufs.New` builds, whose root is a `nestFS`, so that one guard covers every
mounted layer at once:

```text
caller → restrictFS → nestFS → archiveFS / memFS / localFS / …
```

```go
// New returns a wrapper around inner that restricts the entries its policy
// marks.
func New(inner ufs.FS, opts Options) (ufs.FS, error)
```

Naming `ufs.FS` in a `drivers/` package adds no import cycle: `drivers/core/localfs`,
`drivers/core/memfs`, `drivers/boltfs` and `drivers/testing` already import the
root package.

```go
type restrictFS struct {
	inner  ufs.FS
	policy Policy
}

var (
	_ ufs.FS                    = (*restrictFS)(nil)
	_ fs.GlobFS                 = (*restrictFS)(nil)
	_ types.ListFilenames       = (*restrictFS)(nil)
	_ types.ForEachFilenameIter = (*restrictFS)(nil)
	_ types.CopyFileFS          = (*restrictFS)(nil)
	_ types.Watcher             = (*restrictFS)(nil)
	_ types.WrapperFS           = (*restrictFS)(nil)
)
```

The struct holds `inner` as a field, not an embedded interface. Embedding
`ufs.FS` would promote the whole surface — `OpenFile`, `RenameFile`, `Remove`,
`Release`, and every optional interface the inner answers — so the type would
implement more than this file writes, and a promoted method could reach the
inner file system without the policy ever being consulted. A field makes "this
type implements exactly the methods written in this file" true.

Because the surface is `ufs.FS`, the write side is policed too, and that is not
an optional extra: a guard that only refused reads would still let a caller
delete, move or replace the material it cannot read.

Rejected:

- Wrapping `types.ReadFS` and returning a `types.ReadFS` (this spec's first
  revision, and what PR #691 implements). It closed copy-out and creation over
  a restricted name by construction, because it exposed no write methods at
  all, but it cannot be applied where the protection is needed: `nestFS` and
  every mounted layer is a `ufs.FS`, so a read-side guard cannot wrap an
  assembled tree, and the write-side refusals are left to a later package.
- Putting the driver in `drivers/decorators/` now. `ufs.NewDecorator` takes and
  returns `types.WriteFS` (`register.go:98`), so a driver whose surface is
  `ufs.FS` cannot register as a decorator until that signature is widened; the
  widening is issue #513's territory. Importing the package and calling `New`
  is how the driver is used until then.

### Every interface is implemented, not inherited

`nestFS` answers optional interfaces of its own — `fs.GlobFS` (`nestfs.go:66`),
`AbsPathGetter` (`nestfs.go:67`) and `types.MountedArchiveDirFS`
(`nestfs.go:68`) — and memFS implements `fs.GlobFS` and `types.Watcher` itself
(`drivers/core/memfs.go:46` and `:48`), as localFS does (`drivers/core/localfs.go:52`).
The enumerators and iterators are the one place where every driver on main does
use a `drivers/polyfills` factory. Nothing in the contract keeps it that way:
those interfaces are optional exactly so a driver can replace the polyfilled
path with its own, and a policy written only in the polyfilled methods
disappears the day a driver does. So the driver implements the optional
surfaces itself and consults the policy in each, rather than forwarding them to
an inner that has no idea a policy exists:

| Surface                                                                                                            | Implemented here                                  | Why                                                                                                                            |
| :----------------------------------------------------------------------------------------------------------------- | :------------------------------------------------ | :----------------------------------------------------------------------------------------------------------------------------- |
| `OpenFileFS`, `RenameFileFS`, `RemoveFS`, `RemoveAllFS`, `CreateFileFS`, `MkdirAllFS`, `ReleaseFS`                 | yes, they are part of `ufs.FS`                    | they exist whether this file writes them or not, and each can destroy or move restricted material                              |
| `fs.GlobFS`                                                                                                        | yes                                               | it returns names from a pattern the policy never saw, and every tree wrapper implements it natively                            |
| `types.ListFilenames`, `types.ForEachFilenameIter`, `types.ForEachFileInfoIter`                                    | yes                                               | they are the fast enumeration path; a driver that has one is never walked over `ReadDir`                                       |
| `types.CopyFileFS`                                                                                                 | yes                                               | `ops.Copy` prefers it over the `Open`-and-write fallback, so forwarding it is a read that never calls `Open`                   |
| `types.Watcher`                                                                                                    | yes, with restricted paths not reported           | implementing none means `ops` sees no watcher at all, so a restricted mount loses notification support                         |
| `types.MountedArchiveDirFS`, `DeviceInfoGet`, `URIGetFS`, `ComparableFS`, `WrapperFS`, `fmt.Stringer`, `io.Closer` | yes, forwarding, with masked or annotated results | they report existence and identity, not content; `InnerFS()` returns the raw inner, as the `types.WrapperFS` contract requires |

`types.MkdirFS`, `types.BatchCreateFS`, `types.ClosestParentFS` and
`types.ExternalPathGetFS` are left unimplemented, so callers fall back to the
methods above and pass the policy. Leaving each of those is a decision about
that interface, not an oversight: a batch create that skipped the per-path check
would be the one write path with no guard on it.

### Enforcement point

There is one mode, `restrict`: an entry the policy marks keeps its presence and
loses its access. Its name and its type stay in listings, its metadata is
masked, and every operation that would reach through it fails. Operations that
return neither content nor state are forwarded, with the reason in the table
rather than left implicit.

| Operation                                          | Restricted entry                                                                                      |
| :------------------------------------------------- | :---------------------------------------------------------------------------------------------------- |
| `Open`, `OpenFile`                                 | `*fs.PathError` wrapping `ErrProtected`, which unwraps to `fs.ErrPermission`                          |
| `ReadFile`                                         | refused, as `Open`                                                                                    |
| `Stat`, `Lstat`                                    | a masked `fs.FileInfo`: name and mode kept, `Size()` zero, zero mod-time                              |
| `ReadLink`                                         | refused — a target name is content, and reporting no target would be a lie about what is there      |
| `ReadDir`, the iterators, `Glob`                   | the entry is listed, described by the masked info; nothing is filtered out                            |
| `CreateFile`, `MkdirAll`                           | refused over a restricted name, so restricted material cannot be replaced or buried under a directory |
| `Remove`, `RemoveAll`                              | refused — a caller that cannot read a key is still not free to delete it                            |
| `RenameFile`                                       | refused when either endpoint is restricted                                                            |
| `CopyFile`                                         | refused when either endpoint is restricted                                                            |
| `Read` on a handle opened before detection matched | forwarded until the content policy matches, then the read fails with `ErrProtected`                   |
| `Release`, `Close`                                 | forwarded — they end a session, they do not read                                                    |
| `DeviceInfo`, `IsMountedArchiveDir`                | forwarded — they describe the mount, not the entry                                                  |
| `URI`                                              | forwarded unchanged (see Open decisions)                                                              |
| `String`                                           | names the driver, its policy, and its pattern and marker counts                                       |
| `IsSame`, `InnerFS`                                | forwarded (see the WrapperFS contract below)                                                          |

Checking both endpoints of `RenameFile` and `CopyFile` is what stops a rename
from becoming the copy-out the guard exists to prevent.

Masking `Size()` to zero is what keeps a stat from reporting how many bytes of
key material are on disk, and it is the size a `host` mount hands back in
`stat(2)`. The name and the mode are kept because the requested behavior is
"you can see the presence of the file but cannot access it". The masked info is
the same one `ReadDir` reports in its `fs.DirEntry`, so a listing and a stat of
the same name still describe one node.

Keeping restricted entries listed also keeps the `types.WrapperFS` conformance
expectation intact: `drivers/testing/wrapperfs.go`'s `innerListsSameEntries`
compares `ReadDir(".")` on the wrapper with `ReadDir(".")` on `InnerFS()` and
requires them to be equal, which a filtering wrapper fails whenever a policy
protects a root entry. The `hide` mode of the first revision failed it for that
reason, and it also made a caller syncing or mirroring the tree silently drop a
protected file. Both are gone.

#### What the read-consistency suite says about this

PR #688 added a `ReadConsistency` group to `drivers/testing/readfs.go`. Three
of its four subgroups assume that a listed regular entry is reachable, and a
restricted entry deliberately is not:

| Subgroup      | What it asserts                                                             | A restricted entry                |
| :------------ | :-------------------------------------------------------------------------- | :-------------------------------- |
| `Conventions` | `fstest.TestFS` walks each listing and opens every name it finds            | fails                             |
| `Listings`    | every name `ReadDir` reports is opened by `Open`                            | fails                             |
| `EntryInfo`   | `Lstat` answers for every listed name and matches that entry's `Info()`     | passes, because both are masked   |
| `ReadPaths`   | `ReadFile` answers for every regular entry a listing reports                | fails                             |

So `restrict` is exactly the shape the shared suite rejects today. That is not
an argument against it — the suite encodes "a file system's read paths agree
with each other", and a restricting driver replaces that invariant with a
different one: the policy is what refuses, and it refuses the same way in every
operation. The suite has to be told that a driver restricts, and that is an
owner decision rather than something this PR can settle by itself:

1. An `Args` field in `drivers/testing` — `Restricting bool`, say — that drops
   the reachability assertions for the driver that sets it and keeps the rest.
2. A companion suite, `drivers/testing/restrictfs.go`, asserting the invariants
   a restricting driver actually makes: every listed name is either reachable or
   refused with `ErrProtected`; a restricted entry reports the same masked info
   through `ReadDir`, `Stat` and `Lstat`; and every write path refuses it.
3. Testing the driver only over a fixture with no restricted entry, which does
   not work: `fstest.TestFS` and `Listings` walk the whole tree, not just the
   root.

Option 2 is the better long-term shape, because it tests the guarantee the
driver makes instead of excusing it from one that it does not. The evidence that
this needs ruling, and not a workaround: PR #691, which implements the first
revision's read-only shape, fails `ReadConsistency` on current `main` —
`Open("keys/server.key") = … protected by the key material policy, want the
name listed by ReadDir("keys") to open` in `Listings`, the matching `Lstat`
failure in `EntryInfo`, and `fstest.TestFS() = …` in `Conventions`. That is the
same conflict, arriving from the other direction.

### Matching by name

A new internal package holds the matcher, because the same rule is needed by
the content guard of #513 and by a declarative policy in #514, and neither of
them should reach into a driver package for it.

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

Patterns are validated when the policy is built, so `New` returns an error for
a malformed pattern instead of silently restricting nothing. Failing loudly is
the price of a two-value constructor; the alternative of panicking is what
`RegisterDecorator` does for a duplicate name, and a user-authored policy string
is not a programmer error.

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
Content detection is what makes a policy name-independent, so it is part of the
design and not a follow-up. It is about sensitive content in a file rather than
key formats alone: a policy says which content it objects to, and the shipped
policy objects to private-key material.

```go
// pemSensitive reports whether chunk holds a PEM block whose label contains
// PRIVATE KEY, which is how OpenSSH, PKCS#1, PKCS#8, PGP, SEC1 and encrypted
// keys announce themselves.
func pemSensitive(chunk []byte) bool

// markerSensitive reports the offset in chunk where a configured marker starts,
// and -1 when no marker appears.
func markerSensitive(chunk []byte, markers []string) int
```

PEM is handled structurally rather than as an exact-label list, so an unusual
label such as `-----BEGIN X9.42 ENCRYPTED PRIVATE KEY-----` is caught without
being enumerated. The markers are exact byte sequences for sensitive content
that has no PEM wrapper, and they are the seam a policy for other sensitive
content uses: the shipped list is a starting point, not a closed set.

```go
// DefaultMarkers are byte sequences that mark sensitive content wherever it
// appears in a file. PEM private-key blocks are detected structurally and are
// not listed here.
var DefaultMarkers = []string{
	"AGE-SECRET-KEY-",
	"\"private_key\"",
}
```

Detection runs on the bytes a read returns, in chunks, for as long as the
handle is read. When a chunk holds sensitive content, the read that produced it
stops at the match instead of delivering it:

```go
// guardFile hands the caller the inner's bytes until the policy finds sensitive
// content in them, and fails the read there. read counts the bytes already
// handed over, which is where the next chunk begins.
type guardFile struct {
	types.File
	policy  Policy
	read    int64
	stopped bool
}
```

`guardFile.Read` delegates, scans what came back, and returns an `ErrProtected`
`*fs.PathError` whose `Op` is `read`: the bytes before the match are handed
over, the matching bytes and everything after them are not. A chunk boundary
cannot hide a match, because a scan carries the last `len(marker)-1` bytes of
the previous chunk into the next one. A file whose name says nothing and whose
content does is therefore broken in the middle of a read rather than handed
over whole.

The consequences are stated rather than buried:

- A content-detected file opens. `Open` cannot know what a file holds without
  reading it, so `Open` succeeds and the first read that reaches the material
  fails. `ReadFile` fails whole, because it reads the file before returning.
- `Stat`, `Lstat` and `ReadDir` cannot know either, and finding out in `ReadDir`
  costs one read per entry per listing. So an entry the *name* matched is
  masked, and an entry only its content betrays keeps its real size and
  mod-time until a read breaks. Masking every entry is not an option: the shared
  read-consistency suite requires a stat's size to equal the bytes `ReadFile`
  returns.
- A handle that scans is a wrapped handle, so whatever the inner handle offered
  — `io.ReaderAt`, for one — is reached through the scan, and the wrapper has to
  keep a `Seek` from moving the position the scan counts. The cost is a scan per
  read plus one indirection on every file the guard opens; the benchmark in
  Testing measures it.

The default chunk is `DefaultChunkBytes`, 8 KiB: PEM and age markers sit at
offset 0, a JWK or service-account document puts its private field early, and 8
KiB bounds the look-back a scan carries. A negative `ChunkBytes` disables
content detection, which is the documented way to get name-only enforcement.
Raising `ChunkBytes` does not make detection more correct, only costlier per
read, because a scan never buffers more than one chunk.

Rejected:

- Name-only enforcement. It fails on exactly the cases the issue names ("files
  that contain keys"), and a renamed or linked key is the interesting case.
- Sniffing during `ReadDir`. It would make a content-detected entry maskable,
  and so make `Stat` and `Read` agree about it, at the cost of one read per
  entry per listing. Rejected, and left as an open decision.
- Buffering a whole file on open to decide. Correct and simple, and the cost is
  unbounded: the guard would hold a copy of the largest file it is protecting.
- A content index or cache of detection results, keyed by path or content hash.
  That is a persistent state machine in its own right (the shape of
  `simplecachefs`), it has to be invalidated on every write, and it becomes a
  second source of truth about what is restricted.
- Sniffing only when the name looks plausible. That is name matching with extra
  steps, and gives up the name-independence that justifies the read.

### Errors

A refusal is a `*fs.PathError` built by `ufserrors.NewPathError`, whose `Err`
wraps both the file-system sentinel and the policy reason:

```go
// ErrProtected names the reason a restricting file system refused: the path is
// restricted by its access policy. It unwraps to fs.ErrPermission.
var ErrProtected = errors.New("restricted by the access policy")
```

`errors.Is(err, fs.ErrPermission)` and `errors.Is(err, ErrProtected)` both
hold. The `Op` field carries the operation being refused — `open`, `readfile`,
`read`, `remove`, `rename`, `copyfile` — matching the `Op` strings
`pathutil.Validate` and `readonlyfs` already use, so a refusal looks like any
other ufs error and a `host` mount can map it to `EACCES`. A read stopped by
content detection is the same error with `Op` `read`, after the bytes before the
match were already handed over, so a caller sees a partial read and then a
refusal rather than a refusal with nothing.

Rejected:

- Plain `fs.ErrPermission` with no policy sentinel. A caller cannot tell a
  restriction from a backend that genuinely cannot read the file, and #548's
  audit is moving the repo toward named sentinels, not away from them.
- `fs.ErrNotExist` for a restricted entry, which is what the first revision's
  `hide` mode reported. The mode the review asked for keeps the presence
  visible, so an error claiming the entry is absent would undo it everywhere
  else in the API.
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
// Package restrictfs wraps a file system so that the entries its policy marks
// as sensitive stay visible and cannot be read or changed. The restriction is
// in-process: a caller that reaches the same bytes another way is not
// restricted by it.
package restrictfs

// DefaultChunkBytes is how many bytes a content scan looks at at a time.
const DefaultChunkBytes = 8192

// Policy decides which entries a restricting file system blocks and what a
// blocked entry reports. Implementations must be safe for concurrent use.
type Policy interface {
	// Restricted reports whether the entry at the validated, slash-separated
	// path name is restricted.
	Restricted(name string) bool
	// Sensitive inspects the chunk that ends at offset and reports the offset
	// in the stream where a reader must stop, or -1 when the chunk holds
	// nothing sensitive.
	Sensitive(chunk []byte, offset int64) int64
	// Masked returns the fs.FileInfo a restricted entry reports in place of
	// the inner's: its name and mode, with a zero size and mod-time.
	Masked(info fs.FileInfo) fs.FileInfo
}

// Options configures the policy. The zero value selects the security-sensitive
// file policy: DefaultPatterns by name, DefaultMarkers and the structural PEM
// check by content.
type Options struct {
	// Policy replaces the whole policy. When it is set, the fields below are
	// ignored.
	Policy Policy `yaml:"-"`
	// Patterns are path.Match patterns. A name is restricted when a pattern
	// matches the whole path or any single component of it, so ".ssh"
	// restricts everything under a .ssh directory. Empty selects
	// DefaultPatterns.
	Patterns []string `yaml:"patterns"`
	// Markers are byte sequences that mark sensitive content wherever they
	// appear in a file, in addition to the structural PEM check. Empty
	// selects DefaultMarkers.
	Markers []string `yaml:"markers"`
	// ChunkBytes bounds how many bytes a content scan reads at a time. Zero
	// selects DefaultChunkBytes, a negative value disables content detection.
	ChunkBytes int `yaml:"chunkBytes"`
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

// ErrProtected names the reason a restricting file system refused a path.
var ErrProtected = errors.New("restricted by the access policy")

// SensitiveFilePolicy is the shipped policy: a name catalog and a content
// catalog. It is what the zero Options selects.
type SensitiveFilePolicy struct { /* patterns, markers, chunk bytes */ }

// NewSensitiveFilePolicy returns the shipped policy over patterns and markers,
// validating each pattern so a policy that does not compile fails when it is
// built rather than restricting nothing. Empty arguments select the defaults.
func NewSensitiveFilePolicy(patterns, markers []string) (*SensitiveFilePolicy, error)

// New returns a wrapper around inner that restricts the entries its policy
// marks. It reports an error for a malformed pattern.
func New(inner ufs.FS, opts Options) (ufs.FS, error)
```

`String()` returns JSON through `types.Description` and `types.Nested`, as the
description policy requires, and names the driver, its policy, and the pattern
and marker counts so an operator can see the policy is present without dumping
it.

`Policy` is an interface rather than a struct of catalogs so that #514 can give
a mount a declarative rule set behind the same seam, and so a caller whose
sensitive content is not key material — a customer data export, a build secret —
writes a `Policy` instead of a second wrapper.

## Bypass surfaces

Each of these is reachable today; what closes it, or why it stays open, is
stated rather than assumed.

1. **Not using the driver, or unwrapping it.** `types.InnerFS()` and
   `types.InnermostFS` hand back the unrestricted file system, and the caller
   who passed `inner` to `New` still holds it. The driver restricts what a
   caller *handed the driver* can do; a caller that keeps or recovers the inner
   handle is outside its reach. `InnerFS()` returning the raw inner is required
   by the `types.WrapperFS` contract and by PR #684's ruling on unwrapping, so
   the driver cannot close this without breaking that contract; the package
   documentation states the limit instead.
2. **A mounted sub-file system.** The driver covers the tree it wraps. Placed
   over a `nestFS` root it covers every mount below it, which is why the
   constructor takes a `ufs.FS`; placed *below* a mount point it guards only
   that mount, and a sibling mount reaches the same bytes without passing
   through it. The rule the documentation gives: wrap the tree at or above the
   point where the sensitive files are mounted, and a test seeds a nest with a
   mount under a restricted directory to pin that reads through that mount are
   refused.
3. **Path escapes.** `pathutil.Validate` runs first in every method, so
   absolute paths, `..`, `.`, trailing slashes and the eleven names in
   `drivers/testing`'s shared invalid-path list fail with `fs.ErrInvalid`
   before any policy decision, exactly as `readwrapfs` does. There is no
   policy-relevant path spelling left to exploit.
4. **Symbolic links inside the wrapped tree.** Name matching sees only the link
   path, so `notes/link -> keys/id_ed25519` is not caught by a name rule.
   Content detection *does* catch it, because detection runs on the bytes the
   opened path actually returns, which is the main reason it is in the design —
   though it catches it as a read that breaks partway, not as an `Open` that is
   refused. A marker-less binary key behind a clean name is not caught;
   resolving the link target and re-checking it is an open decision.
5. **Metadata that is still visible.** A restricted entry reports its name and
   its mode type in `ReadDir`, `Stat` and `Lstat`, and `URI`, `DeviceInfo` and
   `IsMountedArchiveDir` confirm the path exists. That is the mode the review
   asked for, so the presence is not a leak to be closed; the size and mod-time
   are masked, which is what removes the content-shaped part of it. What stays
   open is an entry only content detection would catch: `Stat` and `ReadDir`
   cannot know what it holds, so it keeps its real size and mod-time until a
   read breaks. Through a `host` mount the masked size is what `stat(2)`
   reports, because `host/fuse_linux.go` builds its attributes with
   `fuseAttrFromFileInfo` over what `Stat` returns (`:193` for `Getattr`, `:210`
   for `Lookup`).
6. **Copy or rename into an unrestricted name.** Closed by the surface: because
   the driver is a `ufs.FS`, it has `CopyFile` and `RenameFile` to police, and
   it refuses when *either* endpoint is restricted. Checking only the
   destination is the bug that turns a copy into a read.
7. **A cache above the driver.** If a caching layer (`simplecachefs`, #613)
   sits outside it, key content already fetched stays reachable through that
   layer. The documented ordering rule is that the driver goes outermost on the
   access path; decorator ordering is an open decision.
8. **Change notifications.** A watcher on an ancestor of a restricted subtree
   reports that subtree's names, and the driver implements `types.Watcher`
   rather than leaving notifications to the inner, so it has to filter them:
   a notification naming a restricted path is dropped. Dropping is a decision
   with a cost — a caller watching a parent directory does not learn that a
   restricted file changed — and it is the cost of a mode whose whole point is
   that the content is not the caller's to learn.
9. **Outside ufs entirely.** `os.Open` on the backing directory, or any other
   process with access to it, is unaffected. A `host` mount handed the
   restricted file system does get the restriction — `host.Mount` takes a
   `types.ReadFS` and reads through it, and `host/fuse_linux.go` maps
   `fs.ErrPermission`, which `ErrProtected` unwraps to, to `EACCES`.

## Changes

Phase 1 (the implementation PR this spec precedes):

| Path                                                     | Change                                                                                                                                                                                                                                     |
| :------------------------------------------------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/patternutil/patternutil.go`                    | New. `Match` and `Validate` over `path.Match`, whole-path and per-component. Written already in PR #691 and reusable as it stands.                                                                                                         |
| `internal/patternutil/patternutil_test.go`               | New. Component and whole-path matching, malformed pattern reporting, empty list matching nothing. Same as PR #691.                                                                                                                         |
| `drivers/wrappers/restrictfs/restrictfs.go`              | New. `Policy`, `Options`, `SensitiveFilePolicy`, the defaults, `New`, and `restrictFS` with every method in the enforcement table plus the interface assertions.                                                                           |
| `drivers/wrappers/restrictfs/restrictfs_test.go`         | New. Policy tables, a refusal case per method, masked metadata for a restricted entry, and pattern validation errors.                                                                                                                      |
| `drivers/wrappers/restrictfs/restrictfs_driver_test.go`  | New. `ufsdriversTesting.FS(t, ufsdriversTesting.Args{}, …)` over a seeded nest; reachability groups gated on the owner's ruling in Testing.                                                                                              |
| `docs/superpowers/specs/2026-11-16-keyguardfs-design.md` | This file.                                                                                                                                                                                                                                 |

No existing file changes in phase 1: the driver registers nothing, so
`drivers/all/all.go` is untouched, and no caller is modified. `host` needs no
change — it already builds its attributes from the `fs.FileInfo` the driver
returns.

Later phases (each its own PR, see Implementation plan):

| Path                                              | Phase | Change                                                                                                                                                |
| :------------------------------------------------ | :---- | :---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `drivers/wrappers/restrictfs/handle.go`           | 2     | New. `guardFile`, the chunked scan, `pemSensitive` and `markerSensitive`; adds `Markers` and `ChunkBytes` to `Options`.                               |
| `drivers/wrappers/restrictfs/handle_test.go`      | 2     | New. PEM labels, marker placement across a chunk boundary, the byte at which a read breaks, and cost benchmarks.                                      |
| `drivers/wrappers/restrictfs/enumerate.go`        | 3     | New. `Glob`, `ListFilenames`, `ForEachFilenameIter`, `ForEachFileInfoIter` and `Watcher` with policy filtering.                                       |
| `drivers/testing/restrictfs.go`                   | 3     | New. The restricted-view conformance suite, in place of excusing the driver from `readConsistency`. Owner decision.                                   |
| `drivers/decorators/restrictfs/`                  | 4     | New. YAML `Options`, `init()` registering the `restrict` decorator, delegating to `restrictfs`. Needs #513's decorator signature widened to `ufs.FS`. |
| `drivers/all/all.go`                              | 4     | Adds the blank import and the `restrict` entry in the package comment's decorator list.                                                               |

### Relation to PR #691

PR #691 (`feat/keyguardfs-name-policy`) implements the first revision of this
spec: `drivers/wrappers/keyguardfs` over a `types.ReadFS`, with `refuse` and
`hide` modes and no content detection. This design supersedes that shape, so the
owner has to rule on what happens to it — rework #691 into `restrictfs`, or
merge and replace it. `internal/patternutil` and its tests carry over either
way; `keyguardfs.go` does not, because its surface, its error for a restricted
entry, and its presentation modes are all different.

## Testing

- `restrictfs_driver_test.go` runs the shared conformance funnel the way the
  `readwrapfs` and `readonlyfs` driver tests do. Because the driver is a
  `ufs.FS`, the funnel takes the `ufs.FS` branch
  (`drivers/testing/testing.go:139`) and runs `fsInternal`, so the write-side
  suites run here too, and the fixture has to be a nest — `ufs.New` of a seeded
  `mem://` tree, wrapped in the driver — not an empty one. The fixture holds a
  `keys/` directory with a PEM private key and a `server.key`, a `.env` file
  with a marker inside it, `notes.txt` with a PEM block well past the first
  chunk (the documented miss), a marker-free binary blob, and a plain
  subdirectory. The content assertions in the shared suites are only meaningful
  with non-empty fixtures, so the seed matters.
- Which shared groups pass today, and which need an owner decision, is the
  table in "What the read-consistency suite says about this". Until that
  decision lands, the driver test runs the funnel with the three reachability
  groups skipped and says why in a comment, rather than pretending they pass.
  `wrapperFS`'s `innerListsSameEntries` passes unchanged, because `restrict`
  filters nothing.
- Refusal coverage, in `restrictfs_test.go`, one case per method: `Open`,
  `OpenFile`, `ReadFile`, `ReadLink`, `CreateFile`, `MkdirAll`, `Remove`,
  `RemoveAll`, `RenameFile` (both endpoints), `CopyFile` (both endpoints),
  `Glob` and the iterators, with `errors.Is` checks against `fs.ErrPermission`
  and `ErrProtected`. A restricted entry's `Stat`, `Lstat` and `DirEntry.Info()`
  are asserted equal to each other and to report size zero. Each method
  validates the path first, so the shared invalid-path names must still produce
  `fs.ErrInvalid`, not a refusal — a policy that swallows `ErrInvalid` is a
  contract break.
- Matching coverage: whole-path patterns, component patterns (`.ssh` covering
  `.ssh/id_ed25519`), a pattern that matches nothing, a malformed pattern
  rejected by `New`, content detection on a PEM key, on an unusual PEM label,
  on a marker, off when `ChunkBytes` is negative, and a marker that straddles a
  chunk boundary.
- Read-break coverage: a file whose name is clean and whose sensitive bytes sit
  at offset 0, at a chunk boundary, and past the first chunk. The assertions are
  the number of bytes returned before the failure and that the failure is
  `ErrProtected` with `Op` `read`. A clean file must read back byte-identical
  through the scanning handle in one read, in many small reads, and via
  `io.ReadAll`, which is what keeps the scan from eating or duplicating bytes.
- Cost coverage: benchmarks of `Open` and of a 1 MiB `ReadAll` with detection
  off, on a clean file, and on a sensitive file, added with phase 2 and reported
  in that PR. A change that adds per-read cost gets measured numbers, not an
  argument.
- Every new test is checked by breaking the implementation: delete the
  `ReadFile` guard and its case must fail; mask only in `Stat` and the
  `DirEntry` case must fail; drop the scan's look-back and the straddling-marker
  case must fail. The PR body states which mutations were tried.
- Locally: `go test ./drivers/wrappers/restrictfs/ ./internal/patternutil/`,
  then `make testassets` once and a full `go test ./...`, and `make lint-go`
  before pushing. `make lint-vuln` is not run here because the local toolchain
  makes it red on every repo. CI's `make presubmit` on Linux and Windows is
  authoritative.
- Not provable locally: `host` mount behavior (no `/dev/fuse` here), so the
  `EACCES` claim in bypass surface 9 is inference from `host.Mount` reading
  through the `types.ReadFS` it is given and `fuseErrno` mapping
  `fs.ErrPermission` to `EACCES`, not a mount-verified fact.

## Known limitations

- A read that breaks leaves the caller holding the leading bytes it already
  got, so a sensitive file whose beginning is innocuous is partly delivered.
  Detection cannot be retroactive.
- Detection is a scan for known shapes, so it is fooled in both directions. A
  file that merely quotes `-----BEGIN OPENSSH PRIVATE KEY-----` — a README, a
  test fixture, this spec's own examples — breaks a read. A raw 32-byte Ed25519
  seed, a binary PKCS#12 blob, and an encrypted keystore with no recognizable
  header are restricted by name only.
- In-process only. Unwrapping, not using the driver, or reading the backing
  directory from another process defeats it entirely.
- A restricted entry keeps its name and its mode visible in `ReadDir`, `Stat`
  and `Lstat`, which is the requested behavior. An entry that only content
  detection would catch keeps its real size and mod-time until a read breaks.
- A restricted entry that a caller can list but not open contradicts the shared
  read-consistency suite, as the table in "What the read-consistency suite says
  about this" spells out. Until the owner rules on it, the driver cannot run
  those groups.
- Every read through the driver pays a scan plus one indirection, on sensitive
  files and on clean ones alike.
- Symlink targets are not resolved, so a marker-less key behind a clean name is
  reachable.
- Patterns are `path.Match`: no `**`, no case-insensitivity, no brace
  expansion, and the semantics are case-sensitive exactly as every driver's
  `Glob` is.

## Out of scope

- The `agentbox` and ADK tool-layer integration, and `posixcli`.
- A declarative rule language, rule composition, or per-mount policy merging
  (#514). `Policy` is the seam such a language plugs into, not a first attempt
  at one.
- Redaction, DLP on writes, and key rotation or management.
- Cross-process or OS-level enforcement, mount namespaces, and file permissions.
- Parsing, validating, or evaluating keys and certificates.
- Any change to the `ufs.NewDecorator` signature or to decorator ordering.

## Implementation plan

| Phase | PR                                                                                                                                        | Deliberately cut                                                                                        |
| :---- | :---------------------------------------------------------------------------------------------------------------------------------------- | :------------------------------------------------------------------------------------------------------ |
| 0     | This spec.                                                                                                                                | Any code.                                                                                               |
| 1     | `internal/patternutil` plus `restrictfs` over `ufs.FS`: the name policy, a refusal per read and write method, masked metadata, and tests. | Content detection, so the first PR is reviewable as a policy surface and the cost question is deferred. |
| 2     | Content detection: the scanning handle, PEM, markers, benchmarks.                                                                         | Decorator registration.                                                                                 |
| 3     | `Glob`, the iterators and `Watcher` with filtering, plus the restricted-view conformance suite in `drivers/testing`.                      | Anything the owner has not ruled on yet.                                                                |
| 4     | `drivers/decorators/restrictfs` with YAML options and registration in `drivers/all/all.go`.                                               | Any change to decorator ordering or to the decorator signature.                                         |

Phase 1 may be split further into `internal/patternutil` and the driver if the
first PR reads as too large. Each PR is one squash commit on top of latest
`main`.

## Open decisions

1. How the shared conformance suite treats a restricting driver: an `Args` field
   that drops the reachability assertions, a companion
   `drivers/testing/restrictfs.go` suite, or a driver that never runs them. The
   rest of this design waits on this one, because it decides what "verified"
   means for the driver.
2. What happens to PR #691, which implements the first revision's shape: rework
   it into `restrictfs`, or land it and replace it.
3. Which catalog is default. Whether certificate and CA bundles (`.crt`,
   `.cer`, `.pem` bundles) and `*.pub` are restricted, and whether directory
   names (`keys`, `credentials`) belong in the defaults or stay opt-in. The
   issue names cert/CA bundles; those files hold public keys, which argues for
   restricting them for integrity rather than secrecy, and that is a product
   call.
4. Is the matched rule disclosed in the error? Naming the pattern helps an
   operator author a policy and tells an agent what to stop trying; it also
   hands the caller information about the policy shape.
5. Should the driver annotate the inner's URI the way `readwrapfs` and
   `readonlyfs` add `ro=true`, or leave it untouched? The URI suite in PR #679
   pins what a wrapper may change, and this driver changes no data identity.
6. Should the write surface refuse or allow? The spec refuses `Remove` and
   `RenameFile` over a restricted name. A caller that legitimately rotates or
   deletes a key through the same mount is blocked by that, and the alternative
   is a policy that lets a key be deleted but not read, which may be backwards.
7. Should content detection break a read at the match, as specified, or refuse
   the whole file once it has matched? Refusing retroactively means withholding
   bytes a caller already holds, which the spec rejects; a buffered variant is
   the alternative and it costs a full chunk of memory per open file.
8. Should symlink targets be resolved and re-checked at open, and at what cost
   for file systems whose `ReadLink` is expensive?
9. Where does the driver sit relative to caches and to `readOnly` in decorator
   ordering, once phase 4 registers it? This is the ordering half of #514.
