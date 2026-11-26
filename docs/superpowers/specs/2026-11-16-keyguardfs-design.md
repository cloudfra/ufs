# Restricted-Access Wrapper (RestrictFS) — Design Spec

**Date:** 2026-11-16
**Issue:** <https://github.com/cloudfra/ufs/issues/689>
**Status:** Proposed, revised after review (2026-11-22) and against `main` at `d94c72e` (2026-11-26)

## Goal

Add `drivers/wrappers/restrictfs`: a wrapper that keeps the entries its policy marks as sensitive
visible and makes them untouchable. A marked entry keeps its name and its type in every listing,
`Stat` and `Lstat` answer for it with zero values, and every operation that would reach through it
fails — `Open`, `OpenFile`, `ReadFile`, `ReadLink`, `Create`, `MkdirAll`, `Remove`, `RemoveAll`,
`Rename` and `CopyFile` at either endpoint. None of them reports `fs.ErrNotExist`: the entry is
there, that is the mode, and an error calling it absent contradicts the listings, stats and URIs the
same wrapper answers.

The policy is the configurable part; the wrapper is general, and ships one: `SensitiveFilePolicy`,
which marks private keys, keystores, `.env` files, PKCS#12 bundles and SSH keys by name, and marks
by content any file holding sensitive material. Issue #689 asks to restrict that catalog; other
sensitive content configures the same wrapper rather than adding a second, which is what the owner
asked for on 2026-11-26: `restrictfs` "with separate policies".

## What merged while this spec waited

`keyguardfs` — the older shape of this idea — merged on 2026-11-25 as PR #691 (commit `8052760`),
and it is narrower than what this spec asks for:

|              | `keyguardfs` as merged                                                                                                                                                                                                 | this spec                                                                             |
| :----------- | :--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :------------------------------------------------------------------------------------ |
| surface      | `New(inner types.ReadFS, opts Options) (types.ReadFS, error)` (`drivers/wrappers/keyguardfs/keyguardfs.go:108`)                                                                                                        | `types.FS`, so the write side is policed too                                          |
| presentation | two modes, `refuse` (`drivers/wrappers/keyguardfs/keyguardfs.go:50`) and `hide` (`:53`); in `refuse`, `Stat` and `Lstat` of a protected name are errors (`drivers/wrappers/keyguardfs/keyguardfs_statfs.go:27`, `:32`) | one mode, `restrict`, which answers with zero values rather than refusing to describe |
| policy       | name patterns through `internal/patternutil` only, no content scan                                                                                                                                                     | a name policy and a content policy                                                    |

The owner's ruling on this PR (2026-11-26) is that `keyguardfs` shipped to unblock users, that it is
likely to be deleted quickly in favour of `restrictfs` with separate policies, and that this design
should use it for inspiration without tying itself to it. What follows from that is written down
rather than assumed:

- This spec is the design of the driver the owner asked for, not a description of `keyguardfs`.
  Nothing here changes `keyguardfs`; deleting, renaming or extending it is an owner action, and it
  is open decision 1.
- Its shipped parts are worth reusing rather than re-inventing: `DefaultPatterns`
  (`drivers/wrappers/keyguardfs/keyguardfs.go:59`) is the name catalog §Names describes, `ErrProtected`
  and its `Is` method (`keyguardfs.go:71`, `:84`) are the error shape §Errors describes, and the
  package layout — one file per interface, `keyguardfs_<interface>.go` — is the layout §Changes
  follows.
- Its semantics are not the mode that was asked for. `refuse` lists a protected name and refuses to
  describe it; `hide` removes it from listings; `restrict` keeps the name and describes it with zero
  values. §Enforcement is written against the ruling, not against the merged code, and that is why
  the merged driver is inspiration and not a base to build on.

## Surface

