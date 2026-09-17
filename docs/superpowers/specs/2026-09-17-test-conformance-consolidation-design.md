# Test Conformance Consolidation — Design Spec

## Goal

Consolidate the duplicated FS-level and File-level test suites scattered
across per-backend `_test.go` files into two shared, registry-driven
conformance suites:

1. **FS conformance** — the tests that already loop over
   `getAllTestCaseList()` and its siblings (`TestFSMkdirAll`,
   `TestFSReadFile`, `TestReadOnlyFS`, `TestFS`), plus the six near-identical
   one-line wrapper tests that call `testFileSystem(t, ctor, uri)` per
   backend. These move into a new `conformance_test.go` under
   **`package ufs_test`**, so the file only exercises `ufs`'s exported
   surface — preparing it to be lifted into a separate module/package later.
2. **File conformance** — the duplicated `TestMemFile*`/`TestBoltFile*`
   tests (`Operations`, `Seek`, `ReadAt`, `WriteAtOffset`,
   `ReadDirOnFile`, `SeekNegative`, `DirRead`) collapse into one
   registry-driven suite in a new `fileconformance_test.go`, staying in
   **`package ufs`** since it needs unexported constructors. `gcsFile`'s
   read-side (`Read`/`ReadAt`/`Seek`/`Stat`) joins this suite as new
   coverage — it reimplements the same buffered-content semantics as
   `bufFile` but has no dedicated tests today.

`getAllTestCaseList()` (and `getReadWriteTestCaseList`,
`getAllRegularTestCaseList`, `getAllExceptAngryTestCaseList`) stop being
hand-maintained slice literals and instead read from a small test-only
registrar that mirrors the shape of the production driver registrar in
`register.go`. Every backend's own `_test.go` file self-registers its test
case(s) via `init()`, the same way `boltfs_test.go` already does today with
its standalone `boltFSTestCaseList()` (the one existing instance of this
pattern) — this generalizes that pattern to every backend and removes the
hardcoded `localFS`/`tempMountFS`/`memFS`/`nullFS` literals currently
sitting in `testing_test.go`.

## Non-goals

- No production code behavior changes. `register.go`'s `Driver` type and
  `Register()` are untouched — the new registrar is test-only.
- `nullFS`'s file and `angryFS` are excluded from file conformance: `nullFS`
  always returns empty reads / discards writes by design, and `angryFS`
  never produces a file at all (every FS-level op errors). Both are
  special-cased and keep their existing dedicated tests
  (`nullfs_test.go`, `angryfs_test.go`).
- `localFSFile` (thin `*os.File` wrapper) and archive's file handling
  aren't included — they have no hand-rolled buffering logic to
  consolidate against.
- Widely-shared low-level helpers (`validateClose`, `must`, `mustTemp`,
  `randomString`, `mustTime`, `wantCloseError`, `dirEntryListToNames`,
  `toMapKeys`, `skipTestOnWindows`) stay in `package ufs` (in
  `testing_test.go`) since 16–29 other backend-specific tests use them
  directly and most need unexported access.

## FS conformance registrar

New file `conformance_registry_test.go`, **package `ufs`**. Exported (for
cross-package test use only) types and functions:

```go
// FSTestCase describes one FS backend instance a conformance test can
// exercise. It is exported so package ufs_test's conformance_test.go can
// consume it without cross-package access to unexported constructors.
type FSTestCase struct {
	Name       string
	NewFS      func(tb testing.TB) FS
	WantString string
}

type FSTestCategory int

const (
	ReadWriteCategory FSTestCategory = iota
	ReadOnlyCategory
	PermDeniedCategory
	AngryCategory
)

// RegisterFSTestCase registers an FSTestCase under category. Call from a
// backend's own _test.go init().
func RegisterFSTestCase(category FSTestCategory, tc FSTestCase)

// GetFSTestCases returns the registered cases for category, sorted by Name
// for deterministic test output.
func GetFSTestCases(category FSTestCategory) []FSTestCase
```

Internally this is a plain `map[FSTestCategory][]FSTestCase` guarded by a
mutex (registration happens in `init()`, single-threaded, but a mutex
keeps it consistent with `register.go`'s style and safe if that ever
changes). Unlike the production registrar, there's no `MatchFunc`/priority
matching — tests always want the full set for a category, not a lookup by
name.

