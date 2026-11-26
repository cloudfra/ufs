# UFS URI-Parameter Registrar — Design Spec

Status: design only, no implementation. Written against `main` at `d94c72e`
("Split gcsFS into one file per interface (#741)", tagged `v0.18.0`). It answers
[issue #694](https://github.com/cloudfra/ufs/issues/694) and the six answers in
[comment 7031389132](https://github.com/cloudfra/ufs/issues/694#issuecomment-7031389132),
and builds on the rough spec posted in
[comment 7031027085](https://github.com/cloudfra/ufs/issues/694#issuecomment-7031027085).
It answers the five inline comments of review 5989349599 on this pull request.

Two steps of the plan this spec first posted have merged in the meantime, and one
of them merged differently from the way it was written here. What merged, and what
is still open, is in [What merged while this spec
waited](#what-merged-while-this-spec-waited); the implementation plan is numbered
against what is left. Code citations, call-site counts and behavior claims were
re-checked against the commit named above: a statement that did not survive the
check is corrected in place rather than annotated, and the URI claims under [What
the URI does today](#what-the-uri-does-today) were re-run against that commit
rather than read off the code.

Naming note: the issue says decorators "must always implement `ufs.DS`". There is
no `ufs.DS` in the tree. The surface is `types.FS` (`drivers/types/fs.go:22`),
which is `types.WriteFS` (`drivers/types/writefs.go:19`) plus `types.OpenFileFS`,
`types.RenameFS` and `types.ReleaseFS`. That interface already lives in
`drivers/types`, so the move this spec first proposed as its own step is done and
is not a step any more. The root name is **not** an alias of it: `ufs.go:58` is
`type FS interface { types.FS }`, a distinct interface that embeds `types.FS` and
whose method set is currently identical to it. Comment 4645717461 rules that
decorator-facing signatures are written against `types.FS`, which is what lets a
decorator package embed it without importing the root package; nothing here asks
the root package to change its declaration. `types.FS` is deliberately incomplete:
`drivers/types/fs.go:28-31` carries "TODO: Add all optional types", naming
`BatchCreateFS`, `ChmodFS` and `fs.GlobFS`, and `Copy` is commented out at
`drivers/types/fs.go:33-34`. Optional capabilities are therefore forwarded by hand
and tracked separately, see the mount-entry section.

## Goal

A decorator declares the URI parameters it understands. Construction removes
those parameters from the URI, decodes their values, and builds the decorator
from them, repeating until no declared parameter is left. Emission is the
inverse: a decorator adds exactly the parameters it declared. Neither direction
may reorder or re-encode parameters it does not own, because the URI a decorator
is tacked onto may be signed over its exact query string.

## Non-goals

- `drivers/wrappers/*` are not registered. They stay manual: the caller wraps a
  file system it already has, and there is no URI for the registrar to read. The
  merged `keyguardFS` is in that group today — it is constructed by
  `New(inner types.ReadFS, opts Options) (types.ReadFS, error)`
  (`drivers/wrappers/keyguardfs/keyguardfs.go:108`) and registers nothing, which
  is exactly why it cannot be selected from a URI. Turning it into a decorator is
  the RestrictFS spec in PR #690's work, not this design's.
- Per-driver parameters keep being parsed by the driver. This design does not
  move bolt's or the HTTP drivers' parsing into the registrar; it only declares
  which `ufs.` parameters a driver owns so the central unknown-parameter check
  leaves them alone.
- Hiding files. A decorator changes behavior on paths the caller can already
  reach. A decorator that removes entries from a listing is a different problem
  and is not solved here; see the `types.WrapperFS` ruling that a wrapper which
  hides root entries cannot satisfy.
- The decorator order a caller can express in a mount spec does not change.

## What merged while this spec waited

Construction and URI handling both moved under this design while it was open.

| Merged         | What it settled                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `0b3e828` #698 | Every file system must report a URI: "Every file system reports a URI" is now part of the contract at `drivers/types/uriget.go:27`, and the round-trip promise this design exists to close is at `drivers/types/uriget.go:23-25`. Conformance additions followed in `15e0c8a` #679.                                                                                                                                                                                                     |
| `6cdf704` #699 | The take/tack pair. `uri.RemoveParameter` (`drivers/uri/uri.go:68`) removes claimed pairs and returns their values; `uri.AddParameter` (`drivers/uri/uri.go:126`) appends or replaces one pair in place. Both edit the query pair by pair over `internal/httputil/uriparams.go` (`QueryPair` `:36`, `SplitQueryParts` `:52`, `JoinQuery` `:70`), so nothing is sorted or re-encoded. The hand-rolled `u.Query()` / `v.Set()` / `RawQuery = v.Encode()` sites this spec listed are gone. |
| `6db7e7e` #700 | The prefix. `ufs.options` is `uri.OptionsParam` (`drivers/uri/uri.go:171`); `construct.go:45` declares `ufsQueryParamPrefix = "ufs."` and `construct.go:52` builds `roQueryParam` as `ufs.` + `ro`. A `ufs.`-prefixed parameter is never a mount point (`construct.go:228-235`), and a bare `ro` or `options` **is** one now, pinned by `TestNewQueryParametersAreMountPoints` (`construct_test.go:171`).                                                                               |
| `fbdd41b` #704 | Decorators implement `types.FS`: `drivers/decorators/readonlyfs/readonlyfs.go:37` asserts `_ types.FS = (*readOnlyFS)(nil)`. So does `nestFS` (`nestfs.go:61`).                                                                                                                                                                                                                                                                                                                         |
| `bcb1d40` #717 | URI address helpers (`uri.IsServingAddressString`, `drivers/uri/uri.go:368`), which is where `drivers/testing` now checks that a reported URI is addressable.                                                                                                                                                                                                                                                                                                                           |
| #724 … #743    | Every driver and wrapper, `nestFS` included, is split into one file per interface. Every `nestfs.go:NNN` and `drivers/core/*.go` line number in the first version of this spec moved; the split is the reason the routing changes below list a dozen files instead of one.                                                                                                                                                                                                              |

What is still open, and is the whole content of this design: `Decorator.Params`
and `Decorator.Priority`, `Driver.Params`, the central unknown-parameter error, a
`CreateFunc` that takes `types.FS` and a parameter map instead of `any`, the
decorator rename `readOnly` → `ro`, decoration outside the nest, mounts as entries
rather than bare nests, widening `ufs.New`, and an assertion that a reported URI
reopens to the same thing.

## What the URI does today

There is one `ufs.`-prefix test in the whole construction path, and it is a
copy, not a consume.

| Site                                             | What it does                                                                                                                                                                                                                                                 |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `construct.go:236`                               | `New` takes the query apart, copies `ufs.`-prefixed parameters onto the base URI (`construct.go:250-258`, `strings.HasPrefix` at `:254`) and hands that URI to `openNestFS` (`:259`). Every other parameter names a mount point (`:261-273`).                |
| `construct.go:228-235`                           | The documented rule: a `ufs.`-prefixed parameter is never a mount point, so a mount path cannot begin with `ufs.`; a valueless parameter names the local file system, so `memory://?ro` mounts the working directory at `ro`.                                |
| `construct.go:290`                               | `openNestFS` = `newDecoratedFS` then `makeNestFS` (`:296`). Decoration is still inside the nest.                                                                                                                                                             |
| `construct.go:540-552`                           | `newDecoratedFS` splits `ufs.options` off the name (`splitURIOptions`, `uri.go:41`), drops a `ufs.ro` **marker** with `uri.RemoveParameter(name, []string{roQueryParam})` (`:552`), and calls `newBaseFS` with what is left.                                 |
| `construct.go:560`                               | `getRegistrar().decorate` wraps the **base driver**, not the nest.                                                                                                                                                                                           |
| `register.go:43-51`                              | `Decorator` holds `Name` and `CreateFunc func(context.Context, types.WriteFS, any) (types.WriteFS, error)`. Nothing about URI parameters.                                                                                                                    |
| `register.go:55-57`                              | `Driver` holds `Name` only. There is no record of which `ufs.` parameters a driver parses.                                                                                                                                                                   |
| `drivers/decorators/readonlyfs/readonlyfs.go:42` | The decorator is still named `readOnly`; `drivers/decorators/readonlyfs/readonlyfs_urigetfs.go:36` emits `?ufs.ro=true` and `:40` appends `ufs.options=[{readOnly: {enabled: true}}]`, so a read-only nest round-trips through two spellings of one wrapper. |

Two round trips are broken today. Both were re-run against `d94c72e`.

- **An unknown `ufs.` parameter is nobody's business.** `memory://x?ufs.readonly=true`
  opens **writable**: no decorator matches it, memory reads no query, and the pair
  survives into the reported URI (`{"nestFS":{"/":{"memFS":"memory://x?ufs.readonly=true"}}}`).
  A typo in a decorator name is therefore silent. It is a per-driver choice today,
  not a rule: boltfs rejects an unprefixed pair with `unknown parameters %q, only
  ufs. parameters are accepted` (`drivers/boltfs/boltfs.go:143`) and an unknown
  prefixed one with `unknown parameter %q` (`drivers/boltfs/boltfs.go:155`), naming
  the five it parses in `drivers/boltfs/constants.go:28-42`; the memory driver never
  looks at the query.
- **A decorator on a nest does not cover its mounts.**
  `memory://x?ufs.options=[{readOnly:true}]&sub=memory:` refuses `Create("direct")`
  with `create direct: permission denied` and returns **nil** for
  `Create("sub/escapes")`. The entry decorates the mount's base, and the nest's own
  routing (`nestfs_openfilefs.go`, `nestfs_createfilefs.go`, `nestfs_mkdirallfs.go`,
  `nestfs_removefs.go`, …) reaches `mountFS.fsys` directly, under the decorator. The
  URI the value reports — `memory://x?sub=memory%3A&ufs.options=%5B%7BreadOnly%3A+true%7D%5D&ufs.ro=true`
  — reproduces the hole on reopen. `TestNewAppliesURIOptions` (`uri_test.go:157`)
  pins the current arrangement: its "mount" subtest (`:173`) asserts the tag on
  `nFS.mounts.m["data"].fsys`, which is the mount map this design widens.

The third broken trip in the first version of this spec — `memory:?ro=true` opening
writable — is fixed by #700 and is no longer true: a bare `ro` is a mount point
now, so `memory://x?ro=true` fails with `mount point "ro": failed to mount "true"
with driver "local"`. The `ufs.ro` marker is dropped before the driver sees the name
(`construct.go:552`) without applying any layer, which is the gap step 5 closes.

`drivers/types/uriget.go:23-25` already promises the opposite of all of this: "It
must be safe to round-trip: calling ufs.New with u.String() should recreate an
equivalent file system".

## Decisions from the issue

| Issue text                                                                                                | Decision                                                                                | Status now                                                                                                            |
| --------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| "Decorators are created around nestfs now. This refactor happens early."                                  | Decoration moves from the base driver to the nest.                                      | Open. Steps 3 and 4 below, before the parameter work.                                                                 |
| "They must always implement ufs.DS. Passthrough functions should be common. Maybe they embed FS instead." | `CreateFunc` takes and returns `types.FS`; decorators embed the composite interface.    | Half merged: `types.FS` exists and decorators assert it (#704); `CreateFunc` is still over `types.WriteFS` and `any`. |
| "Need functions to consume url parameters they are tacked on since the original URL might be signed."     | One take helper and one tack helper, editing the query pair by pair.                    | Merged as `uri.RemoveParameter` and `uri.AddParameter` (#699).                                                        |
| "1. … follow existing convention for creating fs."                                                        | The parameter schema is a field on the existing `Decorator` record.                     | Open.                                                                                                                 |
| "2. Resolution follows same approach as drivers."                                                         | `Decorator.Priority`, resolved the way `matchDriver` (`register.go:235`) resolves it.   | Open.                                                                                                                 |
| "3. No standardize on ufs prefix so ufs.ro"                                                               | Every decorator parameter is `ufs.<name>`; the options list is `ufs.options`.           | Half merged: the prefix exists (#700); the decorator is still named `readOnly`.                                       |
| "5. Yes error"                                                                                            | A `ufs.` parameter no decorator claims and the matched driver does not own is an error. | Open.                                                                                                                 |
| "6. Wrapper isn't supported those are manual it's all about decorator"                                    | `drivers/wrappers/*` are unchanged.                                                     | Confirmed by keyguardFS landing in `drivers/wrappers`.                                                                |

## Rulings on this pull request

The five inline comments are answered as follows; the sections above carry the
detail.

| Comment    | Ruling                                                                                                                                                                    | Where this spec changed                                                                                                                                            |
| ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 4639947895 | The surface is the full `FS`, and `ufs.New` widens.                                                                                                                       | Naming note rewritten; the widening is one of step 4's contracts.                                                                                                  |
| 4645717461 | Use `types.FS`, not an aliased `ufs.FS`.                                                                                                                                  | The alias claim is deleted. `ufs.go:58` is `type FS interface { types.FS }` and stays that way; every decorator-facing signature in this spec reads `types.FS`.    |
| 4639990425 | The parameter set may be `map[string]string`, and "we need to ensure that" every parameter is consumed.                                                                   | No new map type is invented: the existing `uri.Params` (`drivers/uri/uri.go:47`) is reused, and the two consumption assertions are stated as contracts with tests. |
| 4645985365 | Breaking changes are fine.                                                                                                                                                | No alias windows anywhere; the `readOnly` → `ro` rename and `options` → `ufs.options` happen in one step.                                                          |
| 4646001397 | "I want a clear list of steps of what the implementation plan will be … including contract changes like interfaces, planned adjustments to the URL parameters, ordering." | The plan is a numbered sequence with files, contracts, URL parameters, behavior, tests and exclusions per step, plus a subsection on parameter ordering.           |

## Decorator record

`register.go` gains two fields; the registration path (`RegisterDecorator`,
`register.go:168`) validates them the way it validates `Name` with
`isLowerCamelCase` (`register.go:222`).

```go
// Decorator is the configuration for a file system decorator: a wrapper that
// changes the behavior of a file system ufs has already built. A decorator is
// selected by the name of a section under a [MountSpec]'s Options, by a URI
// parameter it declares, or by both.
type Decorator struct {
        // Name of the file system decorator. It is the key of the decorator's
        // entry in a [MountSpec]'s Options and the parameter it claims when
        // Params is empty. It is matched exactly (case sensitive) and must be
        // lower camelCase, e.g. "ro".
        Name string

        // Params are the URI parameters this decorator consumes, written without
        // the ufs. prefix: "ro" claims "?ufs.ro", "options" claims
        // "?ufs.options=...". An empty list means the decorator claims only Name.
        Params []string

        // Priority breaks a tie when two registered decorators claim the same
        // parameter. The lower value wins, as in Driver.
        Priority int

        // CreateFunc wraps the file system ufs has already built. The last
        // argument is the set of parameters this decorator claimed, taken from
        // its URI pair or from its entry in the options list; see [uri.Params]
        // and [DecodeOptions].
        CreateFunc func(context.Context, types.FS, uri.Params) (types.FS, error)
}
```

No new map type. `uri.Params` (`drivers/uri/uri.go:47`) is already
`map[string]string` with exactly the semantics this record needs: keys are the
names as they were written, so a key keeps the `ufs.` prefix when that is how the
caller spelled it; a pair written without a value carries the string `"true"`; a
repeated pair is taken once, by its first value. `internal/httputil.ParseURI`
(`internal/httputil/httputil.go:416`) returns the same shape and documents the
same keying at `:412-413`, so a decorator that receives parameters cannot tell
whether they came from a nest's query or from a driver's own parse — which is the
point. Comment 4639990425 asked whether `any` should become `map[string]string`:
it becomes `uri.Params`, which is that map under the name the module already
uses, so the two halves of construction pass one type around instead of two.

`NewDecorator[T]` (`register.go:98`) keeps its shape and its `DecodeOptions[T]`
(`register.go:117`) wrapper, and defaults `Params` to `[]string{name}`. It calls
`createFunc` with `DecodeOptions[T](params[Name])`, so it stays the typed form of
`CreateFunc`:

```go
// NewDecorator builds a Decorator configuration for a file system decorator,
// to be passed to RegisterDecorator. T is the decorator's options type; its
// Options entry or its URI parameter is decoded into it with [DecodeOptions]
// before createFunc is invoked.
func NewDecorator[T any](name string, createFunc func(context.Context, types.FS, T) (types.FS, error)) Decorator
```

A decorator that claims parameters other than `Name` sets `Params` on the
returned value before registering it. Three packages register decorators today —
`drivers/decorators/readonlyfs/readonlyfs.go:78`, `drivers/decorators/faultfs/faultfs.go:92`, `drivers/decorators/telemetryfs/telemetryfs.go:151` — and all three keep
the one-line form.

The options list is a second carrier for the same thing. An entry
`?ufs.options=[{fault: {errorRate: 0.25}}]` is normalized into the same map as
the prefixed pair `?ufs.fault=[{errorRate: 0.25}]` yields, so both carriers reach
one decode path.

Changing `CreateFunc` and `NewDecorator` breaks every decorator registered
outside this repository. Comment 4645985365 permits that: `main` is released but
`v0.18.0` predates any decorator API promise, and no compatibility window is kept.

A decorator in `drivers/decorators/*` embeds `types.FS` rather than listing
methods, and does not need the root package to do it: `types.FS` is the
definition, and `drivers/types` imports no ufs package that could import it back.
Because `ufs.FS` (`ufs.go:58`) embeds `types.FS` with no methods of its own, a
value produced over `types.FS` still satisfies `ufs.FS`; the root declaration does
not need to become an alias for that to hold.

## Taking and tacking parameters

Merged, in #699. Both helpers live in `drivers/uri` and work on the raw query:

- `uri.RemoveParameter(name string, params []string) (string, uri.Params, error)`
  (`drivers/uri/uri.go:68`) takes the claimed pairs out of a URI string and
  returns the reduced string plus the values. When nothing is claimed it returns
  the name byte for byte and the name itself, which is what makes the claim loop
  below able to stop.
- `uri.AddParameter(u *url.URL, param, value string) (*url.URL, error)`
  (`drivers/uri/uri.go:126`) tacks one pair, replacing every pair of that name in
  place, and returns nil for a nil URL.

The pair-by-pair walk they share is in `internal/httputil/uriparams.go`
(`QueryPair` `:36`, `SplitQueryParts` `:52`, `JoinQuery` `:70`, `Key` `:87`,
`Value` `:98`, `IndexOfQueryKey` `:110`), and the reason it is not
`url.Values` is written once, at `internal/httputil/httputil.go:424-428`:
"`Values.Encode` sorts and re-encodes every remaining parameter, which breaks
URLs signed over their exact query string, and `Query` silently drops malformed
pairs, which would skip a malformed checksum." `httputil.ParseURI`
(`internal/httputil/httputil.go:416`) uses the same walk and keys its result by
the full prefixed name. The round trip and the signed-query property already have
tests in `drivers/uri/uri_test.go`: `TestAddRemoveParameterRoundTrip` (`:367`)
and `TestAddParameterKeepsSignedQuery` (`:409`).

What is left of this section is one site: `nestFS.URI()`
(`nestfs_urigetfs.go:25`) still rebuilds the query with `u.Query()` (`:31`),
`vals.Set(p, mu.String())` (`:39`) and `vals.Encode()` (`:42`), so a nest sorts
and re-encodes its mount parameters — including a mount URI that carries
`ufs.checksum`. It moves to `uri.AddParameter` in step 5.

`uri.AppendOption` (`drivers/uri/uri.go:235`) stays the way to add an entry to
the options list, and already works on the pair-preserving walk; `uri.RemoveOptions`
(`drivers/uri/uri.go:272`) is what `splitURIOptions` (`uri.go:41`) calls.
`uri.Option` (`drivers/uri/uri.go:184`) is the record a mount spec entry and a
taken pair both normalize into: `construct.go:441` already aliases
`MountOption = uri.Option`.

## Construction order

Decoration moves outside the nest, which is what makes a decorator see the
mounted tree.

```go
// buildNestFS builds the base file system for name and puts the nest layer
// around it, without decorators. It returns the decorators' pending options,
// collected from the options list and from every declared URI parameter. The
// name avoids a collision: the root package already declares newNestFS at
// nestfs.go:595, and openNestFS below wraps it.
func buildNestFS(ctx context.Context, name string) (*nestFS, []MountOption, error)

// decorateNest wraps n in the decorators listed in opts. The result is what a
// caller, or a parent nest, uses for data.
func decorateNest(ctx context.Context, n *nestFS, opts []MountOption) (types.FS, error)
```

`openNestFS` (`construct.go:290`) already exists with the signature
`(ctx, name) (*nestFS, error)`; it becomes `(ctx, name) (*nestFS, types.FS, error)` —
the nest for the bookkeeping, the decorated surface for data. `newNestFS`
(`nestfs.go:595`), `NewNestFS` (`nestfs.go:586`) and `WrapNestFS` (`nestfs.go:591`)
keep calling `makeNestFS` (`nestfs.go:603`) unchanged.

`newDecoratedFS` (`construct.go:540`) becomes:

1. `reduced, opts := splitURIOptions(name)` — the options list, spelled
   `ufs.options` (`uri.OptionsParam`, `drivers/uri/uri.go:171`). This step exists
   today.
2. Loop: collect the claims of every registered decorator, then
   `reduced, taken := uri.RemoveParameter(reduced, claims)`, converting each
   taken name and value into a `MountOption{Name, Config}` appended to `opts` —
   the same record an options-list entry produces, so both carriers reach one
   decode path. Repeat while anything was taken. Each pass removes at least one
   pair, so the loop runs at most once per pair.
3. `driver, err := getRegistrar().matchDriver(reduced)` (`register.go:235`).
4. Any `ufs.`-prefixed pair still in `reduced` that is not in `driver.Params` is
   an error.
5. `newBaseFS(ctx, reduced)` (`construct.go:381`) — the driver sees only its own
   parameters. Today this happens after two hand-written removals: `splitURIOptions`
   at `construct.go:541` and the `ufs.ro` marker drop at `construct.go:552`; step 5
   replaces the second with a real decorator claim.
6. `makeNestFS` (`nestfs.go:603`).
7. `decorateNest`, in the existing order: the first option wraps the nest and
   each later one wraps the one before it (`register.go:286`, `decorate` at
   `register.go:290`). It groups `opts` per decorator into a `uri.Params` holding
   only the names that decorator declares in `Params`, and passes that map to
   `CreateFunc`.

Two assertions carry the consumption guarantee that comment 4639990425 asked to
be sure of, and each one gets a test. After the loop, `reduced` contains no
`ufs.`-prefixed pair unless the matched driver declares that name in
`Driver.Params`. And a decorator can only ever receive keys it declared in
`Params`, so it cannot consume a parameter that belongs to a driver or to another
decorator. Together they make "every parameter is consumed" a property of
construction rather than of each driver's goodwill.

`newFromMountSpec` (`construct.go:567`) uses the same steps per entry, and
`FSBuilder.Build` (`construct.go:337`) reaches them through `openNestFS` as it
does now — it already returns the root `FS`. `BuildURI` (`construct.go:366`)
opens nothing: it serializes the root and the nested mounts with `CreateURI`
(`construct.go:378`, `construct.go:94`), so the parameter work changes only the
names it writes.

### Decorators around a nest mean mounts are not always bare nests

`mountMap` holds `map[string]*nestFS` (`nestfs.go:79`, field at `:81`), `addMount`
and `addAutoMount` take a `*nestFS` (`nestfs.go:385`, `nestfs.go:394`), and path
routing calls `getFSAndSubpath` (`nestfs.go:441`), which returns a `*nestFS`, then
uses the mount's base directly — `var owner fs.FS = mountFS.fsys` at
`nestfs_openfilefs.go:73` feeding `polyfills.OpenFile(owner, subName, flag, perm)`
at `:78`, and the same shape in `nestfs_createfilefs.go:30`,
`nestfs_mkdirallfs.go:30`, `nestfs_readdirfs.go:34`, `nestfs_readfilefs.go:28`,
`nestfs_readlinkfs.go:30`, `nestfs_removefs.go:24`, `nestfs_removeallfs.go:24`,
`nestfs_statfs.go:34` and `:64`, `nestfs_renamefs.go:146`, `nestfs_notify.go:40`,
`nestfs.go:504`. Since #743 those thirteen routing sites are in one file per
interface; the count is the same. Once a mount can be decorated, routing through
`mountFS.fsys` skips the decorator, so a mount-level `?ufs.ro=true` would not
refuse writes. That is the correctness reason for the change, not a tidy-up, and
the mount hole recorded under "What the URI does today" is that bug today:

```go
// mountEntry is one mount of a nest: the surface data operations go through,
// and the nest layer under it for the bookkeeping only a nest can answer.
type mountEntry struct {
        // outer is the decorated view of the mount, or the mount itself when it
        // has no decorators.
        outer types.FS

        // nest answers isMountParent, isMountPoint, the mount listing and
        // GetAbsPath. ufs keeps this handle instead of asking outer for its
        // inner file system, so a decorator cannot be unwrapped past itself.
        nest *nestFS

        // autoMounted marks a mount ufs opened for itself, see
        // [nestFS.autoMounted] (nestfs.go:302-308).
        autoMounted bool
}
```

`mountMap` becomes `map[string]*mountEntry`. Data operations go through `outer`;
structural questions (`isMountParent`, `isMountPoint`, the listing built by
`getDirectoryList` (`nestfs.go:110`), `getClosestMount` (`nestfs.go:182`),
`GetAbsPath` (`nestfs_abspathgetter.go:24`)) go through `nest`. `autoMounted`
moves out of `nestFS` into the entry, which removes the mutation in `addAutoMount`
(`nestfs.go:394`). The archive bookkeeping (`inArchive`, `nestfs.go:300`) and the
buffer mode of a mount stay with the nest: `makeNestFS` output already reaches
`mountFS.args.BufMode` at `nestfs_openfilefs.go:100`, so an entry does not need to
duplicate it.

Pass-through cost is real and is stated plainly: `nestFS` now has 33 methods
(`grep -h '^func (fsys \*nestFS)' nestfs*.go | wc -l`), and a decorator only
satisfies `types.FS` for free by embedding it. Optional capabilities are not part
of `types.FS` — `drivers/types/fs.go:28-31` says so outright — and are lost unless
forwarded: `types.MountedArchiveDirFS` (asserted at `nestfs.go:64`),
`fs.GlobFS` (`nestfs.go:62`), `AbsPathGetter` (`nestfs.go:63`),
`types.ComparableFS`, `types.DeviceInfoGet` (`nestfs.go:65`), `types.CopyFileFS`,
the watcher. The conformance suites in `drivers/testing` are the check that a
forward exists; each decorator's test must run them against the decorated file
system, not only against the inner one.

## Parameter names

| Parameter       | Owner         | Notes                                                                                                                                                                                                                                      |
| --------------- | ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `ufs.ro`        | `readonlyfs`  | Emitted today (`drivers/decorators/readonlyfs/readonlyfs_urigetfs.go:36`), not consumed. The decorator name is still `readOnly` (`drivers/decorators/readonlyfs/readonlyfs.go:42`); it becomes `ro`.                                       |
| `ufs.fault`     | `faultfs`     | Name unchanged (`drivers/decorators/faultfs/faultfs.go:48`); claimed instead of options-only. `drivers/decorators/faultfs/faultfs_urigetfs.go:31` emits the options entry only.                                                            |
| `ufs.telemetry` | `telemetryfs` | Name unchanged (`drivers/decorators/telemetryfs/telemetryfs.go:82`); `drivers/decorators/telemetryfs/telemetryfs_urigetfs.go:31` emits the options entry only.                                                                             |
| `ufs.options`   | construction  | Merged: `uri.OptionsParam` (`drivers/uri/uri.go:171`), consumed at `construct.go:541`.                                                                                                                                                     |
| `ufs.checksum`  | `httputil`    | Already prefixed (`internal/httputil/httputil.go:44`); the HTTP-shaped drivers must declare it.                                                                                                                                            |
| `ro`            | mount point   | **Not** a parameter any more. #700 made a bare `ro` a mount point (`construct.go:228-235`, `construct_test.go:171`). The nest's read-only flag is `ufs.ro` (`construct.go:52`).                                                            |
| `ro` in fstab   | mount flag    | Stays bare and outside the URI: `fstabReadOnly` (`construct.go:66`), applied at `construct.go:490`, where it becomes a `MountOption` named `readOnlyOption` (`construct.go:38`), which `defaultRootSpec` also passes (`construct.go:532`). |

The renames happen in one step, step 5, with no alias window: the decorator name
`readOnly` becomes `ro`, and the emitted pair stays `ufs.ro`. Comment 4645985365
permits the break. The three constants that must move together are
`readOnlyOption` (`construct.go:38`, the `MountOption` name an fstab `ro` line
turns into at `construct.go:491` and that `defaultRootSpec` passes at
`construct.go:532`) and the decorator's `optionName` (`drivers/decorators/readonlyfs/readonlyfs.go:42`);
`roQueryParam` (`construct.go:52`) already spells `ufs.ro` and does not move.
Renaming the decorator without `readOnlyOption` leaves every fstab `ro` line and
every rootless mount spec failing `cannot find a ufs file system decorator for
option %q, option names are case sensitive` (`register.go:266`).

- `readonlyfs.URI()` (`drivers/decorators/readonlyfs/readonlyfs_urigetfs.go:31-41`) stops appending an options
  entry and emits `?ufs.ro=true` once. The two spellings it emits today still
  produce one wrapper, because `decorate` rejects a decorator listed more than
  once with `file system decorator %q is listed more than once` (`register.go:304`).
  After step 5 both spellings are claims, so a leftover is a construction error at
  that same check rather than a silent second wrap. Keeping the pair at one is
  what the consumption rule is for.
- `readWrapFS.URI()` (`drivers/wrappers/readwrapfs/readwrapfs_urigetfs.go:42`, comment at `:36`) emits
  `?ufs.ro=true` as well, so a readWrap view round-trips into a nest decorated
  read-only. That is the supported path — PR #660 already dropped readWrap in
  favor of the read-only decorator — and the change is stated in the release notes
  because the reopened value is a nest, not a `readWrapFS`.
- Three emitters write `ufs.ro=true` and none of them consumes it.
  `drivers/core/angryfs_urigetfs.go:33` and `drivers/core/nullfs_urigetfs.go:33`
  use it to describe what those drivers always are: neither parses any query
  parameter, `newAngryFS` (`drivers/core/angryfs.go:61`) ignores everything but the
  scheme. `drivers/wrappers/keyguardfs/keyguardfs_urigetfs.go:38` emits it for a
  wrapper that registers nothing. Today the pair is dropped as a marker at
  `construct.go:552`, so the angry and null trips close by accident: the reopened
  driver is read-only because it always is. A keyguard URI instead round-trips into
  a plain nest, which is wrong, and is what the reopen assertion in step 6 is for.
  Step 5 gives the pair an owner and therefore decides all three cases; see open
  decision 5.
- `drivers/testing/urigetfs.go` is another matter: `uriGetReadOnly` (`:94`) asserts
  on `u.Query().Get("ufs.ro")` at `:111` and `:115` and already expects the
  prefixed spelling, but its comment at `:120-125` says unprefixed spellings are
  deliberately not forbidden, and neither it nor `uriGetStable` (`:41`) reopens
  the URI. Adding the reopen step means editing that file, which the owner has
  closed to changes; see open decision 6.

Duplicate claims: `matchDecorator` (`register.go:258`) looks a claim up in the
decorator map by `Name`, so two decorators cannot claim the same parameter under
their own names. A tie is only possible when one decorator lists a second name in
`Params` that is another's `Name`. Resolution copies `matchDriver`
(`register.go:235`): the lower `Priority` wins; equal priorities return an error
of the same shape, `ambiguous decorator match for %q, both %q and %q both have a
priority %d`.

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
`drivers/boltfs/boltfs.go:133`, with the two rejections at `:143` and `:155`).
The HTTP-shaped drivers that go through `httputil.ParseURI` declare `checksum`.
Those driver-side rejections stay as they are: a driver still validates its own
parameters, because its `CreateFunc` can be called directly with any string.

## Changes

| File                                                           | Change                                                                                                                                                                                                                                                                                                                 |
| -------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `construct.go`                                                 | `buildNestFS`, `decorateNest`, `openNestFS` widened; the claim loop and the central unknown-parameter check; `readOnlyOption` renamed to `ro`; `New` (`:236`), `newDecoratedFS` (`:540`) and `newFromMountSpec` (`:567`) widened to `types.FS`.                                                                        |
| `register.go`                                                  | `Decorator.Params`, `Decorator.Priority`, `CreateFunc` over `types.FS` and `uri.Params`; validation in `RegisterDecorator` (`:168`); `Driver.Params`; claim lookup and the ambiguous-match error in `matchDecorator` (`:258`); `decorate` (`:290`) over `types.FS`.                                                    |
| `nestfs.go` + `nestfs_<interface>.go`                          | `mountMap` (`:79`) to `map[string]*mountEntry`; the thirteen routing sites move from `mountFS.fsys` to `outer`; `addMount`/`addAutoMount` signatures (`:385`, `:394`); `autoMounted` moved onto the entry (`:307-308`).                                                                                                |
| `nestfs_urigetfs.go`                                           | `URI()` (`:25`) builds the mount query with `uri.AddParameter` instead of `u.Query()`/`vals.Set`/`vals.Encode()` (`:31`, `:39`, `:42`).                                                                                                                                                                                |
| `drivers/decorators/readonlyfs`                                | Option name `readOnly` → `ro` (`drivers/decorators/readonlyfs/readonlyfs.go:42`); `URI()` emits `?ufs.ro=true` once (`drivers/decorators/readonlyfs/readonlyfs_urigetfs.go:36`, `:40`); the struct (`drivers/decorators/readonlyfs/readonlyfs.go:90`) embeds `types.ReadFS` at `:91` and gains the rest of `types.FS`. |
| `drivers/decorators/faultfs`, `drivers/decorators/telemetryfs` | Embed `types.FS`; forward the optional capabilities each keeps today; `URI()` claims its parameter.                                                                                                                                                                                                                    |
| `drivers/wrappers/readwrapfs`                                  | `URI()` emits `ufs.ro` through `uri.AddParameter`. No registration.                                                                                                                                                                                                                                                    |
| `drivers/boltfs`                                               | Declare `Params` for the five `ufs.` parameters (`drivers/boltfs/constants.go:28-42`).                                                                                                                                                                                                                                 |
| HTTP-shaped drivers (`httpfs`, `gcsfs`, `gitfs`, …)            | Declare `Params: []string{"checksum"}` where they read `ufs.checksum`.                                                                                                                                                                                                                                                 |
| `construct_test.go`, `uri_test.go`                             | `TestNewKeepsUFSParameters` (`:146`) becomes `TestNewConsumesUFSParameters`; `TestNewAppliesURIOptions` (`uri_test.go:157`) changes with the mount map; the reduce-loop table and the two consumption assertions.                                                                                                      |

`drivers/all/all.go` needs no change: the three decorator packages are already
blank-imported there (`drivers/all/all.go:51-53`), and
`TestAllRegisteringPackagesImported` (`drivers/all/all_test.go:237`) keeps that
honest.

## Testing

- `TestNewKeepsUFSParameters` (`construct_test.go:146`) is rewritten as
  `TestNewConsumesUFSParameters`. It pins copy-not-consume today: it asserts the
  driver is asked to open a URI that still carries `ufs.checksum=sha256%3Aabcd`.
  After step 5 it asserts the reduced URI the driver is asked to open, which no
  longer carries that pair.
- The two consumption assertions, one test each: no `ufs.`-prefixed pair survives
  in the reduced URI unless the matched driver declares the name in
  `Driver.Params`; and a `CreateFunc` receives only the keys its decorator
  declares in `Params`.
- New table in `construct_test.go` for the reduce loop:
  `memory://a?ufs.ro=true&b=bolt://c?ufs.nosync` opens with the mount decorated
  and bolt's own parameter intact;
  `memory://a?ufs.options=[{ro: true}]` refuses `Create` exactly once rather than
  applying the decorator twice;
  `memory://a?ufs.nosync` errors with the unknown-parameter message naming
  `nosync`; a mount path beginning with `ufs.` is impossible and the error says so
  (`construct.go:228-235`).
- The mount hole from "What the URI does today" becomes a regression test:
  `memory://x?ufs.options=[{readOnly:true}]&sub=memory:` must refuse
  `Create("sub/escapes")` once step 4 puts decoration around the nest, and
  `TestNewAppliesURIOptions` (`uri_test.go:157`) is updated in step 3 to assert the
  tag on the mount's `outer`, not on `nFS.mounts.m["data"].fsys` (`:186`).
- Signed-query tests already exist for the helpers —
  `TestAddRemoveParameterRoundTrip` (`drivers/uri/uri_test.go:367`),
  `TestAddParameterKeepsSignedQuery` (`:409`). The new one is for the emitter: a
  fixture query whose order and escaping are asserted byte for byte after
  `nestFS.URI()`, with a mount URI that carries `ufs.checksum`.
- `TestSplitURIOptions` (`uri_test.go:103`) and `TestNewAppliesURIOptions`
  (`uri_test.go:157`) keep covering the options list;
  `TestParseBoltOptions` (`drivers/boltfs/boltfs_edge_test.go:328`) and
  `TestMakeBoltFSInvalidOptions` (`drivers/boltfs/boltfs_edge_test.go:426`) keep
  covering bolt's own parsing.
- Decoration-around-nest behavior: a decorator on a nest with two mounts sees
  both, and a mount-level `?ufs.ro=true` refuses writes routed from the parent,
  including writes made through the mount's own directory.
- Round trip: the promise at `drivers/types/uriget.go:23-25` needs an assertion.
  Where it goes is open decision 6, because the natural host is
  `uriGetStable`/`uriGetReadOnly` in `drivers/testing/urigetfs.go` (`:41`, `:94`),
  which is closed to changes.
- Decoration is a behavior change for the suites: a decorator's driver test must
  run the `drivers/testing` suites against the decorated value, which is what
  proves each optional-capability forward exists.

## Implementation plan

Six steps, in this order. The first two were steps 1 and 2 of the first version
of this plan; both merged, and step 1 merged as something different from what was
written there.

### Steps 1 and 2: closed, and what actually merged

- The `FS` interface move did not happen as written. `types.FS`
  (`drivers/types/fs.go:22`) was introduced as the definition and the root
  `ufs.FS` (`ufs.go:58`) became `type FS interface { types.FS }`, not
  `type FS = types.FS`. Comment 4645717461 confirms that is the right shape:
  signatures say `types.FS`, and this design has no step that touches `ufs.go`.
- The take/tack pair merged as #699: `uri.RemoveParameter`, `uri.AddParameter`,
  and `internal/httputil/uriparams.go` as the shared walk, with `httputil.ParseURI`
  moved onto it. #700 put the prefix on `ufs.options` and `ufs.ro`, and #698 made
  reporting a URI a requirement. The plan below assumes all of that.

### Step 3: mounts become entries

- Files: `nestfs.go`, the `nestfs_<interface>.go` files, `construct.go`.
- Contracts: `mountEntry` in place of a bare `*nestFS` in `mountMap`
  (`nestfs.go:81`); `addMount` (`:385`) and `addAutoMount` (`:394`) take the
  entry; `openNestFS` (`construct.go:290`) returns the nest and its surface.
- URL parameters: none read yet; decorators stay on the options list.
- Behavior: none. Every mount is undecorated, so `outer` is the nest itself.
- Tests: the existing nest tests, and `TestNewAppliesURIOptions`
  (`uri_test.go:157`) reaching the mount through the entry.
- Does not: decorate anything. This step is mechanical and is the one that makes
  step 4 small.

### Step 4: decoration around the nest

- Files: `construct.go`, `register.go`, the three decorator packages.
- Contracts: `buildNestFS`, `decorateNest`, `openNestFS` as above; `CreateFunc`
  over `types.FS` and `uri.Params`, with `NewDecorator[T]` following
  (`register.go:98`); `New` (`construct.go:236`) and `newFromMountSpec`
  (`construct.go:567`) widen from `types.WriteFS` to `types.FS`, since a decorated
  nest surface otherwise loses methods as soon as it passes through them —
  `FSBuilder.Build` (`construct.go:337`) already returns `FS` and `nestFS` already
  asserts `types.FS` (`nestfs.go:61`), so this is the last narrow link.
- URL parameters: none claimed yet.
- Behavior: a decorator now sees the mounted tree.
- Tests: the two-mounts test, the `sub/escapes` regression, and the decorator
  suites run against the decorated value.
- Does not: take a parameter from a URI, or rename one.

### Step 5: claims and consumption

- Files: `register.go`, `construct.go`, `drivers/uri`, `drivers/boltfs`, the
  HTTP-shaped drivers, `nestfs_urigetfs.go`.
- Contracts: `Decorator.Params`, `Decorator.Priority` and `Driver.Params`; the
  claim lookup and the ambiguous-match error in `matchDecorator`
  (`register.go:258`); `nestFS.URI()` over `uri.AddParameter`.
- URL parameters: the consume loop and the central unknown-parameter error; the
  decorator name `readOnly` becomes `ro` and the emitted pair stays `ufs.ro`, with
  no alias window. `roQueryParam` (`construct.go:52`) already spells `ufs.ro` and
  does not move; `readOnlyOption` (`construct.go:38`) does.
- Behavior: this is the step that changes what a URI means.
- Tests: the reduce-loop table, the two consumption assertions, and
  `TestNewConsumesUFSParameters`. An fstab `ro` line and a mount spec whose root
  carries no decorator still construct, which is the regression that travels with
  `readOnlyOption`.
- Does not: register `drivers/wrappers/*`, or move driver parsing into the
  registrar.

### Step 6: prove the round trip

- Files: whichever files its assertions force, plus `drivers/testing/urigetfs.go`
  if the owner opens it (open decision 6).
- Contracts: none.
- URL parameters: none; step 5 fixed the names.
- Behavior: none intended. A driver that cannot reopen its own `URI()` is a bug
  this step finds.
- Tests: reopen the file system from `URI()` and re-run the same assertions, so a
  decorator that emits a parameter it cannot consume fails.
- Does not: rename a parameter, change a contract, or add a driver. Its only
  content is the assertion that the round trip closes.

### URL parameter ordering

A decorator tacks its parameters at the end of the query, in the order its
`Params` declares them, through `uri.AddParameter` (`drivers/uri/uri.go:126`),
which replaces an existing pair of the same name in place rather than appending a
second one. Nothing is sorted, and no pair already in the query is moved or
re-encoded, because the URI may be signed over its exact query string
(`internal/httputil/httputil.go:424-428`).

Claims are collected in a deterministic order, decorators by name and each
decorator's names in declaration order, and pairs are taken left to right. The
consume loop takes at least one pair per pass, so it is bounded by the number of
pairs in the query. A value is percent-encoded once, by `AddParameter`, and
decoded once, by `RemoveParameter`.

Decorators are applied in the order their claims were collected: the
`ufs.options` entries first, in list order, then the pairs in query order. That
is today's order, where `splitURIOptions` (`uri.go:41`) removes the list before
anything else reads the query, and `decorate` wraps the first option first and the
rest around it (`register.go:286`). `readonlyfs.URI()` today emits both
`ufs.ro=true` and an options entry (`drivers/decorators/readonlyfs/readonlyfs_urigetfs.go:36`, `:40`); step 5
replaces the two with a single pair.

## Known limitations

- A mount path cannot be a parameter name that a decorator claims. A mount named
  `fault` collides with `ufs.fault` only in the prefixed spelling, and
  `construct.go:228-235` already forbids a mount path that begins with `ufs.`. The
  bare-spelling bug recorded in the first version of this spec (`memory:?ro=memory:`
  dropping the mount silently) is gone: a bare `ro` is a mount point now and an
  unusable one, which fails loudly.
- Percent-encoding is normalized for the parameters this design writes, and
  preserved byte for byte for the parameters it does not. A URI whose signature
  covers a `ufs.` parameter a decorator consumed cannot be verified afterwards;
  that is inherent to removing a parameter from a signed query.
- Decorators reach only what `types.FS` and their own forwards expose. A consumer
  that type-asserts an optional capability off the value `ufs.New` returns gets
  nothing back unless the outermost decorator forwards it, and `types.FS` does not
  include the optional capabilities at all (`drivers/types/fs.go:28-31`).
- `mountArchive` (`nestfs.go:399`) builds an archive mount through
  `newBaseFS`/`newBaseFSFromFile` (`construct.go:381`, `:404`) and `makeNestFS`
  (`nestfs.go:603`) directly. It stays that way: an archive's contents are not
  decorated, so a read-only nest can still be written through a `.zip.d` mount.
  Closing that gap is a separate design.
- The 33 methods of `nestFS` are the upper bound on what a decorator wrapping a
  nest can forward.

## Out of scope

- Registering `drivers/wrappers/*`, keyguardFS included.
- Nesting decorators inside decorators.
- A UI or helper for editing a URI's parameters.
- Anything in `agentbox` that consumes these URIs.
- Widening `types.FS` to include the optional interfaces listed at
  `drivers/types/fs.go:28-31`; that is #709's work and this design does not depend
  on it.

## Open decisions

1. `Params` on `Decorator` is a name list; a decorator with a structured
   per-parameter schema (allow and deny lists, as the RestrictFS spec's phase 4
   would want) has no place to declare it. Does that get a schema table now, or
   stay in `DecodeOptions[T]`? Recommendation: stay in `DecodeOptions[T]`; a
   schema table is only worth its cost once a second consumer exists.
2. Are mount URIs in a nest's query reduced by the parent's decorator claims
   today, and is that intended? `construct.go:250-273` copies prefixed
   parameters onto the base and leaves the mount values alone, which this design
   preserves: a mount's own parameters belong to the mount. Recommendation: keep
   it, and say so in the `New` doc comment.
3. Does a `MountSpec`'s `Options` get the same unknown-name rule as the URI, or
   does only the URI get the central check? A mistyped section in a mount spec is
   as silent today as an unknown `ufs.` parameter is. Recommendation: the same
   rule, one error path, applied in `decorate` (`register.go:290`).
4. Does the fstab `ro` flag move to `ufs.ro`? It is a mount property
   (`construct.go:66`, `:490`), never a URI parameter, and step 5 renames only the
   decorator's name. Recommendation: leave it bare.
5. Do the drivers that emit `ufs.ro` without parsing it declare it in
   `Driver.Params`, so the central check leaves the pair alone, or does the
   read-only decorator claim it and wrap a driver that is already read-only? The
   second is one uniform rule and a redundant layer; the first means a URI can
   carry a pair nothing acts on. keyguardFS keeps emitting `ufs.ro` until PR #690
   turns it into a registered decorator, so its URIs cannot round-trip meanwhile.
   Recommendation: let the decorator claim the name, and drop the pair from
   `angryFS` and `nullFS`, whose read-only-ness is inherent — a URI should carry
   only what changes construction.
6. Where does the round-trip assertion live? `uriGetStable` and `uriGetReadOnly`
   (`drivers/testing/urigetfs.go:41`, `:94`) are the natural host, but the owner
   has closed `drivers/testing/` to changes, and that file's comment at `:120-125`
   deliberately tolerates unprefixed spellings. Options: ask for that one file to
   be opened; put the assertion in a root-package test over every registered
   decorator and driver; or leave step 6 with per-decorator tests only.
   Recommendation: ask for the file to be opened — a per-package copy of the
   assertion is what `drivers/testing` exists to avoid.

## Changes since the rough spec and since the review

The rough spec in
[comment 7031027085](https://github.com/cloudfra/ufs/issues/694#issuecomment-7031027085)
proposed `Params []string` on `Decorator`, a `consumeUFSOptions` helper in
`uri.go` reusing the pair-by-pair scan, and a reduce loop inside
`newDecoratedFS`. All three survive. Three things changed after the issue's
answers: decoration moves outside the nest, which drags in the mount-map widening
that the rough spec did not anticipate; `ro` and `options` are renamed under the
prefix; and the unknown-parameter rule became central with a `Driver.Params`
exemption instead of a per-driver convention.

Review 5989349599 changed five more things: the surface is `types.FS`, which is no
longer a question; the `any` passed to `CreateFunc` becomes a parameter map, which
is what makes consumption checkable; the compatibility window for `options` and a
bare `ro` is gone, because comment 4645985365 permits breaking changes; the bare
`ro` mount flag is a decision rather than an open question; and the implementation
plan became a numbered sequence of steps with a subsection on URL parameter
ordering.

What `main` merged since then removed more than it added. The parameter-map type
this spec invented (`URIParams`) is deleted in favor of `uri.Params`
(`drivers/uri/uri.go:47`), because #699 and #700 had already landed a map with the
same semantics and the prefixed keying `httputil.ParseURI` uses. The take and tack
helpers this spec proposed to write are merged as `uri.RemoveParameter` and
`uri.AddParameter`, so the plan lost two steps and gained one: mounts become
entries before anything decorates them. The `FS` interface step is closed by the
way `types.FS` actually landed — the root name stays
`type FS interface { types.FS }` (`ufs.go:58`), per comment 4645717461. The two
URI claims this spec opened with were re-run against `d94c72e`: the mount hole is
still there, and the bare-`ro` hole is not.