`types.FS` (`drivers/types/fs.go:22`) is `WriteFS` — `ReadFS` plus `Remove`, `RemoveAll`, `Create`
and `MkdirAll` (`drivers/types/writefs.go:19`) — plus `OpenFile` (`drivers/types/openfilefs.go:24`),
`Rename` (`drivers/types/renamefs.go:36`) and `Release` (`drivers/types/releasefs.go:44`). `ufs.FS`
is `interface { types.FS }` (`ufs.go:58`); the composite is named in `drivers/types`, so a
`drivers/` package names it without importing the root. The wrapper takes and returns that composite,
so one guard over the tree `ufs.New` builds covers every mounted layer. It is not the read-side
`types.ReadFS` `keyguardfs` takes, said once: the protected tree is made of `types.FS` layers, so a
read-side guard cannot wrap it, and one that refused only reads would still let a caller delete what
it cannot read.

```go
// New returns a wrapper around inner that restricts the entries its policy marks.
func New(inner types.FS, opts Options) (types.FS, error)

type restrictFS struct {
	inner  types.FS
	policy Policy
}

var _ types.FS = (*restrictFS)(nil)
```

`inner` is a field, never an embedded interface: embedding promotes the inner's whole surface, and a
promoted method would skip the policy entirely — which is the trade `readonlyfs` accepts when it
wraps (`drivers/decorators/readonlyfs/readonlyfs.go:37`, `:100`). Every method is written here,
optional interfaces and enumerators included, rather than left to `drivers/polyfills`: those are
probes, calling the file system's own method when it has one (`drivers/polyfills/copyfilefs.go:63`,
`drivers/polyfills/renamefs.go:72`), and `ops` prefers a native method to its own fallback
(`ops/copy.go:31`, `ops/rename.go:38`). Forwarding an interface is therefore a bypass — `CopyFile`
handed to the inner never consults the policy — and omitting one is no safer: a file system is
allowed to write its own path instead of taking the polyfilled one, and `nestFS` already does exactly
that, replacing the directory handle `polyfills.OpenFile` returned because the polyfill "has no
mounts to look at" (`nestfs_openfilefs.go:92`, `:96`, `:100`). A policy that lives only in the
methods a fallback happens to call disappears the day someone writes a fast path.

| Surface                                                                                                  | Written here                        | Why                                                                                                                                                                     |
| :------------------------------------------------------------------------------------------------------- | :---------------------------------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `OpenFile`, `Remove`, `RemoveAll`, `Create`, `MkdirAll`, `Rename`, `Release`                             | yes, they are in `types.FS`         | each can destroy, move or replace restricted material                                                                                                                   |
| `fs.GlobFS`                                                                                              | yes                                 | it yields names from a pattern the policy never saw, and the layers above answer it natively (`nestfs.go:61`, `drivers/core/localfs.go:50`, `drivers/core/memfs.go:47`) |
| `types.ListFilenames`, `types.ForEachFilenameIter`, `types.ForEachFileInfoIter`                          | yes                                 | the enumeration path a file system takes when it has one                                                                                                                |
| `types.CopyFileFS`                                                                                       | yes                                 | `ops.Copy` prefers it over the open-and-write path (`ops/copy.go:31`); `Copy` itself is still a commented TODO in `types.FS` (`drivers/types/fs.go:34`)                 |
| `types.Watcher` (`drivers/types/notify.go:45`)                                                           | yes, restricted names dropped       | implement none and a restricted mount loses notifications entirely                                                                                                      |
| `types.WrapperFS`, `ComparableFS`, `URIGetFS`, `DeviceInfoGet`, `MountedArchiveDirFS`, `String`, `Close` | yes, forwarded, masked or annotated | they report presence and identity, not content; `InnerFS()` returns the raw inner, as the contract requires (`drivers/types/wrapperfs.go:26`)                           |
| `types.MkdirFS`, `types.BatchCreateFS`, `types.ClosestParentFS`, `types.ExternalPathGetFS`               | no, deliberately                    | a batch create that skipped the per-path check would be the one write path with no guard; callers fall back through the rows above                                      |

