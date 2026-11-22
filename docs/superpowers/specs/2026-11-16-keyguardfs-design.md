# Restricted-Access Wrapper (RestrictFS) — Design Spec

**Date:** 2026-11-16
**Issue:** <https://github.com/cloudfra/ufs/issues/689>
**Status:** Proposed, revised after review (2026-11-22)

## Goal

Add `drivers/wrappers/restrictfs`: a wrapper that keeps the entries its policy marks as sensitive
visible and makes them untouchable. A marked entry keeps its name and its type in every listing,
`Stat` and `Lstat` answer for it with a masked `fs.FileInfo` whose size is zero, and every
operation that would reach through it fails — `Open`, `OpenFile`, `ReadFile`, `ReadLink`,
`Create`, `MkdirAll`, `Remove`, `RemoveAll`, `Rename` and `CopyFile` at either endpoint. None of
them reports `fs.ErrNotExist`: the entry is there, that is the mode, and an error calling it absent
contradicts the listings, stats and URIs the same wrapper answers.

The policy is the configurable part; the wrapper is general, and ships one: `SensitiveFilePolicy`,
which marks private keys, keystores, `.env` files, PKCS#12 bundles and SSH keys by name, and marks
by content any file holding sensitive material. Issue #689 asks to restrict that catalog; other
sensitive content configures the same wrapper rather than adding a second.

## Surface

`types.FS` (`drivers/types/fs.go:22`) is `WriteFS` plus `OpenFileFS`, `RenameFileFS` and `ReleaseFS`,
and `ufs.FS` is `interface { types.FS }` (`ufs.go:58`); the type lives in `drivers/types`, so naming
it from a `drivers/` package is unremarkable. The wrapper takes and returns that composite, so one
guard over the tree `ufs.New` builds covers every mounted layer. It is not the read-side
`types.ReadFS`, said once: the protected tree is made of `types.FS` layers, so a read-side guard
cannot wrap it, and one that refused only reads could still let a caller delete what it cannot read.

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
promoted method would skip the policy entirely (`drivers/decorators/readonlyfs/readonlyfs.go:91` is
what embedding buys). Every method is written here, optional interfaces and enumerators included,
rather than left to `drivers/polyfills`: those are probes, calling the file system's own method when
it has one (`drivers/polyfills/copyfilefs.go:63`, `drivers/polyfills/renamefs.go:72`), and `ops`
prefers a native method to its own fallback (`ops/copy.go:31`, `ops/rename.go:38`). Forwarding an
interface is therefore a bypass — `CopyFile` handed to the inner never consults the policy — and
omitting one is no safer: the fallback that then runs (`ops/list.go:48`, `ops/foreach.go:36` walk
with `fs.WalkDir`) can be replaced by the file system below at any time, as `nestFS` already replaces
the handle `polyfills.OpenFile` gives it (`nestfs.go:733`, `nestfs.go:751`).

| Surface                                                                                                  | Written here                        | Why                                                                                                                                           |
| :------------------------------------------------------------------------------------------------------- | :---------------------------------- | :-------------------------------------------------------------------------------------------------------------------------------------------- |
| `OpenFile`, `Remove`, `RemoveAll`, `Create`, `MkdirAll`, `Rename`, `Release`                             | yes, they are in `types.FS`         | each can destroy, move or replace restricted material                                                                                         |
| `fs.GlobFS`                                                                                              | yes                                 | it yields names from a pattern the policy never saw, and the tree wrappers answer it natively (`nestfs.go:66`, `drivers/core/localfs.go:53`)  |
| `types.ListFilenames`, `types.ForEachFilenameIter`, `types.ForEachFileInfoIter`                          | yes                                 | the enumeration path a file system takes when it has one                                                                                      |
| `types.CopyFileFS`                                                                                       | yes                                 | `ops.Copy` prefers it over the open-and-write path (`ops/copy.go:31`)                                                                         |
| `types.Watcher` (`drivers/types/notify.go:45`)                                                           | yes, restricted names dropped       | implement none and a restricted mount loses notifications entirely                                                                            |
| `types.WrapperFS`, `ComparableFS`, `URIGetFS`, `DeviceInfoGet`, `MountedArchiveDirFS`, `String`, `Close` | yes, forwarded, masked or annotated | they report presence and identity, not content; `InnerFS()` returns the raw inner, as the contract requires (`drivers/types/wrapperfs.go:26`) |
| `types.MkdirFS`, `types.BatchCreateFS`, `types.ClosestParentFS`, `types.ExternalPathGetFS`               | no, deliberately                    | a batch create that skipped the per-path check would be the one write path with no guard; callers fall back through the rows above            |