`nestFS`-wrapping (today's `appendNestFSTestCase`) becomes a
`GetFSTestCases` post-processing step: `conformance_test.go` calls a
second exported helper:

```go
// WrapNestFS returns cases with each input case doubled: itself, plus a
// nestFS-wrapped copy named "nestFS.<name>".
func WrapNestFS(cases []FSTestCase) []FSTestCase
```

This keeps `makeNestFS` (unexported) usage inside package `ufs`.

### Per-backend registration (replaces hardcoded literals)

Each backend's own `_test.go` gains an `init()` that calls
`RegisterFSTestCase`, replacing the corresponding literal currently in
`testing_test.go`'s `readWriteFSTestCaseList`/`readOnlyFSTestCaseList`/
`permDeniedFSTestCaseList` vars:

| Backend | File | Category |
|---|---|---|
| `localFS` | `localfs_test.go` | ReadWrite |
| `tempMountFS` | `tempmountfs_test.go` | ReadWrite |
| `memFS` | `memfs_test.go` | ReadWrite |
| `boltFS` | `boltfs_test.go` | ReadWrite (replaces `boltFSTestCaseList()`) |
| `nullFS` | `nullfs_test.go` | ReadOnly |
| `readOnlyFS(nullFS)` | `readonlyfs_test.go` | PermDenied |
| `angryFS` | `angryfs_test.go` | Angry (single case, no list needed — keeps today's `angryFSTestCase` as a package-level var for the one test that special-cases it, `TestReadOnlyFS`'s `permDeniedFSTestCaseList` usage stays as-is since angry isn't in that path) |
| `gcsFS` | `gcsfs_test.go` | ReadWrite (new addition — see below; needed to fold `TestGCSFS` into `TestFSConformance`) |

`gitFS` is **not** added to this registry: it isn't in
`getAllTestCaseList()` today and isn't one of the six wrapper tests being
folded in either (there is no `TestGitFS`-style wrapper) — it needs a real
git remote to clone from, so adding it is out of scope for a pure
consolidation.

`testing_test.go` keeps `getAllTestCaseList()` et al. as thin wrappers
that call `GetFSTestCases`/`WrapNestFS` — but since these are only used by
tests moving to `conformance_test.go`, they actually move there entirely
(see below), leaving `testing_test.go` with just the cross-cutting
helpers.

## `conformance_test.go` (package `ufs_test`)

Contains, moved from `testing_test.go` and six backend files:

- `getAllTestCaseList`, `getReadWriteTestCaseList`,
  `getAllRegularTestCaseList`, `getAllExceptAngryTestCaseList` — now
  calling `ufs.GetFSTestCases`/`ufs.WrapNestFS`.
- `testFileSystem`, `mkdirForTest`, `mustFS`, `verifyFS`,
  `verifyReadOnlyFS` — unchanged bodies, updated to reference `ufs.FS`
  etc. instead of the bare (package-local) names.
- `TestFSMkdirAll`, `TestFSReadFile`, `TestReadOnlyFS`, `TestFS` —
  unchanged bodies.
- A new consolidated loop replacing the six wrapper tests:

  ```go
  func TestFSConformance(t *testing.T) {
  	t.Parallel()
  	for _, tc := range ufs.GetFSTestCases(ufs.ReadWriteCategory) {
  		t.Run(tc.Name, func(t *testing.T) {
  			t.Parallel()
  			testFileSystem(t, tc.NewFS, tc.Name)
  		})
  	}
  }
  ```

  `testFileSystem` takes a `func(ctx, name) (FS, error)` + a `name` to pass
  through, while `FSTestCase.NewFS` takes a `testing.TB` and returns a
  ready `FS`. Since the six replaced tests (`TestLocalFS`, `TestMemFS`,
  `TestBoltFS`, `TestGCSFS`, `TestTempMountFSFileSystem`, `TestNestFS`)
  each pass a *fresh, empty* FS and a fixed name/URI, `testFileSystem`'s
  signature changes to accept a `func(tb testing.TB) FS` directly instead
  of `(ctx, name) (FS, error)` + separate `name` — collapsing `mustFS`'s
  job into the caller. `mustFS` is then only needed for the two remaining
  direct callers if any exist after the move (checked during
  implementation; if unused, it's deleted rather than kept as dead code).
  `gcsFS`'s case keeps its dedicated `TestGCSFS`-style registration inline
  in `gcsfs_test.go`'s `init()` (needs a fake `*storage.Client` built via
  `createStorage(tb)`), registered under `ReadWriteCategory` alongside the
  others — bringing `gcsFS` into `TestFSConformance` even though it wasn't
  in `getAllTestCaseList()` before, since it was already being exercised
  via the now-removed `TestGCSFS` wrapper and belongs in the same
  consolidated loop.
  `nestFS`'s case (`TestNestFS`) is subsumed by `WrapNestFS` producing a
  `"nestFS.memFS"`-style entry automatically — no separate registration
  needed.

Backend files lose: `TestLocalFS`, `TestMemFS`, `TestBoltFS`, `TestGCSFS`,
`TestTempMountFSFileSystem`, `TestNestFS`.

## File conformance (`fileconformance_test.go`, package `ufs`)

A second, independent registry (same shape, different element type) for
`File`-level cases:

```go
type FileTestCase struct {
	Name string
	// NewFile returns a freshly-created, writable File plus the FS it
	// belongs to (needed for ReadDir-on-file / directory-read checks).
	NewFile func(tb testing.TB, name string) (File, FS)
	// SupportsOverwrite is false for backends (gcsFile) whose Write
	// cannot seek-and-overwrite in place — only append-on-create.
	SupportsOverwrite bool
}
```

Registered cases: `memFile` (via `newMemFS`), `boltFile` (via
`newTestBoltFS`/`makeBoltFS`) — both `SupportsOverwrite: true`; `gcsFile`
(via `createStorage(tb)` + `makeGCSFSWithClient`) — `SupportsOverwrite:
false`, and only wired into the subset of sub-tests that don't require
overwrite (see below).

Consolidated tests, replacing `TestMemFile*`/`TestBoltFile*` pairs:

- `TestFileOperations` — loops all registered cases (the current
  `TestMemFileOperations`/`TestBoltFileOperations` body, generalized).
- `TestFileSeek`, `TestFileReadAt`, `TestFileReadDirOnFile`,
  `TestFileSeekNegative`, `TestFileDirRead` — same treatment, all cases.
- `TestFileWriteAtOffset` — only cases with `SupportsOverwrite: true`
  (skips `gcsFile`).

`gcsFile`'s inclusion adds new coverage (Read/ReadAt/Seek/Stat semantics
against a freshly-written object) it didn't have before; this was an
explicit scope decision (see below), not implied by "consolidate
duplicates" alone.

`memfs_test.go` and `boltfs_test.go` lose the 7 `TestMemFile*`/
`TestBoltFile*` functions listed above; `TestMemFSMkdirAllInvalid`/
`TestBoltFSMkdirAllInvalid` and everything else stay put (they're FS-level
path validation, not File-level).

`nullFile` and `angryFS`'s file handling are explicitly excluded per
scope decision and keep their existing dedicated tests untouched.

## Migration checklist

1. Add `conformance_registry_test.go` (package `ufs`): `FSTestCase`,
   `FSTestCategory`, `RegisterFSTestCase`, `GetFSTestCases`, `WrapNestFS`.
2. Add `init()` registrations to `localfs_test.go`, `tempmountfs_test.go`,
   `memfs_test.go`, `boltfs_test.go` (replacing `boltFSTestCaseList()`),
   `nullfs_test.go`, `readonlyfs_test.go`, `gcsfs_test.go`. Remove the
   corresponding hardcoded vars from `testing_test.go`.
3. Create `conformance_test.go` (package `ufs_test`): move
   `getAllTestCaseList` & siblings, `testFileSystem`, `mkdirForTest`,
   `mustFS` (if still needed), `verifyFS`, `verifyReadOnlyFS`,
   `TestFSMkdirAll`, `TestFSReadFile`, `TestReadOnlyFS`, `TestFS`; add
   `TestFSConformance` replacing the six wrapper tests. Delete the moved
   pieces from `testing_test.go` and the six wrapper tests from their
   backend files.
4. Add `fileconformance_test.go` (package `ufs`): `FileTestCase` registry
   + `TestFileOperations`/`TestFileSeek`/`TestFileReadAt`/
   `TestFileWriteAtOffset`/`TestFileReadDirOnFile`/`TestFileSeekNegative`/
   `TestFileDirRead`. Register `memFile`, `boltFile`, `gcsFile` cases
   (inline `init()` or explicit registration calls from
   `memfs_test.go`/`boltfs_test.go`/`gcsfs_test.go`).
5. Delete the 7 duplicated `TestMemFile*` functions from `memfs_test.go`
   and 7 `TestBoltFile*` functions from `boltfs_test.go`.
6. `make test`, `make lint`, `make presubmit` — must pass with identical
   effective coverage (same assertions, same backends exercised) plus the
   new `gcsFile` read-side coverage.

## Risks / open questions resolved during brainstorming

- **Package-boundary access**: `ufs_test`'s `conformance_test.go` can only
  reach package `ufs` through exported identifiers. `FSTestCase`,
  `RegisterFSTestCase`, `GetFSTestCases`, `WrapNestFS` are the only new
  exported-for-test surface, all defined in `_test.go` files, so they
  don't leak into the real `ufs` public API (`go doc` / production
  consumers never see them — they only exist in the test binary).
- **`testFileSystem` signature change**: changing it from
  `(ctx, newFSFunc, name)` to `(tb, newFS func(tb) FS, name)` touches its
  6+ current call sites; all are being removed/consolidated as part of
  this change, so there's no orphaned caller left on the old signature.
- **Double-Close risk**: today's `fsTestCase.createFS` registers
  `tb.Cleanup(Close)` itself, while `testFileSystem` also calls
  `fsys.Close()` explicitly at the end. `FSTestCase.NewFS` keeps the same
  `tb.Cleanup(Close)` convention as the existing `fsTestCase.createFS`
  closures (copied as-is), and `testFileSystem`'s own explicit
  `fsys.Close()` call is removed in favor of relying on `tb.Cleanup`,
  matching how `TestFS`/`TestFSMkdirAll` already handle it via
  `validateClose`.