The wrapper is built by hand until the decorator signature widens: registered decorators are typed
over `types.WriteFS` in `Decorator.CreateFunc` (`register.go:51`) and in `NewDecorator`
(`register.go:98`), `ufs.New` builds decorations *under* the nest (`openNestFS` at `construct.go:290` calls
`newDecoratedFS` at `construct.go:540`, and `makeNestFS` puts the nest on top of that
result), and a mount inside a nest is always another
`*nestFS` (`nestfs.go:95`, `addMount` at `nestfs.go:385`). So a registered decorator cannot police a
whole nest tree today, whatever its signature; the caller wraps the handle `ufs.New` returned, and
§Changes phase 4 lists what has to change first. `String()` returns JSON built with
`types.Description` and `types.Nested` (`drivers/types/stringerfs.go:43`), naming the policy by its
pattern and marker counts, so an operator can see that a restriction is present without dumping the
policy.

Rejected names, one clause each: `keyguardfs` is merged (`drivers/wrappers/keyguardfs/`), and it
names one policy's catalog rather than the wrapper; `secretfs` promises a general secrets scanner
while the catalog also marks certificate bundles; `redactfs` promises rewriting; `denyfs` and
`policyfs` a rule language; `keysfs` reads as a file system *of* keys.

## Enforcement

One mode, `restrict`: the entry keeps its presence and loses its access. There is no second mode and
no presentation switch for a policy to set — the owner's ruling on 2026-11-26 was that a masked view
is not a thing of its own, since "depending on the mode it's either ErrPermission or 0 values for
thing like size and no Sys() result".

| Operation                                                                                     | Restricted entry                                                                                                         |
| :-------------------------------------------------------------------------------------------- | :----------------------------------------------------------------------------------------------------------------------- |
| `Open`, `OpenFile`                                                                            | `*fs.PathError` wrapping `ErrProtected`, which unwraps to `fs.ErrPermission`                                             |
| `ReadFile`, `ReadLink`                                                                        | refused, as `Open` — a link target is content, and reporting no target misdescribes what is there                        |
| `Stat`, `Lstat`, and a listed entry's `DirEntry.Info()`                                       | the inner's name and mode, `Size()` 0, the zero `ModTime()`, `Sys()` nil                                                 |
| `ReadDir`, the iterators, `Glob`                                                              | listed with that metadata; nothing is filtered out                                                                       |
| `Create`, `MkdirAll`                                                                          | refused, so restricted material cannot be replaced or buried                                                             |
| `Remove`, `RemoveAll`                                                                         | refused — a caller that cannot read a key is not free to delete it                                                       |
| `Rename`, `CopyFile`                                                                          | refused when either endpoint is restricted; checking only the destination is the bug that turns a rename into a copy-out |
| `Read` on an open handle                                                                      | forwarded until the content policy matches, then the read fails with `ErrProtected`                                      |
| `Release`, `Close`, `DeviceInfo`, `IsMountedArchiveDir`, `URI`, `String`, `InnerFS`, `IsSame` | forwarded, `String` naming the policy; they end a session, describe the mount, or follow the `types.WrapperFS` contract  |

### Zero values, not a masked type

The metadata a restricted entry reports is produced once, by the wrapper, from the inner's `name` and
`mode`: size zero, mod-time zero, `Sys()` nil. No `Masked` hook in `Policy`, no masking wrapper type
to configure, and no per-mode table — that is the whole rule. `Sys()` is nil rather than the inner's
because it is where backend-specific identity (inode, device, uid) is carried, and it costs nothing
to drop: `host` builds a `stat(2)` answer from size, mode and mod-time alone and never reads `Sys()`
(`host/fuse_linux.go:515`), in `Getattr` (`host/fuse_linux.go:194`, `:199`) and `Lookup`
(`host/fuse_linux.go:203`, `:210`).

