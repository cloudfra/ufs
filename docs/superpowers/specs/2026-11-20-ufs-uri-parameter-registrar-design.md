# UFS URI-Parameter Registrar — Design Spec

Status: design only, no implementation. Written against `main` at
`6e7dee9` ("Sync template-go and refresh dependencies (#695)"), which is
unreleased. It answers
[issue #694](https://github.com/cloudfra/ufs/issues/694) and the six answers in
[comment 7031389132](https://github.com/cloudfra/ufs/issues/694#issuecomment-7031389132),
and builds on the rough spec posted in
[comment 7031027085](https://github.com/cloudfra/ufs/issues/694#issuecomment-7031027085).
It answers the five inline comments of review 5989349599 on this pull request.
The decorator that motivated the issue, restrict fs /
[keyGuardFS](https://github.com/cloudfra/ufs/pull/690), is not merged: PR #690
(spec) and PR #691 (name policy) are both open.

Naming note: the issue says decorators "must always implement `ufs.DS`". There
is no `ufs.DS` in the tree (`grep -rnw DS` over `*.go` and `*.md` finds
nothing). The surface is the full `FS` interface at `ufs.go:58`
(`types.WriteFS` + `types.OpenFileFS` + `types.RenameFileFS` +
`types.ReleaseFS`), which is what `ufs.New` returns and what every concrete
value already is. That interface moves to `drivers/types` as `types.FS` and the
root name becomes an alias, `type FS = types.FS`, so `ufs.FS` and `types.FS`
denote one interface. Decorator-facing signatures are written against
`types.FS`, which is what lets a decorator package embed it without importing
the root package; callers keep writing `ufs.FS`. Step 1 of the implementation
plan is that move and nothing else.

## Goal

A decorator declares the URI parameters it understands. Construction removes
those parameters from the URI, decodes their values, and builds the decorator
from them, repeating until no declared parameter is left. Emission is the
inverse: a decorator adds exactly the parameters it declared. Neither direction
may reorder or re-encode parameters it does not own, because the URI a
decorator is tacked onto may be signed over its exact query string.

## Non-goals

- `drivers/wrappers/*` are not registered. They stay manual: the caller wraps a
  file system it already has, and there is no URI for the registrar to read.
- Per-driver parameters keep being parsed by the driver. This design does not
  move bolt's or the HTTP drivers' parsing into the registrar; it only declares
  which `ufs.` parameters a driver owns so the central unknown-parameter check
  leaves them alone.
- Hiding files. A decorator changes behavior on paths the caller can already
  reach. A decorator that removes entries from a listing is a different problem
  and is not solved here; see the `types.WrapperFS` ruling that a wrapper which
  hides root entries cannot satisfy.
- The decorator order a caller can express in a mount spec does not change.

## What the URI does today

There is one `ufs.`-prefix test in the whole construction path, and it is a
copy, not a consume.

| Site                    | What it does                                                                                                                                                                                                     |
| ----------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `construct.go:233-239`  | `New` copies the `options` parameter and every parameter starting with `ufs.` (`construct.go:235`) onto the base URI, then hands that URI to `openNestFS`. Every other query parameter is read as a mount point. |
| `construct.go:243`      | The mount loop skips `ro` and anything already copied, so a copied parameter is not mistaken for a mount path.                                                                                                   |
| `construct.go:535-552`  | `newDecoratedFS` splits the `options` list off the URI (`splitURIOptions`, `uri.go:62`), calls `newBaseFS` with the reduced URI, and passes the entries to `getRegistrar().decorate`.                            |
| `register.go:43-53`     | `Decorator` holds `Name` and `CreateFunc func(context.Context, types.WriteFS, any) (types.WriteFS, error)`. Nothing about URI parameters.                                                                        |
| `readonlyfs.go:123-135` | `URI()` sets a bare `ro=true` through `url.Values` and then appends an `options=[{readOnly: true}]` entry.                                                                                                       |
| `uri.go:41-58`          | `AppendURIOption` rebuilds the whole query with `u.Query()` and `query.Encode()`.                                                                                                                                |

Three round trips are broken today, and they are the reason the registrar needs
to own both directions:

- `memory:?options=[{readOnly: true}]` refuses `Create` with `permission
  denied`: the entry is applied to the nest, and `nestFS.Create`
  (`nestfs.go:676`) routes to the base, which is decorated.
- `memory:?ro=true` opens writable. Only `readWrapFS.URI()`
  (`drivers/wrappers/readwrapfs/readwrapfs.go:50-62`) emits a bare `ro`, and
  nothing reads a bare `ro` as a wrapper; the mount loop skips it
  (`construct.go:243`) and the base driver ignores it.
- `memory:?ufs.readonly=true` opens writable too. Unknown `ufs.` parameters are
  a per-driver choice, not a rule: `boltfs.go:138` rejects them with
  `unknown parameters %q, only ufs. parameters are accepted` and `boltfs.go:150`
  with `unknown parameter %q`; the memory driver never looks at the query.

`drivers/types/uriget.go:23-25` already promises the opposite: "It must be safe
to round-trip: calling ufs.New with u.String() should recreate an equivalent
file system".

## Decisions from the issue

| Issue text                                                                                                | Decision                                                                                                                                                                   |
| --------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| "Decorators are created around nestfs now. This refactor happens early."                                  | Decoration moves from the base driver to the nest. It is steps 3 and 4, before the parameter work.                                                                         |
| "They must always implement ufs.DS. Passthrough functions should be common. Maybe they embed FS instead." | `CreateFunc` takes and returns `FS`, which is `types.FS` with `ufs.FS` as an alias. Decorators embed the composite interface instead of hand-writing pass-through methods. |
| "Need functions to consume url parameters they are tacked on since the original URL might be signed."     | One take helper and one tack helper, both editing the query pair by pair. They are the only sanctioned way to touch a query in the construction and `URI()` paths.         |
| "1. … follow existing convention for creating fs."                                                        | The parameter schema is a field on the existing `Decorator` record; values arrive in a `URIParams` map decoded by the existing `DecodeOptions[T]`.                         |
| "2. Resolution follows same approach as drivers."                                                         | `Decorator.Priority`, resolved the way `matchDriver` (`register.go:235`) resolves it: lower priority wins, equal priority is an error.                                     |
| "3. No standardize on ufs prefix so ufs.ro"                                                               | Every decorator parameter is `ufs.<name>`. `ro` becomes `ufs.ro`. The options list becomes `ufs.options`.                                                                  |
| "5. Yes error"                                                                                            | A `ufs.`-prefixed parameter that no decorator claims and the matched driver does not own is an error.                                                                      |
| "6. Wrapper isn't supported those are manual it's all about decorator"                                    | `drivers/wrappers/*` are unchanged.                                                                                                                                        |

## Decorator record

`register.go` gains two fields; the registration path (`RegisterDecorator`,
`register.go:168`) validates them the way it validates `Name` with
`isLowerCamelCase` (`register.go:222`).

```go
// Decorator is the configuration for a file system decorator: a wrapper that
// changes the behavior of a file system ufs has already built. A decorator is
// selected by the name of a section under a [MountSpec]'s Options, or by a URI
// parameter it declares.
type Decorator struct {
	// Name of the file system decorator. It is the key of the decorator's
	// entry in a [MountSpec]'s Options and the parameter it claims when
	// Params is empty. It is matched exactly (case sensitive) and must be
	// lower camelCase, e.g. "ro".
	Name string

	// Params are the URI parameters this decorator consumes, written without
	// the ufs. prefix: "ro" claims "?ufs.ro", "options" claims
	// "?options=...". An empty list means the decorator claims only Name.
	Params []string

	// Priority breaks a tie when two registered decorators claim the same
	// parameter. The lower value wins, as in Driver.
	Priority int

	// CreateFunc wraps the file system ufs has already built. The last
	// argument is the set of parameters this decorator claimed, taken from its
	// URI pair or from its entry in the options list, see [URIParams] and
	// [DecodeOptions].
	CreateFunc func(context.Context, FS, URIParams) (FS, error)
}

// URIParams holds the parameters a decorator declared, keyed by the claimed
// name without the ufs. prefix. A pair written without a value, "?ufs.ro",
// carries the string "true". A decorator receives only the keys it declared in
// Params; the value is the raw text taken from the URI, decoded by
// [DecodeOptions].
type URIParams map[string]string
```

`NewDecorator[T]` (`register.go:98`) keeps its shape and its `DecodeOptions[T]`
wrapper, and returns the same type with `Params` defaulted to `[]string{name}`.
It calls `createFunc` with `DecodeOptions[T](params[Name])`, so the wrapper stays
the typed form of `CreateFunc`:

```go
// NewDecorator builds a Decorator configuration for a file system decorator,
// to be passed to RegisterDecorator. T is the decorator's options type; its
// Options entry or its URI parameter is decoded into it with [DecodeOptions]
// before createFunc is invoked.
func NewDecorator[T any](name string, createFunc func(context.Context, FS, T) (FS, error)) Decorator
```

A decorator that claims parameters other than `Name` sets `Params` on the
returned value before registering it. Three packages register decorators today
— `faultfs.go:90`, `readonlyfs.go:77`, `telemetryfs.go:148` — and all three
keep the one-line form.

The options list is a second carrier for the same thing. An entry
`?options=[{fault: {errorRate: 0.25}}]` is normalized into the same map as
`params["fault"] = "{errorRate: 0.25}"`, the same text the prefixed pair
`?ufs.fault=[{errorRate: 0.25}]` yields, so both carriers reach one decode path.

Changing `CreateFunc` and `NewDecorator` breaks every decorator registered
outside this repository. Comment 4645985365 permits that: `main` is unreleased
and no compatibility window is kept.

A decorator in `drivers/decorators/*` embeds `types.FS` rather than listing
methods, and does not need the root package to do it: `types.FS` is the
definition, `ufs.FS` is `type FS = types.FS` at `ufs.go:58`, and `drivers/types`
imports no package that imports it.

## Taking and tacking parameters

Both live in `uri.go`, next to `AppendURIOption`, and both work on the raw
query. `internal/httputil.ParseURI` (`internal/httputil/httputil.go:419`)
already does the pair-by-pair walk and documents why
(`internal/httputil/httputil.go:427-430`): "`Values.Encode` sorts and
re-encodes every remaining parameter, which breaks URLs signed over their exact
query string, and `Query` silently drops malformed pairs, which would skip a
malformed checksum." That walk moves into a helper in `internal/httputil` that
`httputil.ParseURI` and these two functions share, so there is one
implementation. The helper is exported from that package, which is internal to
the module and so is not public API.

```go
// TakeURIParams removes from name the pairs claimed by params, in the form
// "ro" (the ufs. prefix is added by the caller's claims), and returns the
// reduced name and the removed values keyed by the name without the prefix. A
// pair written without a value ("?ro") yields the string "true". Every pair
// that is not claimed keeps its original order and encoding.
func TakeURIParams(name string, params []string) (reduced string, values URIParams, err error)

// TackURIParam appends one ufs. parameter to uri. The parameters already in
// the query keep their original order and encoding, which is what a URL signed
// over its exact query string needs. value is percent-encoded; it must not
// already be encoded.
func TackURIParam(uri *url.URL, param, value string) (*url.URL, error)
```

`TackURIParam` is the replacement for the hand-rolled `u.Query()` /
`v.Set(...)` / `u.RawQuery = v.Encode()` pattern, which appears at
`readonlyfs.go:131-133`, `readwrapfs.go:56-58`, `drivers/core/angryfs.go:61-63`
and `drivers/core/nullfs.go:139-141`.

`AppendURIOption` (`uri.go:41`) stays as the way to add an entry to the options
list, and is reimplemented on the same pair-preserving walk. Its current
behavior of returning a new `*url.URL` without changing its input is already
pinned by `TestAppendURIOption` (`uri_test.go:110-133`); the new requirement is
that an unrelated pair keeps its byte-for-byte spelling and position.

Values are percent-encoded once, by `TackURIParam`, and decoded once, by
`TakeURIParams`. `encodeURIOptions`/`decodeURIOptions` (`uri.go:85`,
`uri.go:113`) keep producing the YAML flow form used by the options list, so
`?ufs.fault=[{errorRate: 0.25}]` and `?options=[{fault: {errorRate: 0.25}}]`
carry the same bytes after percent-decoding.

## Construction order

Decoration moves outside the nest, which is what makes a decorator see the
mounted tree.

```go
// newNestFS builds the base file system for name and puts the nest layer
// around it, without decorators. It returns the decorators' pending options,
// collected from the options list and from every declared URI parameter.
func newNestFS(ctx context.Context, name string) (*nestFS, []MountOption, error)

// decorateNest wraps n in the decorators listed in opts. The result is what a
// caller, or a parent nest, uses for data.
func decorateNest(ctx context.Context, n *nestFS, opts []MountOption) (FS, error)

// openNestFS opens name for New: the nest, and the decorated surface over it.
func openNestFS(ctx context.Context, name string) (*nestFS, FS, error)
```

`newDecoratedFS` (`construct.go:535`) becomes:

1. `reduced, opts := splitURIOptions(name)` — the options list, spelled
   `ufs.options`. Nothing reads the bare `options` spelling after step 5.
2. Loop: collect the claims of every registered decorator, then
   `reduced, taken := TakeURIParams(reduced, claims)`, converting each taken
   name and value into a `MountOption{Name, Config}` appended to `opts` — the
   same record an options-list entry produces, so both carriers reach one decode
   path. Repeat while anything was taken. Each pass removes at least one pair, so
   the loop runs at most once per pair.
3. `driver, err := getRegistrar().matchDriver(reduced)`.
4. Any `ufs.`-prefixed pair still in `reduced` that is not in
   `driver.Params` is an error.
5. `newBaseFS(ctx, reduced)` — the driver sees only its own parameters.
6. `makeNestFS` (`nestfs.go:1143`).
7. `decorateNest`, in the existing order: the first option wraps the nest and
   each later one wraps the one before it (`register.go:285-290`). It groups
   `opts` per decorator into a `URIParams` map holding only the names that
   decorator declares in `Params`, and passes that map to `CreateFunc`.

Two assertions carry the consumption guarantee, and each one gets a test. After
the loop, `reduced` contains no `ufs.`-prefixed pair unless the matched driver
declares that name in `Driver.Params`. And a decorator can only ever receive keys
it declared in `Params`, so it cannot consume a parameter that belongs to a
driver or to another decorator.

`newFromMountSpec` (`construct.go:554`) uses the same steps per entry, and
`FSBuilder.Build` / `BuildURI` (`construct.go:316`, `construct.go:345`) reach
them through `openNestFS` as they do now.

### Decorators around a nest mean mounts are not always bare nests

`mountMap` holds `map[string]*nestFS` (`nestfs.go:83-87`), `addMount` and
`addAutoMount` take a `*nestFS` (`nestfs.go:497`, `nestfs.go:506`), and path
routing calls `getFSAndSubpath`, which returns a `*nestFS`
(`nestfs.go:577`), then uses the mount's base directly — `mountFS.fsys.Open`,
`mountFS.fsys.Create`, `mountFS.fsys.MkdirAll` and eleven more call sites from
`nestfs.go:645` to `nestfs.go:917`. Once a mount can be decorated, routing
through `mountFS.fsys` skips the decorator, so a mount-level
`?ufs.ro=true` would not refuse writes. That is the correctness reason for the
change, not a tidy-up:

```go
// mountEntry is one mount of a nest: the surface data operations go through,
// and the nest layer under it for the bookkeeping only a nest can answer.
type mountEntry struct {
	// outer is the decorated view of the mount, or the mount itself when it
	// has no decorators.
	outer FS

	// nest answers isMountParent, isMountPoint, the mount listing and
	// GetAbsPath. ufs keeps this handle instead of asking outer for its
	// inner file system, so a decorator cannot be unwrapped past itself.
	nest *nestFS

	// autoMounted marks a mount ufs opened for itself, see
	// [nestFS.autoMounted].
	autoMounted bool
}
```

`mountMap` becomes `map[string]*mountEntry`. Data operations in `nestfs.go` go
through `outer`; structural questions (`isMountParent`, `isMountPoint`, the
listing at `nestfs.go:419-426`, `GetAbsPath`) go through `nest`. `autoMounted`
moves out of `nestFS` into the entry, which removes the mutation in
`addAutoMount`.

Pass-through cost is real and is stated plainly: `nestFS` has 32 methods, and a
decorator only satisfies `FS` for free by embedding it. Optional capabilities
are not part of `FS` and are lost unless forwarded —
`types.MountedArchiveDirFS` (asserted at `nestfs.go:65-68` and forwarded by hand
at `readonlyfs.go:107` and `readwrapfs.go:42-46`), `types.ComparableFS`,
`types.DeviceInfoGetFS`, `types.CopyFileFS`, `AbsPathGetter`, the watcher. The
conformance suites in `drivers/testing` are the check that a forward exists;
each decorator's test must run them against the decorated file system, not only
against the inner one.

## Parameter names

| Parameter       | Owner           | Notes                                                                                                                                       |
| --------------- | --------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `ufs.ro`        | `readonlyfs`    | Renamed from the decorator name `readOnly` and from the bare `ro`. `readonlyfs.go:41`.                                                      |
| `ufs.fault`     | `faultfs`       | Unchanged name, `faultfs.go:46`.                                                                                                            |
| `ufs.telemetry` | `telemetryfs`   | Unchanged name, `telemetryfs.go:79`.                                                                                                        |
| `ufs.options`   | construction    | The options list, renamed from `options` (`construct.go:46`).                                                                               |
| `ufs.checksum`  | `httputil`      | Already prefixed (`httputil.go:44`); declared by the HTTP-shaped drivers.                                                                   |
| `ro`            | nest mount flag | Stays bare. It is the fstab option (`construct.go:66`, applied at `construct.go:489`) and the per-mount flag skipped at `construct.go:243`. |

The renames happen in one step, step 5, with no alias window: `options` becomes
`ufs.options`, the decorator name `readOnly` becomes `ro`, and the emitted pair
becomes `ufs.ro`. `main` is unreleased, and comment 4645985365 permits the
breaking change.

- `construct.go:243` keeps skipping a bare `ro` mount parameter; nothing else
  reads a bare `ro`.
- `readonlyfs.URI()` (`readonlyfs.go:123-135`) stops setting a bare `ro` and
  emits `?ufs.ro=true` once. This is what fixes the double-application bug.
- `readWrapFS.URI()` (`readwrapfs.go:50-62`) emits `?ufs.ro=true` as well, so a
  readWrap view round-trips into a nest decorated read-only. That is the
  supported path — PR #660 already dropped readWrap in favor of the read-only
  decorator — and the change is stated in the release notes because the reopened
  value is a nest, not a `readWrapFS`.
- `angryfs.go:62` and `nullfs.go:140` keep their bare `ro=true`. It describes
  the driver, not a decorator, and a bare parameter is a driver's business.
- `drivers/testing/urigetfs.go:92-115` (`uriGetReadOnly`) asserts on
  `u.Query().Get("ro")` at `:106` and `:110`; it moves to `ufs.ro` and gains the
  reopen step described under Testing.

The bare `ro` mount flag stays bare, which is a decision rather than an open
question: it is the fstab option, declared at `construct.go:66`, applied at
`construct.go:489` and skipped by the mount loop at `construct.go:243`, so it
names a mount property and not a decorator, and it never reaches the
unknown-parameter check, which looks only at `ufs.`-prefixed pairs. The
alternative is folding it into `ufs.ro` and letting the mount loop read that
spelling; it costs one parameter name in `construct.go` and its mount tests, and
changes nothing else here.

Duplicate claims: `matchDecorator` (`register.go:258`) looks a claim up in the
decorator map by `Name`, so two decorators cannot claim the same parameter
under their own names. A tie is only possible when one decorator lists a second
name in `Params` that is another's `Name`. Resolution copies `matchDriver`
(`register.go:235-252`): the lower `Priority` wins; equal priorities return an
error of the same shape, `ambiguous decorator match for %q, both %q and %q both
have a priority %d`.

Unknown parameters: the error is raised centrally at step 5, not per driver.

```go
// Params are the ufs.-prefixed parameters this driver parses, written without
// the prefix, so the central unknown-parameter check leaves them in the URI.
// Drivers that read no ufs. parameter leave this empty.
Params []string
```

on `Driver` (`register.go:55`). `boltfs` declares the five it parses
(`drivers/boltfs/constants.go:28-42`: `nosync`, `nofreelistsync`,
`nostatistics`, `readonly`, `timeout`; parsed by `parseBoltOptions` at
`boltfs.go:128`). The HTTP-shaped drivers that go through `httputil.ParseURI`
declare `checksum`. `boltfs.go:138` and `boltfs.go:150` stay as they are: a
driver still validates its own parameters, because its `CreateFunc` can be
called directly with any string.

## Changes

| File                                                           | Change                                                                                                                                                                                                                                                  |
| -------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `drivers/types`, `ufs.go`                                      | `FS` defined in `drivers/types/fs.go` (new); `ufs.go:58` becomes `type FS = types.FS`. This is step 1 and changes no behavior.                                                                                                                          |
| `uri.go`                                                       | Add `TakeURIParams` and `TackURIParam`; reimplement `AppendURIOption` and `splitURIOptions` on the shared pair-preserving walk; rename `optionsQueryParam` to `ufs.options`.                                                                            |
| `internal/httputil/httputil.go`                                | Extract the pair walk from `ParseURI` (`:419-455`) into a module-internal helper `uri.go` calls. `ParseURI`'s behavior and its doc comment stay.                                                                                                        |
| `register.go`                                                  | `Decorator.Params`, `Decorator.Priority`, `CreateFunc` over `FS` and `URIParams`; validation in `registerDecorator`; `Driver.Params`; claim lookup and the ambiguous-match error; `decorate` over `FS`.                                                 |
| `construct.go`                                                 | `newNestFS`, `decorateNest`, `openNestFS` returning `(*nestFS, FS, error)`; the claim loop and the unknown-parameter check; `roQueryParam` kept, `optionsQueryParam` renamed; `New` adds mounts through the widened mount map.                          |
| `nestfs.go`                                                    | `mountMap` to `map[string]*mountEntry`; routing from `mountFS.fsys` to `outer`; `addMount`/`addAutoMount` signatures; `autoMounted` moved onto the entry; `URI()` (`:330-347`) tacks with the pair-preserving walk so mount values keep their encoding. |
| `drivers/decorators/readonlyfs`                                | Option name `ro`; `URI()` emits `?ufs.ro=true` once; struct embeds `FS` and overrides the write methods.                                                                                                                                                |
| `drivers/decorators/faultfs`, `drivers/decorators/telemetryfs` | Embed `FS`; forward the optional capabilities each keeps today; `URI()` uses `TackURIParam`.                                                                                                                                                            |
| `drivers/wrappers/readwrapfs`                                  | `URI()` emits `ufs.ro` through `TackURIParam`. No registration.                                                                                                                                                                                         |
| `drivers/boltfs`                                               | Declare `Params` for the five `ufs.` parameters.                                                                                                                                                                                                        |
| HTTP-shaped drivers (`httpfs`, `gcsfs`, `gitfs`, …)            | Declare `Params: []string{"checksum"}` where they read `ufs.checksum`.                                                                                                                                                                                  |
| `drivers/testing/urigetfs.go`                                  | `uriGetReadOnly` reads `ufs.ro`; both URI cases reopen the file system.                                                                                                                                                                                 |
| `construct_test.go`                                            | `TestNewKeepsUFSParameters` becomes `TestNewConsumesUFSParameters`; the reduce-loop table and the two consumption assertions.                                                                                                                           |

`drivers/all/all.go` needs no change: the three decorator packages are already
blank-imported there (`drivers/all/all.go:48-54`), and
`TestAllRegisteringPackagesImported` (`drivers/all/all_test.go:237`) keeps that
honest.

## Testing

- `TestNewKeepsUFSParameters` (`construct_test.go:128`) is rewritten as
  `TestNewConsumesUFSParameters`. It pins copy-not-consume today: it asserts the
  driver is asked to open `ufsparams-test://<host>/a.zip?ufs.checksum=sha256%3Aabcd`.
  After step 5 it asserts the reduced URI the driver is asked to open, which no
  longer carries that pair.
- The two consumption assertions: no `ufs.`-prefixed pair survives in the reduced
  URI unless the matched driver declares the name in `Driver.Params`, and a
  `CreateFunc` receives only the keys its decorator declares in `Params`.
- New table in `construct_test.go` for the reduce loop:
  `memory://a?ufs.ro=true&b=bolt://c?ufs.nosync` opens with the mount decorated
  and bolt's own parameter intact;
  `memory://a?ufs.options=[{ro: true}]` refuses `Create` exactly once rather
  than applying the decorator twice (the `nestfs.go:676` path);
  `memory://a?ufs.nosync` errors with the unknown-parameter message naming
  `nosync`; a mount path named `ufs.ro` is impossible and the error says so.
- Signed-query tests, one per helper: a fixture query whose order and escaping
  are asserted byte for byte after `TakeURIParams`, `TackURIParam`,
  `AppendURIOption` and `nestFS.URI()`. `take(tack(u, p)) == u` byte for byte is
  asserted directly.
- `TestAppendURIOption` (`uri_test.go:110`) gains the no-reorder, no-re-encode
  case.
- `uriGetStable` and `uriGetReadOnly` (`drivers/testing/urigetfs.go:39`,
  `:92`) reopen the file system from `URI()` and re-run the same assertion, so
  a decorator that emits a parameter it cannot consume fails the suite. The
  promise in `drivers/types/uriget.go:23-25` finally has a check behind it.
  `TestParseBoltOptions` (`drivers/boltfs/boltfs_edge_test.go:275`) and
  `TestMakeBoltFSInvalidOptions` (`drivers/boltfs/boltfs_edge_test.go:373`)
  still cover bolt's own parsing.
- Decoration-around-nest behavior: a decorator on a nest with two mounts sees
  both, and a mount-level `?ufs.ro=true` refuses writes routed from the parent.
  Capability forwarding is checked by running the `drivers/testing` suites
  against the decorated value.
- Mount round trip through the widened mount map: the existing
  `nestfs_comparable_test.go` and the mount listing tests stay green, which is
  the guard that `outer` and `nest` were not swapped anywhere.

## Implementation plan

Six PRs in this order, each stacked on the one before it and green on its own.
Steps 1, 2 and 3 are refactors a reviewer can read as behavior-preserving; step 4
changes signatures; steps 5 and 6 change what a URI means.

### Step 1: move the interface

- Files: `drivers/types/fs.go` (new), `ufs.go`.
- Contracts: `types.FS` is the composite at `ufs.go:58` today, doc comment
  included; `ufs.go:58` becomes `type FS = types.FS`. `drivers/types` imports no
  package that imports it, so no cycle appears. The alias form is already used at
  `nestfs.go:43` (`type bufferMode = polyfills.BufferMode`).
- URL parameters: none.
- Behavior: none - refactor only.
- Tests: none; the compiler proves both names denote one interface.
- Does not: change any signature, or move `Decorator`.

### Step 2: the walk and the two helpers

- Files: `internal/httputil/httputil.go`, `uri.go`, `drivers/decorators/readonlyfs`,
  `drivers/wrappers/readwrapfs`, `drivers/core/angryfs.go`, `drivers/core/nullfs.go`.
- Contracts: the pair walk extracted out of `ParseURI`
  (`internal/httputil/httputil.go:419`) into a module-internal helper;
  `TakeURIParams` and `TackURIParam` added to `uri.go`; `AppendURIOption`
  (`uri.go:41`) and `splitURIOptions` (`uri.go:62`) reimplemented on it.
- URL parameters: none renamed.
- Behavior: none - refactor only. The four hand-rolled `v.Set("ro", "true")`
  sites (`readonlyfs.go:131-133`, `readwrapfs.go:56-58`, `angryfs.go:61-63`,
  `nullfs.go:139-141`) become `TackURIParam` calls emitting the same pair.
- Tests: one signed-query fixture per helper, asserting order and escaping byte
  for byte, plus `take(tack(u, p)) == u` byte for byte.
- Does not: rename a parameter, or consume one.

### Step 3: mounts become entries

- Files: `nestfs.go`, `construct.go`.
- Contracts: `mountEntry` holding `outer` and `nest`; `mountMap` widened to
  `map[string]*mountEntry` (`nestfs.go:83-87`); `addMount` and `addAutoMount`
  signatures; `autoMounted` moved onto the entry.
- URL parameters: none.
- Behavior: none - refactor only. Data operations route through `outer`,
  structural questions through `nest`.
- Tests: the mount listing tests and `nestfs_comparable_test.go` stay green,
  which is the guard that `outer` and `nest` were not swapped.
- Does not: put a decorator around a nest. It is mechanical and large on purpose,
  so that step 4 stays small.

### Step 4: decoration around the nest

- Files: `construct.go`, `register.go`, `drivers/decorators/readonlyfs`,
  `drivers/decorators/faultfs`, `drivers/decorators/telemetryfs`.
- Contracts: `newNestFS`, `decorateNest` and `openNestFS`; `CreateFunc` over
  `types.FS` and `URIParams`, with `NewDecorator[T]` following; the three
  decorators (`readonlyfs.go:77`, `faultfs.go:90`, `telemetryfs.go:148`) embed
  `types.FS` and forward the optional capabilities they keep today.
- URL parameters: none read yet; decorators stay on the options list.
- Behavior: a decorator now sees the mounted tree.
- Tests: a decorator on a nest with two mounts sees both, and the
  `drivers/testing` suites run against the decorated value, which is what proves
  each forward exists.
- Does not: take a parameter from a URI, or rename one.

### Step 5: claims and consumption

- Files: `register.go`, `construct.go`, `uri.go`, `drivers/boltfs`, the
  HTTP-shaped drivers.
- Contracts: `Decorator.Params`, `Decorator.Priority` and `Driver.Params`; the
  claim lookup and the ambiguous-match error in `matchDecorator`
  (`register.go:258`).
- URL parameters: the consume loop and the central unknown-parameter error;
  `options` becomes `ufs.options`, the decorator name `readOnly` becomes `ro`,
  and the emitted pair `ro` becomes `ufs.ro`, all here with no alias window.
  `boltfs` declares the five names in `drivers/boltfs/constants.go:28-42`; the
  HTTP-shaped drivers declare `checksum`.
- Behavior: this is the step that changes what a URI means.
- Tests: the reduce-loop table in `construct_test.go`, the two consumption
  assertions named under Testing, and `TestNewKeepsUFSParameters`
  (`construct_test.go:128`) rewritten as `TestNewConsumesUFSParameters`.
- Does not: register `drivers/wrappers/*`, or move driver parsing into the
  registrar.

### Step 6: prove the round trip

- Files: `drivers/testing/urigetfs.go`, and every driver its assertions force.
- Contracts: none.
- URL parameters: none; step 5 fixed the names.
- Behavior: none intended. A driver that cannot reopen its own `URI()` is a bug
  this step finds.
- Tests: `uriGetStable` and `uriGetReadOnly` (`drivers/testing/urigetfs.go:39`,
  `:92`) reopen the file system from `URI()` and re-run the same assertions.
- Does not: rename a parameter, change a contract, or add a driver. Its only
  content is the assertion that the round trip closes.

### URL parameter ordering

A decorator tacks its parameters at the end of the query, in the order its
`Params` declares them. Nothing is sorted, and no pair already in the query is
moved or re-encoded, because the URI may be signed over its exact query string
(`internal/httputil/httputil.go:427-430`).

Claims are collected in a deterministic order, decorators by name and each
decorator's names in declaration order, and pairs are taken left to right. The
consume loop takes at least one pair per pass, so it is bounded by the number of
pairs in the query. A value is percent-encoded once, by `TackURIParam`, and
decoded once, by `TakeURIParams`.

Decorators are applied in the order their claims were collected: the
`ufs.options` entries first, in list order, then the pairs in query order. That
is today's order, where `splitURIOptions` (`uri.go:62`) removes the list before
anything else reads the query. `readonlyfs.URI()` today emits both a bare
`ro=true` and an `options=[{readOnly: true}]` entry; step 5 replaces the two with
a single `ufs.ro=true`.

## Known limitations

- A mount path can no longer be a parameter name that a decorator claims. Today
  `memory://a?ro=...` is a mount path check; after the rename a mount named
  `ro` is still fine, but a mount named `fault` collides with `ufs.fault` only
  in the prefixed spelling, so the collision is new only for names the caller
  writes with the prefix.
- Percent-encoding is normalized for the parameters this design writes, and
  preserved byte for byte for the parameters it does not. A URI whose signature
  covers a `ufs.` parameter a decorator consumed cannot be verified afterwards;
  that is inherent to removing a parameter from a signed query.
- Decorators reach only what `FS` and their own forwards expose. A consumer
  that type-asserts an optional capability off the value `ufs.New` returns gets
  nothing back unless the outermost decorator forwards it.
- `mountArchive` (`nestfs.go:535-575`) builds an archive mount through
  `newBaseFS`/`newBaseFSFromFile` and `makeNestFS` directly. It stays that way:
  an archive's contents are not decorated, so a read-only nest can still be
  written through a `.zip.d` mount. Closing that gap is a separate design.
- The 32 methods of `nestFS` are the upper bound on what a decorator wrapping a
  nest can forward.

## Out of scope

- Registering `drivers/wrappers/*`.
- Nesting decorators inside decorators.
- A UI or helper for editing a URI's parameters.
- Anything in `agentbox` that consumes these URIs.

## Open decisions

1. `Params` on `Decorator` is a name list; a decorator with a structured
   per-parameter schema (allow and deny lists, as keyGuardFS phase 4 would want)
   has no place to declare it. Does that get a schema table now, or stay in
   `DecodeOptions[T]`?
2. Are mount URIs in a nest's query reduced by the parent's decorator claims
   today, and is that intended? `construct.go:233-253` copies prefixed
   parameters onto the base and leaves the mount values alone, which this design
   preserves.
3. Does a `MountSpec`'s `Options` get the same unknown-name rule as the URI, or
   does only the URI get the central check? A mistyped section in a mount spec is
   as silent today as an unknown `ufs.` parameter is.

## Changes from the rough spec

The rough spec in
[comment 7031027085](https://github.com/cloudfra/ufs/issues/694#issuecomment-7031027085)
proposed `Params []string` on `Decorator`, a `consumeUFSOptions` helper in
`uri.go` reusing the pair-by-pair scan, and a reduce loop inside
`newDecoratedFS`. All three survive. Three things changed after the answers:
decoration moves outside the nest, which drags in the mount-map widening that
the rough spec did not anticipate; `ro` and `options` are renamed under the
prefix; and the unknown-parameter rule became central with a `Driver.Params`
exemption instead of a per-driver convention.

Review 5989349599 changed five more things: the surface is `types.FS` with
`ufs.FS` as an alias, which is step 1 and no longer a question; the `any` passed
to `CreateFunc` is now `URIParams`, which is what makes consumption checkable;
the compatibility window for `options` and a bare `ro` is gone, because comment
4645985365 permits breaking changes on an unreleased `main`; the bare `ro` mount
flag is a decision rather than an open question; and the implementation plan is a
numbered sequence of steps with a subsection on URL parameter ordering.