The wrapper is built by hand until the decorator signature widens: registered decorators are typed
over `types.WriteFS` in `Decorator.CreateFunc` (`register.go:51`) and in `NewDecorator`
(`register.go:98`). `String()` returns JSON built with `types.Description` and `types.Nested`
(`drivers/types/stringerfs.go:43`), naming the policy with its pattern and marker counts, so an
operator can see that a restriction is present without dumping the policy.

Rejected names, one clause each: `keyguardfs` (PR #691's name) names one policy's catalog, not the
wrapper; `secretfs` promises a general secrets scanner while the catalog also marks certificate
bundles; `redactfs` promises rewriting; `denyfs` and `policyfs` a rule language; `keysfs` reads as a
file system *of* keys. Package and file names stay as they are.

## Enforcement

One mode, `restrict`: the entry keeps its presence and loses its access.

| Operation                                                                                     | Restricted entry                                                                                                         |
| :-------------------------------------------------------------------------------------------- | :----------------------------------------------------------------------------------------------------------------------- |
| `Open`, `OpenFile`                                                                            | `*fs.PathError` wrapping `ErrProtected`, which unwraps to `fs.ErrPermission`                                             |
| `ReadFile`, `ReadLink`                                                                        | refused, as `Open` — a link target is content, and reporting no target misdescribes what is there                        |
| `Stat`, `Lstat`                                                                               | a masked `fs.FileInfo`: name and mode kept, `Size()` zero, zero mod-time                                                 |
| `ReadDir`, the iterators, `Glob`                                                              | listed with the masked info; nothing is filtered out                                                                     |
| `Create`, `MkdirAll`                                                                          | refused, so restricted material cannot be replaced or buried                                                             |
| `Remove`, `RemoveAll`                                                                         | refused — a caller that cannot read a key is not free to delete it                                                       |
| `Rename`, `CopyFile`                                                                          | refused when either endpoint is restricted; checking only the destination is the bug that turns a rename into a copy-out |
| `Read` on an open handle                                                                      | forwarded until the content policy matches, then the read fails with `ErrProtected`                                      |
| `Release`, `Close`, `DeviceInfo`, `IsMountedArchiveDir`, `URI`, `String`, `InnerFS`, `IsSame` | forwarded, `String` naming the policy; they end a session, describe the mount, or follow the `types.WrapperFS` contract  |

A refusal is a `*fs.PathError` from `ufserrors.NewPathError` (`internal/ufserrors/ufserrors.go:31`)
whose `Err` wraps `ErrProtected` — a type with an `Is` method, so `errors.Is(err, ErrProtected)` and
`errors.Is(err, fs.ErrPermission)` both hold, and `Op` names the operation as elsewhere in ufs. A
`host` mount maps the refusal to `EACCES` (`host/fuse_linux.go:471`); plain `fs.ErrPermission` would
not tell a restriction apart from a backend that genuinely cannot read.

Masking the size keeps a stat from reporting how many bytes of key material are on disk, and it is
the size a `host` mount hands back in `stat(2)` (`host/fuse_linux.go:198` `Getattr`,
`host/fuse_linux.go:209` `Lookup`). Name and mode stay because the requested behavior is "you can
see the file but cannot access it", and the masked info is what `ReadDir` reports in its
`fs.DirEntry`. Every method validates the path first (`internal/pathutil/pathutil.go:67`), so the
eleven names in the shared invalid-path list (`drivers/testing/testing.go:46`) still `fs.ErrInvalid`.

## Policy and core API

```go
// Policy decides which entries the wrapper blocks and what a blocked entry reports. It is an
// interface, not a struct of catalogs: content that is not key material needs only its own
// Policy. Implementations must be safe for concurrent use.
type Policy interface {
	// Restricted reports whether the entry at the validated path name is restricted.
	Restricted(name string) bool
	// Sensitive inspects the chunk ending at offset and reports the stream offset where
	// a reader must stop, or -1 for nothing sensitive.
	Sensitive(chunk []byte, offset int64) int64
	// Masked returns the fs.FileInfo a restricted entry reports in place of the inner's.
	Masked(info fs.FileInfo) fs.FileInfo
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

**Names.** `patternutil.Match` matches a path against `path.Match` patterns, against the whole path
or against any single component of it, which gives directory scoping without a `**` extension:
`.ssh` matches `.ssh/id_ed25519`, and `*.key` matches `deploy/prod/api.key`. Patterns are validated
when the policy is built, so a malformed pattern fails in `New` instead of restricting nothing, and
`path.Match` is what every `Glob` on this tree already means
(`internal/globutil/globutil.go:30`): no `**`, no new dependency, and no pattern meaning something
other than the same file system's `Glob` would mean.

**Content and the read.** Name matching fails on a key renamed to `notes.txt`, on a key copied to a
scratch name, and on a key behind a link whose name says nothing, so detection runs on the bytes a
read returns: PEM as a block whose label contains `PRIVATE KEY` — how OpenSSH, PKCS#1, PKCS#8 and
PGP keys announce themselves — plus exact-byte markers for sensitive content with no PEM wrapper.
Reads go through a `guardFile` that embeds the inner `types.File`, counts the bytes already handed
over, and fails the first read whose chunk the content policy matches, with `ErrProtected`. A chunk
boundary cannot hide a match: the scan carries the previous chunk's `len(marker)-1` trailing bytes
and its unterminated trailing line, since a PEM label's length is unbounded. `types.File` also
carries `io.ReaderAt` and `io.Seeker` (`drivers/types/file.go:38`), so a `Seek` must not move the
position the scan counts.

Two consequences follow. A content-detected file opens, because `Open` cannot know what a file
holds; the first read that reaches the material fails, and `ReadFile` fails whole. And `Stat`,
`Lstat` and `ReadDir` cannot know either — finding out in `ReadDir` would cost a read per entry per
listing — so an entry the *name* matched is masked, and one only its content betrays keeps its real
size until a read breaks. Masking every entry is not open to this design: the shared suite requires a
stat's size to equal the bytes `ReadFile` returns (`drivers/testing/readfs.go:211`).

## Conformance: the read-consistency suite

The `ReadConsistency` group in `drivers/testing` (`drivers/testing/readfs.go:46`) walks the whole
tree from its listings (`drivers/testing/readfs.go:82`) and asks that what is listed be reachable;
three of its four subgroups reject a restricted entry as designed:

| Subgroup      | What it asserts                                                                                           | Restricted entry          |
| :------------ | :-------------------------------------------------------------------------------------------------------- | :------------------------ |
| `Conventions` | `fstest.TestFS` opens every name it finds (`drivers/testing/readfs.go:55`)                                | fails                     |
| `Listings`    | every name `ReadDir` reports opens (`drivers/testing/readfs.go:118`)                                      | fails                     |
| `EntryInfo`   | `Lstat` answers for every listed name and matches that entry's `Info()` (`drivers/testing/readfs.go:162`) | passes, both sides masked |
| `ReadPaths`   | `ReadFile` answers for every regular listed entry (`drivers/testing/readfs.go:192`)                       | fails                     |

That is not an argument against the mode: the suite encodes "a file system's read paths agree with
each other", and this wrapper substitutes another invariant — the policy refuses, the same way in
every operation — so the suite has to be told that a wrapper restricts. An owner decision to make:

1. An `Args` field — `Restricting bool`, say — dropping the reachability assertions for the wrapper
   that sets it and keeping the rest; `Args` carries only `ReadOnly` and `Bucket` today
   (`drivers/testing/testing.go:32`).
2. A companion `drivers/testing/restrictfs.go` asserting what a restricting wrapper guarantees
   instead of excusing it: every listed name reachable or refused with `ErrProtected`, the same
   masked info through `ReadDir`, `Stat` and `Lstat`, every write path refusing. The better shape.
3. Running the funnel only over a fixture with no restricted entry, which proves nothing about the
   restricted tree the wrapper exists for.

## Bypass surfaces

1. **Not using the wrapper, or unwrapping it.** `InnerFS()` returns the raw inner, as the
   `types.WrapperFS` contract requires, and the caller that passed `inner` to `New` still holds it.
2. **Placement and caches.** At or above where the sensitive files are mounted, one wrapper covers
   every layer below; below a mount point, a sibling mount reaches the same bytes untouched, and a
   cache above the wrapper keeps content already fetched — so the documented rule is that the
   wrapper goes outermost on the access path. A test seeds a nest with a mount under a restricted
   directory to pin that reads through it fail.
3. **Links inside the wrapped tree.** Name matching sees only the link name, so
   `notes/link → keys/id_ed25519` escapes a name rule; content detection catches it as a broken read
   rather than a refused `Open`. A marker-less binary key behind a clean name escapes both.
4. **Copy or rename into an unrestricted name,** closed by refusing either endpoint.
5. **Notifications.** A watcher on an ancestor reports that subtree's names, so the wrapper
   implements `types.Watcher` and drops notifications naming a restricted path; a caller watching a
   parent then does not learn that a restricted file changed.
6. **Outside ufs.** A `host` mount handed the wrapper does get the restriction: `host.Mount` reads
   through the `types.ReadFS` it is given (`host/host.go:53`) and `fs.ErrPermission` becomes
   `EACCES` (`host/fuse_linux.go:471`) — inference, because there is no `/dev/fuse` in this sandbox.
7. **Metadata that stays visible** — name, mode type, `URI`, `DeviceInfo`, `IsMountedArchiveDir` —
   is the mode the review asked for rather than a leak to close.

## Changes and plan

Phase 1, the implementation PR this spec precedes:

| Path                                                    | Change                                                                                                                                                     |
| :------------------------------------------------------ | :--------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/patternutil`                                  | `Match` and `Validate` over `path.Match`, whole-path and per-component, with tests for both, for malformed patterns and for an empty list matching nothing |
| `drivers/wrappers/restrictfs/restrictfs.go`             | `Policy`, `Options`, `SensitiveFilePolicy`, the defaults, `New`, and `restrictFS` with every method in the enforcement table plus the interface assertions |
| `drivers/wrappers/restrictfs/restrictfs_test.go`        | policy tables, one refusal case per method, masked metadata, pattern validation errors                                                                     |
| `drivers/wrappers/restrictfs/restrictfs_driver_test.go` | the shared funnel over a seeded nest, with the reachability groups left out until the owner rules                                                          |

Nothing existing changes in phase 1: `internal/patternutil` and `restrictfs` are new packages, the
wrapper registers nothing so `drivers/all/all.go` is untouched, and `host` already builds its
attributes from the `fs.FileInfo` it is given.

1. `internal/patternutil`, then `restrictfs` over `types.FS`: the name policy, a refusal per read
   and write method, masked metadata, and tests. Content detection is cut so the cost question is
   deferred.
2. Content detection: `handle.go` with the `guardFile`, the chunked scan, `pemSensitive` and
   `markerSensitive`, `Markers` and `ChunkBytes` in `Options`, and the cost benchmarks.
3. `Glob`, the iterators and `Watcher` with policy filtering, plus the restricted-view conformance
   suite in `drivers/testing` once the owner rules.
4. `drivers/decorators/restrictfs`: YAML options and registration in `drivers/all/all.go`, once the
   decorator signature takes `types.FS`.

PR #691 is open and implements an earlier shape — a read-side wrapper with two presentation modes,
under the older name. What becomes of it is an open decision below; nothing here depends on it.

## Testing

- The shared conformance funnel, the way the `readwrapfs` and `readonlyfs` tests run it, over a
  nest: `ufs.New` around a seeded `mem://` tree, wrapped in the wrapper; as a `types.FS` the funnel
  takes the `ufs.FS` branch and runs the write-side suites (`drivers/testing/testing.go:139`). The
  seed holds a `keys/` directory with a PEM private key and a `server.key`, a `.env` file with a
  marker inside it, a `notes.txt` whose PEM block sits past the first chunk, a marker-free binary
  blob and a plain subdirectory, all non-empty.
- Refusal coverage, one case per method: `Open`, `OpenFile`, `ReadFile`, `ReadLink`, `Create`,
  `MkdirAll`, `Remove`, `RemoveAll`, `Rename` and `CopyFile` at both endpoints, `Glob` and the
  iterators, each checked with `errors.Is` against `fs.ErrPermission` and `ErrProtected`. A
  restricted entry's `Stat`, `Lstat` and `DirEntry.Info()` are asserted equal to each other and
  size zero, and the shared invalid-path names must still report `fs.ErrInvalid`.
- Matching and read-break coverage: whole-path and component patterns, a pattern matching nothing, a
  malformed pattern rejected by `New`, detection on a PEM key, on an unusual PEM label and on a
  marker, off when `ChunkBytes` is negative, and a marker or PEM header straddling a chunk boundary;
  and a clean name with sensitive bytes at offset 0, at a chunk boundary and past the first chunk,
  asserting the byte count handed over before the failure and that the failure is `ErrProtected`.
  A clean file must read back byte-identical in one read, in many small reads, and through
  `io.ReadAll`.
- Locally: `go test ./drivers/wrappers/restrictfs/ ./internal/patternutil/`, then `make testassets`
  once and `go test ./...`, and `make lint-go` before pushing; CI's `make presubmit` is authoritative.

## Limitations and out of scope

- In-process advice only. It is not a sandbox: a caller that never uses the wrapper, unwraps it, or
  reaches the same bytes through `os.Open` on the backing directory is not stopped.
- A read that breaks leaves the caller holding the leading bytes it already received, and detection
  is a scan for known shapes, fooled both ways: a README that quotes
  `-----BEGIN OPENSSH PRIVATE KEY-----` breaks a read, and a raw Ed25519 seed, a binary PKCS#12
  blob or an encrypted keystore with no recognizable header is restricted by name only.
- Every read through the wrapper pays a scan plus one indirection.
- Out of scope: content rewriting or redaction, so no redacted or truncated view is ever returned;
  key generation, key parsing, encryption at rest, certificate validation and trust stores; a rule
  language, rule composition or per-mount policy merging; cross-process or OS-level enforcement,
  mount namespaces and file permissions; any change to the decorator signature or to decorator
  ordering; and the `agentbox`, `posixcli` and ADK tool layers.

## Open decisions

1. How the shared suite treats a restricting wrapper: an `Args` field, a companion suite, or never
   running those groups. What "verified" means for this wrapper waits on this.
2. What becomes of PR #691, which implements the older shape.
3. Which catalog is default: whether certificate and CA bundles — issue #689 names them, and they
   hold public keys, which argues for restricting them for integrity rather than secrecy — are
   restricted, and whether directory names such as `keys` belong in the defaults or stay opt-in.
4. Whether the matched pattern is disclosed in the error: it helps an operator author a policy and
   tells a caller what to stop trying, and it hands out the shape of the policy.
5. Whether the wrapper annotates the inner's URI the way `readwrapfs` adds `ufs.ro=true`
   (`drivers/wrappers/readwrapfs/readwrapfs.go:67`), or leaves it untouched.
6. Whether the write surface refuses at all: a caller that legitimately rotates or deletes a key
   through the same mount is blocked, and "a key that may be deleted but not read" may be backwards.
7. Whether detection breaks the read at the match, as specified, or withholds the whole file.
8. Whether symlink targets are resolved and re-checked at open, and at what cost.
9. Where the wrapper sits in decorator ordering, once phase 4 registers it.