The zero size also keeps a `stat` from reporting how many bytes of key material are on disk. Name and
mode stay because the requested behavior is "you can see the presence of the file but cannot access
it" (owner, 2026-11-22), and the same metadata is what `ReadDir` reports in its `fs.DirEntry`, which
is why `Stat`, `Lstat` and `DirEntry.Info()` must return values that agree with each other.

### Errors

A refusal is a `*fs.PathError` from `ufserrors.NewPathError` (`internal/ufserrors/ufserrors.go:31`)
whose `Err` wraps `ErrProtected` — a type with an `Is` method, so `errors.Is(err, ErrProtected)` and
`errors.Is(err, fs.ErrPermission)` both hold, and `Op` names the operation as elsewhere in ufs; that
is the same shape `keyguardfs` ships (`drivers/wrappers/keyguardfs/keyguardfs.go:71`, `:84`). A
`host` mount maps the refusal to `EACCES` (`host/fuse_linux.go:487`, `:494`); plain
`fs.ErrPermission` would not tell a restriction apart from a backend that genuinely cannot read.

Every method validates the path first (`internal/pathutil/pathutil.go:67`), so the names in the
shared invalid-path list (`drivers/testing/testing.go:46`) still report `fs.ErrInvalid`.

## Policy and core API

```go
// Policy decides which entries the wrapper blocks. It is an interface, not a struct of catalogs:
// content that is not key material needs only its own Policy. Implementations must be safe for
// concurrent use.
type Policy interface {
	// Restricted reports whether the entry at the validated path name is restricted.
	Restricted(name string) bool
	// Sensitive inspects the chunk ending at offset and reports the stream offset where
	// a reader must stop, or -1 for nothing sensitive.
	Sensitive(chunk []byte, offset int64) int64
}
```

```go
// DefaultPatterns is the shipped name catalog and DefaultMarkers the shipped content catalog
// other than PEM; the zero Options selects both.
var DefaultPatterns = []string{".env", ".env.*", "*.key", "*.pem", "*.p12", "*.pfx", "*.pkcs12",
	"*.jks", "*.keystore", "*.kdb", "*.kdbx", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
	"id_xmss", "id_sk_*", "credentials.json", "service-account*.json", ".ssh", "secrets"}
var DefaultMarkers = []string{"AGE-SECRET-KEY-", "\"private_key\""}

// Options configures the policy: the zero Options selects the shipped catalogs and a default chunk
// size; a negative ChunkBytes disables content detection.
type Options struct {
	Policy     Policy   `yaml:"-"`
	Patterns   []string `yaml:"patterns"`
	Markers    []string `yaml:"markers"`
	ChunkBytes int      `yaml:"chunkBytes"`
}

// NewSensitiveFilePolicy returns the shipped policy over patterns and markers, empty meaning the
// shipped defaults.
func NewSensitiveFilePolicy(patterns, markers []string) (*SensitiveFilePolicy, error)

// ErrProtected names why the wrapper refused: the path is restricted by its access policy.
var ErrProtected error = protectedError{}
```

`DefaultPatterns` is the catalog `keyguardfs` ships (`drivers/wrappers/keyguardfs/keyguardfs.go:59`),
repeated here because that driver is expected to go away; whichever survives, one list of those names
must. Whether it is the right default at all is open decision 3.

### Names: `internal/patternutil` is reused, not rewritten

The name half of the policy is `patternutil.Match(patterns, name)`, which matches a `path.Match`
pattern against the whole path or against any single component of it
(`internal/patternutil/patternutil.go:27`, `matchOne` at `:51`), so directory scoping comes for free
without a `**` extension: `.ssh` matches `.ssh/id_ed25519`, and `*.key` matches
`deploy/prod/api.key`. `patternutil.Validate` (`internal/patternutil/patternutil.go:39`) is called
when the policy is built, so a malformed pattern fails in `New` instead of restricting nothing —
which is what `keyguardfs.New` already does (`drivers/wrappers/keyguardfs/keyguardfs.go:108`).
`path.Match` is what every `Glob` on this tree already means
(`internal/globutil/globutil.go:30`), so no pattern means something other than what the same file
system's `Glob` would mean. This package exists: phase 1 adds no pattern matcher.

