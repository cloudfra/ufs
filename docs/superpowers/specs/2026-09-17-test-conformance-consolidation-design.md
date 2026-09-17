# Test Conformance Consolidation — Design Spec

**Revision note:** this spec was revised after the initial version was
approved, in light of a broader project direction: `ufs` will eventually
split its drivers into smaller packages that can be imported
conditionally (à la `database/sql` drivers), rather than staying one
monolithic package. That changes where the actual conformance *assertion
logic* should live — see "Reusable `conformance` package" below. The
overall consolidation scope (what moves where, which tests get folded)
is unchanged from the approved version.

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

## Reusable `conformance` package

New directory/package `conformance/` (import path
`github.com/cloudfra/ufs/conformance`), containing **real, non-`_test.go`
Go files** — not a test-only helper. This is the piece motivated by the
future driver split: once a driver (say `gcsfs`) moves into its own
package or module, its own test file needs to run the same conformance
checks without any dependency on `ufs`'s internal test machinery. A
`_test.go`-scoped helper only exists inside the package that defines it;
a real importable package works from anywhere.

`conformance` depends only on `ufs`'s public `FS`/`File` interfaces and
the stdlib (`testing`, `testing/fstest`) — never on `ufs`'s unexported
internals or its `_test.go` files, so there's no import cycle: `ufs`'s
own `_test.go` files (still `package ufs`) are free to import
`conformance`, since `conformance` only imports the production `ufs`
package, never the reverse.

```go
package conformance

// FSTestCase describes one FS backend instance to run the conformance
// battery against.
type FSTestCase struct {
	Name       string
	NewFS      func(tb testing.TB) ufs.FS
	WantString string
}

// RunFS runs the standard CRUD + fstest.TestFS battery against tc
// (today's testFileSystem logic, generalized).
func RunFS(t *testing.T, tc FSTestCase)

// FileTestCase describes one File-producing backend to run the file-level
// conformance battery against.
type FileTestCase struct {
	Name    string
	NewFile func(tb testing.TB, name string) (ufs.File, ufs.FS)
	// SupportsOverwrite is false for backends (gcsFile) whose Write cannot
	// seek-and-overwrite in place — only append-on-create.
	SupportsOverwrite bool
}

// RunFile runs Operations/Seek/ReadAt/ReadDirOnFile/SeekNegative/DirRead
// checks as subtests, plus WriteAtOffset when SupportsOverwrite is true.
func RunFile(t *testing.T, tc FileTestCase)
```

Any backend — in-tree today or split into its own package tomorrow —
builds a case from its own (possibly unexported) constructors and calls
`conformance.RunFS`/`conformance.RunFile` directly. No registry is needed
at that level; registration only matters for *aggregating* all of
today's in-tree backends into one "run everything" test (below), which
is a concern local to this module, not something a future standalone
driver package needs to participate in.

`TestFSMkdirAll`, `TestFSReadFile`, `TestReadOnlyFS`, `TestFS` (the
already-loop-driven tests, as opposed to the six duplicated one-liners)
stay as-is in `conformance_test.go` for now rather than also moving into
`conformance` — they aren't duplicated across backend files today, so
extracting them isn't needed to solve the current duplication. They're
reasonable future candidates for `conformance` if a split-out driver
package ever needs them.

## FS conformance registrar

New file `conformance_registry_test.go`, **package `ufs`**. Exported (for
cross-package test use only) functions, operating on `conformance.FSTestCase`
directly rather than a second, locally-defined type:

```go
type FSTestCategory int

const (
	ReadWriteCategory FSTestCategory = iota
	ReadOnlyCategory
	PermDeniedCategory
	AngryCategory
)

// RegisterFSTestCase registers a conformance.FSTestCase under category.
// Call from a backend's own _test.go init().
func RegisterFSTestCase(category FSTestCategory, tc conformance.FSTestCase)

// GetFSTestCases returns the registered cases for category, sorted by Name
// for deterministic test output.
func GetFSTestCases(category FSTestCategory) []conformance.FSTestCase
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
func WrapNestFS(cases []conformance.FSTestCase) []conformance.FSTestCase
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
- `mkdirForTest`, `verifyFS`, `verifyReadOnlyFS` — unchanged bodies,
  updated to reference `ufs.FS` etc. instead of the bare (package-local)
  names. (`testFileSystem` and `mustFS` do **not** move here — see below.)
- `TestFSMkdirAll`, `TestFSReadFile`, `TestReadOnlyFS`, `TestFS` —
  unchanged bodies, calling `tc.NewFS(t)` directly (cases are already
  built, no `mustFS` needed).
- A new consolidated loop replacing the six wrapper tests, calling the
  extracted `conformance.RunFS` instead of a local `testFileSystem`:

  ```go
  func TestFSConformance(t *testing.T) {
  	t.Parallel()
  	for _, tc := range ufs.GetFSTestCases(ufs.ReadWriteCategory) {
  		t.Run(tc.Name, func(t *testing.T) {
  			t.Parallel()
  			conformance.RunFS(t, tc)
  		})
  	}
  }
  ```

  `conformance.RunFS`'s signature takes a `conformance.FSTestCase`
  directly (`NewFS func(tb testing.TB) ufs.FS` + `Name` + `WantString`),
  which is exactly the registry's element type — no adapter needed. The
  old `testFileSystem(ctx, newFSFunc, name)` free function is retired:
  its body becomes `conformance.RunFS`, and `mustFS` (only otherwise used
  by `localfs_test.go`'s two direct callers) stays put, unchanged, in
  `testing_test.go` — it never needed to move.
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

Builds `conformance.FileTestCase` values (type defined in the
`conformance` package, above) from each backend's own constructors and
calls the extracted `conformance.RunFile` — no local registry needed here
since there's no separate "run everything" test beyond this one file:

```go
var fileTestCases = []conformance.FileTestCase{
	{Name: "memFile", NewFile: newMemFileForTest, SupportsOverwrite: true},
	{Name: "boltFile", NewFile: newBoltFileForTest, SupportsOverwrite: true},
	{Name: "gcsFile", NewFile: newGCSFileForTest, SupportsOverwrite: false},
}