### Content and the read

Name matching fails on a key renamed to `notes.txt`, on a key copied to a scratch name, and on a key
behind a link whose name says nothing, so detection runs on the bytes a read returns. Two kinds: PEM
as a block whose label contains `PRIVATE KEY` — how OpenSSH, PKCS#1, PKCS#8 and PGP keys announce
themselves — and exact-byte markers for sensitive content with no wrapper format. File contents can be
a bare key-shaped token rather than a document: `y"a29."` is the example the owner gave on 2026-11-26,
a base64-ish secret with no header, and it is exactly the case where "the file isn't obvious", so the
read is where the guard sits.

Reads go through a `guardFile` that embeds the inner `types.File`, counts the bytes already handed
over, and fails the first read whose chunk the content policy matches, with `ErrProtected`. A chunk
boundary cannot hide a match: the scan carries the previous chunk's `len(marker)-1` trailing bytes for
the marker check and its unterminated trailing line for the PEM check, since a PEM label's length is
unbounded. `types.File` also carries `io.ReadAt` and `io.Seeker` (`drivers/types/file.go:38`), so a
`Seek` must not move the position the scan counts.

Two consequences follow. A content-detected file opens, because `Open` cannot know what a file holds;
the first read that reaches the material fails, and `ReadFile` fails whole. And `Stat`, `Lstat` and
`ReadDir` cannot know either — finding out in `ReadDir` would cost a read per entry per listing — so
an entry the *name* matched reports zero values, and one only its content betrays keeps its real size
until a read breaks. Zeroing every entry is not open to this design: the shared suite requires a
stat's size to equal the bytes `ReadFile` returns (`drivers/testing/readfs.go:211`).

Content detection is the part `keyguardfs` deliberately does not do — its package doc says the guard
is name patterns, and there is no scanner in that package — so it is new work here, and its
false-positive radius is a policy question, not an implementation detail: `y"a29."` is short enough to
appear inside ordinary JSON or a README, and the shipped default marker list is open decision 3.

## Conformance: the read-consistency suite

The `ReadConsistency` group in `drivers/testing` (`drivers/testing/readfs.go:46`) walks the whole
tree from its listings (`drivers/testing/readfs.go:82`) and asks that what is listed be reachable;
three of its four subgroups reject a restricted entry as designed:

| Subgroup      | What it asserts                                                                                           | Restricted entry               |
| :------------ | :-------------------------------------------------------------------------------------------------------- | :----------------------------- |
| `Conventions` | `fstest.TestFS` opens every name it finds (`drivers/testing/readfs.go:47`, `:55`)                         | fails                          |
| `Listings`    | every name `ReadDir` reports opens (`drivers/testing/readfs.go:59`, `:118`)                               | fails                          |
| `EntryInfo`   | `Lstat` answers for every listed name and matches that entry's `Info()` (`drivers/testing/readfs.go:65`)  | passes, both sides zero-filled |
| `ReadPaths`   | `ReadFile` answers for every regular listed entry (`drivers/testing/readfs.go:71`, `:192`)                | fails                          |

That is not an argument against the mode: the suite encodes "a file system's read paths agree with
each other", and this wrapper substitutes another invariant — the policy refuses, the same way in
every operation — so the suite has to be told that a wrapper restricts. Three things are true on
current `main`, and they narrow what any of the options means:

1. A `types.FS` wrapper is judged by the write suites, not the read ones. The funnel in
   `drivers/testing/testing.go` takes `Args.ReadOnly` first (`:156`), then the `ufs.FS` branch
   (`:160`) which runs `fsInternal` (`:177`), then `types.WriteFS` (`:164`). The fixture has to be a
   seeded nest, and the write-side `Conventions` group (`drivers/testing/writefs.go:219`) copies the
   shared test assets *into* the file system, so a policy that marks any asset name would also refuse
   the seeding that every write suite depends on.
2. `keyguardfs` already ran into this and worked around it rather than settling it: its driver test
   runs the shared funnel with `Args{ReadOnly: true}` over a guard whose pattern names nothing in the
   fixture, and pins what each mode does to a protected name in its own tests instead
   (`drivers/wrappers/keyguardfs/keyguardfs_driver_test.go:72`, `:78`, `:83`). A green `keyguardfs`
   is that interim state, not a resolved question.
3. Two of the three options below edit `drivers/testing/`, which the owner has ruled these PRs do not
   change (comment on PR #745, 2026-11-26). So this is the owner's decision in the strongest sense:
   the shape is proposed here, the edit cannot come from this series.

| Option                                                                                                                                                                                                                                                  | Assessment                                                                                                                                                |
| :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | :-------------------------------------------------------------------------------------------------------------------------------------------------------- |
| An `Args` field — `Restricting bool`, say — that drops the reachability assertions for the wrapper that sets it                                                                                                                                         | smallest change; `Args` carries only `ReadOnly` and `Bucket` today (`drivers/testing/testing.go:32`); an excuse switch, and it is easy to set by accident |
| A companion `drivers/testing/restrictfs.go` asserting what a restricting wrapper guarantees: every listed name reachable or refused with `ErrProtected`, the same zero-filled metadata through `ReadDir`, `Stat` and `Lstat`, every write path refusing | the better shape, because it tests the guarantee instead of excusing its absence                                                                          |
| Running the funnel only over a fixture with no restricted entry, which is what `keyguardfs` does now                                                                                                                                                    | proves delegation and path validation only; proves nothing about the restricted tree the wrapper exists for                                               |

## Bypass surfaces

1. **Not using the wrapper, or unwrapping it.** `InnerFS()` returns the raw inner, as the
   `types.WrapperFS` contract requires, and the caller that passed `inner` to `New` still holds it.
2. **Placement.** At or above where the sensitive files are mounted, one wrapper covers every layer
   below. Below a mount point, a sibling mount reaches the same bytes untouched: a nest routes an
   operation to its own mount map (`nestfs_openfilefs.go:52`) before the file system under it sees
   anything, and every mount value in that map is another `*nestFS` (`nestfs.go:95`, `:385`), so a
   guard placed under a nest is not on the path at all. The documented rule is that the wrapper goes
   outermost on the access path, and a test seeds a nest with a mount under a restricted directory to
   pin that reads through it fail.
3. **Links inside the wrapped tree.** Name matching sees only the link name, so
   `notes/link → keys/id_ed25519` escapes a name rule; content detection catches it as a broken read
   rather than a refused `Open`. A marker-less binary key behind a clean name escapes both.
4. **Copy or rename into an unrestricted name,** closed by refusing either endpoint.
5. **Notifications.** A watcher on an ancestor reports that subtree's names, so the wrapper
   implements `types.Watcher` and drops notifications naming a restricted path; a caller watching a
   parent then does not learn that a restricted file changed.
6. **Outside ufs.** A `host` mount handed the wrapper does get the restriction: `host.Mount` reads
   through the `types.ReadFS` it is given (`host/host.go:53`) and `fs.ErrPermission` becomes `EACCES`
   through `fuseErrno` (`host/fuse_linux.go:487`, `:494`) — inference, because there is no `/dev/fuse`
   in this sandbox.
7. **Metadata that stays visible** — name, mode type, `URI`, `DeviceInfo`, `IsMountedArchiveDir` — is
   the mode the review asked for rather than a leak to close.

## Changes and plan

Phase 1, the implementation PR this spec precedes, follows the layout `keyguardfs` and `readonlyfs`
use — one file per interface, `restrictfs_<interface>.go` — and does not build on `keyguardfs`, which
is expected to be deleted:

| Path                                                    | Change                                                                                                                                                                                       |
| :------------------------------------------------------ | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `drivers/wrappers/restrictfs/restrictfs.go`             | `Policy`, `Options`, `SensitiveFilePolicy`, the defaults, `New`, and `restrictFS` with the name policy, the zero-value metadata and the interface assertions                                 |
| `drivers/wrappers/restrictfs/restrictfs_<interface>.go` | one file per interface in the enforcement table, each consulting the policy, as in `drivers/wrappers/keyguardfs/`                                                                            |
| `drivers/wrappers/restrictfs/restrictfs_test.go`        | policy tables, one refusal case per method, the zero-filled metadata agreeing, pattern validation errors                                                                                     |
| `drivers/wrappers/restrictfs/restrictfs_driver_test.go` | the shared funnel over a seeded nest with a policy that names nothing in the fixture (the `keyguardfs` interim, `keyguardfs_driver_test.go:72`), plus the driver's own restricted-view tests |

Nothing existing changes in phase 1: `internal/patternutil` already exists, `restrictfs` is a new
package, the wrapper registers nothing so `drivers/all/all.go` is untouched, and `host` already
builds its attributes from the `fs.FileInfo` it is given.

1. `restrictfs` over `types.FS`: the name policy through `patternutil.Match`, a refusal per read and
   write method, zero-filled metadata with `Sys()` nil, and tests. Content detection is cut so its
   cost and its false-positive rate are settled separately.
2. Content detection: `restrictfs_handle.go` with the `guardFile`, the chunked scan, `pemSensitive`
   and `markerSensitive`, `Markers` and `ChunkBytes` in `Options`, and the cost benchmarks.
3. `Glob`, the iterators and `Watcher` with policy filtering, plus the restricted-view conformance
   suite once the owner rules on §Conformance.
4. `drivers/decorators/restrictfs`: YAML options and registration in `drivers/all/all.go`. This step
   needs the decorator signature to take `types.FS` and `uri.Params` rather than `types.WriteFS` and
   `any` (`register.go:51`), and it needs mount entries that can hold a decorated non-nest
   (`nestfs.go:95`); both are what the URI-parameter registrar spec (#696) plans, so phase 4 waits on
   that decision rather than inventing its own.

## Testing

- The shared conformance funnel, the way the `readwrapfs` and `readonlyfs` tests run it, over a nest:
  `ufs.New` around a seeded `mem://` tree, wrapped in the wrapper; as a `types.FS` the funnel takes
  the `ufs.FS` branch (`drivers/testing/testing.go:160`) and runs the write-side suites. The seed
  holds a `keys/` directory with a PEM private key and a `server.key`, a `.env` file with a marker
  inside it, a `notes.txt` whose PEM block sits past the first chunk, a marker-free binary blob and a
  plain subdirectory, all non-empty.
- Refusal coverage, one case per method: `Open`, `OpenFile`, `ReadFile`, `ReadLink`, `Create`,
  `MkdirAll`, `Remove`, `RemoveAll`, `Rename` and `CopyFile` at both endpoints, `Glob` and the
  iterators, each checked with `errors.Is` against `fs.ErrPermission` and `ErrProtected`. A
  restricted entry's `Stat`, `Lstat` and `DirEntry.Info()` are asserted equal to each other, with size
  zero, zero mod-time and nil `Sys()`, and the shared invalid-path names must still report
  `fs.ErrInvalid`.
- Matching and read-break coverage: whole-path and component patterns, a pattern matching nothing, a
  malformed pattern rejected by `New`, detection on a PEM key, on an unusual PEM label and on a
  marker, off when `ChunkBytes` is negative, and a marker or PEM header straddling a chunk boundary;
  and a clean name with sensitive bytes at offset 0, at a chunk boundary and past the first chunk,
  asserting the byte count handed over before the failure and that the failure is `ErrProtected`. A
  clean file must read back byte-identical in one read, in many small reads, and through `io.ReadAll`.
- Locally: `go test ./drivers/wrappers/restrictfs/ ./internal/patternutil/`, then `make testassets`
  once and `go test ./...`, and `make lint-go` before pushing; CI's `make presubmit` is authoritative.

## Limitations and out of scope

- In-process advice only. It is not a sandbox: a caller that never uses the wrapper, unwraps it, or
  reaches the same bytes through `os.Open` on the backing directory is not stopped.
- A read that breaks leaves the caller holding the leading bytes it already received, and detection
  is a scan for known shapes, fooled both ways: a README that quotes
  `-----BEGIN OPENSSH PRIVATE KEY-----` breaks a read, and a raw Ed25519 seed, a binary PKCS#12 blob
  or an encrypted keystore with no recognizable header is restricted by name only.
- Every read through the wrapper pays a scan plus one indirection.
- Out of scope: any change to `keyguardfs`, including deleting or renaming it (open decision 1);
  content rewriting or redaction, so no redacted or truncated view is ever returned; key generation,
  key parsing, encryption at rest, certificate validation and trust stores; a rule language, rule
  composition or per-mount policy merging; cross-process or OS-level enforcement, mount namespaces and
  file permissions; any change to the decorator signature or to decorator ordering; and the `agentbox`,
  `posixcli` and ADK tool layers.

## Open decisions

1. What becomes of this PR and of `keyguardfs`. The owner wrote on 2026-11-26 that `keyguardfs` (PR
   #691, merged) is a stopgap expected to be deleted in favour of `restrictfs` with separate policies.
   This spec is that design; whether it stays open as the design of record, is re-scoped into an
   implementation PR, or is closed with `keyguardfs` kept and extended instead is the owner's call.
   Recommendation: keep this spec as the record of the `restrict` mode, and treat #691's read-side
   shape as the stopgap it was declared to be.
2. How the shared suite treats a restricting wrapper: an `Args` field, a companion
   `drivers/testing/restrictfs.go`, or never running those groups. Recommendation: the companion
   suite, and until the owner allows an edit under `drivers/testing/`, the driver's own tests pin the
   restricted view and the funnel runs over a fixture the policy does not mark.
3. Which catalogs are default: whether certificate and CA bundles — issue #689 names them, and they
   hold public keys, which argues for restricting them for integrity rather than secrecy — are
   restricted; whether directory names such as `keys` belong in the defaults or stay opt-in; and what
   short token markers such as `y"a29."` may ship as defaults given their false-positive radius.
4. Whether the matched pattern is disclosed in the error: it helps an operator author a policy and
   tells a caller what to stop trying, and it hands out the shape of the policy.
5. Whether the wrapper annotates the inner's URI. `keyguardfs`, `readonlyfs` and `readwrapfs` all add
   `ufs.ro=true` (`drivers/wrappers/keyguardfs/keyguardfs_urigetfs.go:38`,
   `drivers/decorators/readonlyfs/readonlyfs_urigetfs.go:36`,
   `drivers/wrappers/readwrapfs/readwrapfs_urigetfs.go:42`). Recommendation: do not add it — those
   three are read-only, and a URI that reopens this wrapper as read-only would drop exactly the write
   refusals this wrapper adds.
6. What "separate policies" means in the API: several `Policy` values stacked as several wrappers, or
   one policy carrying several rules with different presentations. Stacking is expressible today and
   makes the bypass in surface 1 worse (an inner handle for each layer).
7. Whether detection breaks the read at the match, as specified, or withholds the whole file.
8. Whether symlink targets are resolved and re-checked at open, and at what cost.
9. Where the wrapper sits in decorator ordering, once phase 4 registers it.