func TestFileConformance(t *testing.T) {
	for _, tc := range fileTestCases {
		t.Run(tc.Name, func(t *testing.T) {
			conformance.RunFile(t, tc)
		})
	}
}
```

`conformance.RunFile` runs `Operations`/`Seek`/`ReadAt`/`ReadDirOnFile`/
`SeekNegative`/`DirRead` as subtests for every case, plus `WriteAtOffset`
only when `SupportsOverwrite` is true — so `gcsFile` gets everything
except `WriteAtOffset`. `newMemFileForTest`/`newBoltFileForTest` wrap
`newMemFS`/`newTestBoltFS` + `fsys.Create(name)`; `newGCSFileForTest`
wraps `createStorage(tb)` + `makeGCSFSWithClient` + `fsys.Create(name)`.

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

1. Add `conformance/conformance.go` (new package): `FSTestCase`, `RunFS`
   (body = today's `testFileSystem`), `FileTestCase`, `RunFile` (body =
   today's 7 file-level checks as subtests, `WriteAtOffset` gated on
   `SupportsOverwrite`).
2. Add `conformance_registry_test.go` (package `ufs`): `FSTestCategory`,
   `RegisterFSTestCase`, `GetFSTestCases`, `WrapNestFS` — operating on
   `conformance.FSTestCase`.
3. Add `init()` registrations to `localfs_test.go`, `tempmountfs_test.go`,
   `memfs_test.go`, `boltfs_test.go` (replacing `boltFSTestCaseList()`),
   `nullfs_test.go`, `readonlyfs_test.go`, `gcsfs_test.go`. Remove the
   corresponding hardcoded vars from `testing_test.go`.
4. Create `conformance_test.go` (package `ufs_test`): move
   `getAllTestCaseList` & siblings, `mkdirForTest`, `verifyFS`,
   `verifyReadOnlyFS`, `TestFSMkdirAll`, `TestFSReadFile`, `TestReadOnlyFS`,
   `TestFS`; add `TestFSConformance` (calls `conformance.RunFS`) replacing
   the six wrapper tests. Delete the moved pieces from `testing_test.go`
   (including the now-retired `testFileSystem`) and the six wrapper tests
   from their backend files. `mustFS` stays in `testing_test.go` unchanged.
5. Add `fileconformance_test.go` (package `ufs`): `fileTestCases` slice +
   `TestFileConformance` (calls `conformance.RunFile` per case), plus the
   `newMemFileForTest`/`newBoltFileForTest`/`newGCSFileForTest` adapters.
6. Delete the 7 duplicated `TestMemFile*` functions from `memfs_test.go`
   and 7 `TestBoltFile*` functions from `boltfs_test.go`.
7. `make test`, `make lint`, `make presubmit` — must pass with identical
   effective coverage (same assertions, same backends exercised) plus the
   new `gcsFile` read-side coverage.

## Risks / open questions resolved during brainstorming

- **Package-boundary access**: `ufs_test`'s `conformance_test.go` reaches
  package `ufs` only through exported identifiers (`FSTestCategory`,
  `RegisterFSTestCase`, `GetFSTestCases`, `WrapNestFS`, defined in a
  `_test.go` file so they never leak into `ufs`'s real public API) plus
  the genuinely public `conformance` package.
- **No import cycle**: `conformance` imports only the production `ufs`
  package (for the `FS`/`File` interfaces); `ufs`'s `_test.go` files
  import `conformance` back. This is one-directional — `conformance`
  never imports `ufs`'s test files — so it compiles like any other
  `package foo` / `package foo_test` + helper-package arrangement.
- **Double-Close risk**: today's `fsTestCase.createFS` registers
  `tb.Cleanup(Close)` itself, while `testFileSystem` also calls
  `fsys.Close()` explicitly at the end — already double-closing today for
  every case routed through `TestFS`/`TestFSMkdirAll` (which also
  `defer validateClose(t, fsys)()` on top of the `tb.Cleanup`). Since this
  is pre-existing, tolerated behavior (every in-scope backend's `Close()`
  already survives a double call), `conformance.RunFS` keeps the same
  shape: `FSTestCase.NewFS` registers `tb.Cleanup(Close)`, and `RunFS`
  itself does not call `Close()` a second time — matching how
  `TestFS`/`TestFSMkdirAll` already rely on `tb.Cleanup` as the source of
  truth for cleanup.
