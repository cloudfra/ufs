# ufsforagents — Design Spec

Status: draft for review. Builds on the stubs in `cc62a92 Add stub (#494)`,
`645e5aa Add experimental posix agent toolchain stub (#493)` and `a91a0ae ufsforagents (#496)`,
which are on `main` and in no release tag, so their signatures may still change.

## Goal

Give an AI agent a contained, terminal-shaped view of a ufs mount. The agent types the
POSIX commands it already knows (`ls`, `cat`, `grep`, `mv`); each command is translated into
ufs API calls against the mounted file system. No FUSE, no host shell, no root.

Three integration surfaces, in the order they will be built:

1. `agenttools/posixcli` — a Go package that parses a command line and executes it against a
   file system. It takes no third-party dependency beyond the parser module
   `mvdan.cc/sh/v3/syntax`; everything else it uses is this repo's own code, as listed in
   [Architecture](#architecture). This is the whole product; everything else is a wrapper.
2. `cmd/ufsforagents` — the binary. One command per process invocation, so any agent
   framework that can spawn a process can use it.
3. Agent-framework adapters. For Google ADK Go, an in-process `functiontool` wrapper in
   `agenttools/adk` (a separate module), and an MCP stdio server for agents in other
   languages.

## Non-goals

* Not a shell. No control flow, no expansion beyond path globs, no host command execution.
* Not a POSIX conformance layer. Commands implement the flags agents actually type, and say
  so when a flag is unknown instead of ignoring it.
* Not a general-purpose remote file API. Mounting and unmounting stay with the caller: the
  mount chosen by `-uri` is the security boundary.
* Not a replacement for `host.Mount`. FUSE and ProjFS are still the right answer when the
  agent runs arbitrary programs; this is for the common case where it only needs files.

## Why an emulated terminal instead of a typed tool API

A typed API (`read_file`, `write_file`, `list_dir`, …) needs a schema per operation and
re-teaches the model paths and permissions it already understands. The stub's premise is that
the model has far more prior for `ls -l` and `grep -rn pattern dir` than for our own verb
set, and that a single string-returning tool keeps the tool list small. The cost is parsing
untrusted model output, which is what the parse layer is for. Both surfaces are offered:
the `posixcli` core exposes a command registry, so a caller that wants one tool per command
can generate typed schemas from it (see [ADK adapter](#adk-adapter)).

## Prior art and competitive landscape

Nothing here is a drop-in. The closest projects solve one half of this problem: shell parsing
without a sandboxed file system, or a sandboxed file system without a shell.

| Project | What it gives | How it differs |
|:--|:--|:--|
| [`mvdan.cc/sh/v3`](https://pkg.go.dev/mvdan.cc/sh/v3/interp) `syntax` + `interp` | A POSIX/Bash parser and an in-process shell interpreter with `OpenHandler`/`StatHandler`/`ReadDirHandler2`/`ExecHandler` seams for redirecting I/O. No third-party dependencies. | The nearest thing to this design, and the source of our parser. `interp`'s `DefaultExecHandler` runs commands on the host and its builtins cannot be enumerated or disabled, so it is a parser for us, not an executor. See the parser section. |
| [MCP filesystem server](https://github.com/modelcontextprotocol/servers) | The reference MCP server over the host file system: `read_file`, `write_file`, `list_directory`, `search_files`. | Typed tools, not POSIX; host paths, not a mount. This is the interface the stub argues against, and the shape a future `agenttools/mcp` would improve on by mounting a URI. |
| [MCP Go SDK](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp) | Official `Server`, `AddTool`, stdio/streamable transport. | Transport and tool plumbing only; no file semantics. Candidate for phase 8. |
| [Google ADK Go](https://pkg.go.dev/google.golang.org/adk/v2/tool/functiontool) `functiontool`, [`mcptoolset`](https://pkg.go.dev/google.golang.org/adk/v2/tool/mcptoolset) | Typed Go function tools with inferred JSON Schemas, and an MCP client that turns a server's tools into ADK tools. | The consumer side. `functiontool` is the v1 adapter; `mcptoolset` is why an MCP server here needs no ADK-specific glue. |
| [gVisor](https://github.com/google/gvisor) | A Linux kernel in Go intercepting syscalls, used to sandbox containers. | Does the same job at the syscall boundary with real binaries. Needs a container runtime and a kernel surface, which is what "without FUSE mounts" in the stub doc is rejecting. |
| [E2B](https://github.com/e2b-dev/e2b), [Daytona](https://github.com/daytonaio/daytona) | Sandboxed cloud runtimes exposing a filesystem + command API to agents. | Real VMs/microVMs behind the file system. Strong isolation, but the file system is a real one; the point here is that the file system is a `gs://` prefix, an archive, or a nested composition. |
| [wazero](https://pkg.go.dev/github.com/tetratelabs/wazero) + [WASI](https://www.wasi.dev) | A Go WASM runtime whose WASI layer implements POSIX-style `open`/`readdir`/`stat` over a host `fs.FS`-like mapping. | The same idea one level down: a POSIX surface implemented in Go with no kernel. Heavier than needed — it presumes a WASM binary to run — but it is the model for implementing POSIX calls against a virtual tree. |
| [go-internal testscript](https://github.com/rogpeppe/go-internal/testscript) | Runs scripts of shell-ish commands against a temp dir for Go tests. | Proves the "emulated commands as a testable surface" pattern works, and its `.txtar` style is the model for our command tests. |
| [SWE-agent](https://arxiv.org/abs/2405.15793) | Evidence that an agent-computer interface shapes an agent's success far more than raw capability; argues for compact, agent-shaped commands. | Not a file system, but the argument for why the tool set here is a small, deliberately incomplete command table with good errors rather than a full shell. |
| Go stdlib [`os.Root`](https://pkg.go.dev/os#Root) | A rooted directory tree that refuses escaping paths. | The containment idea, at one directory. Our root is an arbitrary `fs.FS`, and `fs.ValidPath` already gives most of the rule. |

The gap this fills: a POSIX-shaped surface whose file system is a UFS URI. Shell emulators
assume a host file system; agent file-system tools assume typed calls on a real directory;
sandbox runtimes assume a VM. `ufsforagents` is the combination of `mvdan.cc/sh`-grade parsing
with ufs's mount/decorator model, which no upstream project provides.

## Architecture

```text
agenttools/
  posixcli/
    posixcli.go     Session, Result, Run — the entry point and command registry
    parse.go        tokenizer and pipeline/redirect parser; rejects unsupported syntax
    flags.go        flag parsing shared by the commands
    exec.go         pipeline execution, redirection wiring, output budget
    errors.go       ErrUnsupportedShell and the push-back messages
    <cmd>.go        one file per command (ls.go, cat.go, grep.go, mv.go, …)
    help.go         built-in help, generated from the registry
  adk/              separate module: ADK Go adapter (go.mod, adk.go, toolset.go)
  mcp/              separate module: MCP stdio server (later phase)
cmd/ufsforagents/ufsforagents.go
```

`posixcli` depends on the base `ufs` package, `ops`, `drivers/polyfills`, and
`mvdan.cc/sh/v3/syntax` for tokenizing. `syntax` is the only new third-party import, pinned to
a version in the `go.mod` row of [Changes per file](#changes-per-file). Its own dependency
footprint is not asserted here: the phase-1 PR runs `go list -deps mvdan.cc/sh/v3/syntax`
against the pinned version and pastes the output into its PR body, since that is the claim a
reviewer should be able to check against a tree. The interpreter package is deliberately not
used, for the reasons in the parser section.

### Entry point

The stub's `Run(_ context.Context, _ fs.FS, _ string) (string, error)` stays as the stateless
one-shot form used by the binary — what it can report about a failure is open decision 3 — and
gains a stateful sibling for in-process callers that want `cd` and a stable output budget
across calls.

```go
package posixcli

// Session is one agent's view of a mounted file system: the mount, the working
// directory, and the output budget. A Session is not safe for concurrent Run
// calls; callers serialize them.
type Session struct {
        fsys   fs.FS
        cwd    string
        limits Limits
}

// Limits bounds what one command may read and print, so a runaway find or
// grep cannot flood the model's context or the process heap.
type Limits struct {
        MaxOutputBytes int           // default 32 KiB; stdout and stderr separately
        MaxFileBytes   int64         // default 4 MiB; larger files are skipped by grep and reported
        Timeout        time.Duration // default 30s per command, enforced with context.WithTimeout
        MaxResults     int           // default 2000 entries for ls, find and grep
}

// NewSession returns a Session rooted at "." in fsys. fsys is probed for the
// optional interfaces each command needs, so a read-only mount is a valid
// argument: write commands then report a permission error.
func NewSession(fsys fs.FS, limits *Limits) *Session

// Run parses and executes one command line. The error return is reserved for
// failures the caller must handle (a nil file system, an expired context);
// everything the model could get wrong — a missing file, an unknown flag, an
// unsupported construct — is reported in the Result, not as an error.
func (s *Session) Run(ctx context.Context, cmdline string) (*Result, error)

// Chdir moves the working directory. Run reports the same error text for an
// unknown directory.
func (s *Session) Chdir(name string) error

// Result is the terminal output of one command, in a shape a tool caller can
// marshal straight into JSON.
type Result struct {
        Stdout    string
        Stderr    string
        ExitCode  int
        Truncated bool
        Notice    string // a hint for the model: a truncation pointer, or a push-back
}
```

`Run(ctx, fsys, cmdline) (string, error)` stays as a wrapper that builds a throwaway `Session`,
calls it, and returns `Stdout` plus `Notice` when the exit code is zero. How a non-zero exit
code reaches a one-shot caller is open decision 3. Keeping the wrapper means the existing
`cmd/ufsforagents/ufsforagents_test.go` cases keep compiling and the binary stays a one-liner.

### Error contract and exit codes

The split matters more than it looks: a tool call that fails as a framework error gives the
model nothing to read, so it cannot recover. A command failure is therefore *data*, printed to
`Stderr` with a non-zero `ExitCode`, and `Session.Run` returns a nil error.

| Exit code | Meaning | Examples |
|:--|:--|:--|
| 0 | success | |
| 1 | command ran and failed | `ls: no such file or directory: missing.txt`, `cannot write to a read-only mount` |
| 2 | the command line is unusable | unknown command, unknown flag, unsupported shell construct |
| 3 | ufs or I/O failure the model cannot fix | a mount that stopped responding |

Error text is written for a reader that will try again: name the path, name the flag, and name
the fix. `cannot write to a read-only mount; mount it with ?options=[{readOnly: false}] or use a writable mount such as memory:` is the target shape, which follows the
repo's rule of giving raw failures a cause and a resolution.

### Push-back on clever command lines

The stub asks for push-back when the generated command is too clever. That is a distinct
error, `ErrUnsupportedShell`, carrying a message that says the environment is emulated, quotes
the offending token, and offers the supported alternative:

```text
unsupported: "&&" — this is an emulated terminal, not a shell.
Run one command per call. The result already carries stdout and an exit code.
```

The rejected constructs are `;`, `&&`, `||`, `!`, background `&`, `$(...)`, backticks,
`${...}`, here-documents, `2>`/`&>`, aliases, and any command name outside the registry. The
message names the first one found in the string, before tokenizing further, so the model is
never offered a partially parsed command.

### Supported syntax

| Syntax | Support |
|:--|:--|
| Words and quoting | Single and double quotes, and backslash escapes inside double quotes. No ANSI-C quoting. |
| Path globs | `fs.Glob` against the mount, one `*` per path element, expanded only in argument position. A pattern matching nothing is passed through literally, the way `sh` does, so `cat *.txt` reports a real error. |
| Pipeline `\|` | Allowed between a data command and a filter. Stages are joined in memory with a bounded reader. |
| Redirect `>`, `>>` | To a path at or below the mount root only. Creates parents with `MkdirAll`; refuses a directory target; capped by `Limits.MaxFileBytes`. |
| Everything else | `ErrUnsupportedShell`. |

`Stdin` has no host wiring: the only input is a pipeline stage or a file in the mount.
`/dev/null` and `/dev/stdin` are recognized as special files so a model's reflexive
`>/dev/null` succeeds quietly.

### Parser: `syntax` for words, our own dispatch for meaning

The command line is parsed with `mvdan.cc/sh/v3/syntax` configured as
`syntax.NewParser(syntax.Variant(syntax.LangPOSIX), syntax.KeepComments)`, walked for
`CallExpr`/`BinaryCmd`/`Redirect`, and every node outside the table above is refused with
`ErrUnsupportedShell`. Tokenizing is the part with the edge cases (nested quotes, line
continuations, `$` inside double quotes, comment vs assignment) and the part most likely to be
fed hostile input, so it is borrowed; the semantics stay ours. The module is BSD-3-Clause and
pulls in no third-party packages of its own, so the repo's dependency surface grows by one
module, not a tree.

`mvdan.cc/sh/v3/interp` is the obvious second step and is deliberately rejected for v1. Its
handler seams map cleanly onto ufs — `OpenHandler`, `StatHandler`, `ReadDirHandler2`,
`AccessHandler`, `ExecHandler` — and an io/fs-backed implementation is straightforward. The
problems are the defaults:

* `interp.ExecHandler`'s `DefaultExecHandler` resolves commands with `LookPathDir` and runs them
  with `exec.CommandContext`, so a missed seam executes on the host.
* Builtins run inside the interpreter with no handler seam. `interp.IsBuiltin` answers whether a
  name is a builtin but does not let a caller enumerate or disable the set; the package carries
  an upstream `TODO` asking for exactly that API. So `cd`, `echo`, `test`, `export` and friends
  would be interp's implementations rather than ours, and `cd` is state we must own.
* It executes whole scripts: assignment semantics, `set`, expansion of unset variables to empty
  strings. That is more shell than the stub asks for, and every extra behavior is a promise to
  an agent that we will keep.

So: `syntax` for the parse, `Session.Run`'s registry for execution. If a later phase wants
`interp`, the guardrails are a custom `ExecHandler` that never execs, our own builtin allowlist,
and io/fs handlers for every file access — at which point the registry above is most of the way
there already.

### Path discipline

The mount root is the whole universe. Names are validated with `pathutil.Validate`, so
`fs.ValidPath` rules apply and `..` is an error. One deliberate deviation from `fs.FS`: a
leading `/` is accepted and stripped, because models write absolute paths and `ls /` must do
what it looks like. A path that escapes after cleaning is refused with the same text a real
terminal does not have: `outside of the mounted file system`.

`pwd` prints the working directory as a mount-relative path, and `pwd -u` prints
`types.URIGet`'s URI so the agent can tell whether it is on a `gs://` bucket, an archive, or a
nested composition. `ls` and `find` print mount-relative names only; the binary's `-json` mode
adds an `absPath` field when `ufs.AbsPath` resolves, matching `cmd/walk`.

Symlinks are reported, never followed: `ls -l` shows the link, `stat` reports `Type()`, and
`cat` follows only what the backing driver itself follows. `ln` is out of scope — the write
side of links is not part of `types.WriteFS`.

### Capability probing, not driver assumptions

Every command takes whatever `fsys` it is given and asks for the interfaces it needs, so the
same command table runs against `memory:`, a local directory, an `archive://` mount, `bolt:`
and a `readOnly`-decorated mount. Write commands go through `ops` and `drivers/polyfills`
(`ops.Remove`, `ops.RemoveAll`, `ops.Rename`, `polyfills.Create`, `polyfills.MkdirAll`), never
through a driver's concrete type, and never through a sibling polyfill.

A command whose capability a file system genuinely lacks says so instead of guessing:

```text
cannot rename: this file system supports neither rename nor copy-and-remove
```

The `readOnly` decorator answers `fs.ErrPermission`; the message mapped onto it names the
decorator and points at a writable mount, because the single most likely agent mistake is
trying to `touch` inside a read-only archive mount.

### Output budget

Agents get truncated output and must be told it happened. When `MaxOutputBytes` or
`MaxResults` is hit, output is cut at the last whole line, `Truncated` is set, and `Notice`
says what to do next:

```text
output truncated: showing the first 512 of 4183 lines (32 KiB limit). Narrow the path, or use ls -d, grep -l, or find -maxdepth 2.
```

Recursive commands print a deterministic, sorted order. This is not cosmetic: `memFS` iterates
a map, so an unsorted `ls` gives the model a different tree every turn and makes a
re-verification useless. `grep` streams with a bounded buffer and never loads a whole file;
files over `MaxFileBytes` are skipped with one line naming them, and a file containing a NUL
byte is reported as binary and not printed, as grep for agents should.

### Command table

Registry entries carry the name, the flag set, a one-line description for `help`, whether the
command mutates the mount (so an adapter can gate it behind a confirmation), and whether it
reads stdin as a filter.

| Command | Flags in scope | Notes |
|:--|:--|:--|
| `pwd` | `-u` | working directory, or the mount URI |
| `cd`, `pushd`, `popd` | | session state; in one-shot mode `cd` reports that state does not persist and points at `-cwd` |
| `ls` | `-l -a -1 -d -R` | `-l` prints mode, size, mtime (RFC 3339), name; sorted |
| `cat`, `head`, `tail` | `head/tail: -n` | binary files are reported, not dumped |
| `grep` | `-r -n -i -l -c -F -E --include` | default fixed-string, `-E` for the standard regex syntax |
| `find` | `-name -type -maxdepth -size -prune` | built on `ops.Walk`, so virtual archive directories (`.zip.d`) are skipped unless `-archives` |
| `wc`, `sort`, `uniq` | `wc: -l -w -c` | usable as pipeline stages |
| `stat`, `du` | `du: -s -h` | `du` uses `humanize`, already a dependency |
| `tree` | `-L` | |
| `mkdir`, `touch`, `truncate` | `mkdir: -p` | `touch` on a missing file creates it |
| `rm`, `rmdir` | `rm: -r -f` | `rm` without `-r` refuses a directory |
| `mv`, `cp` | `cp: -r`; `mv` within one mount goes through `ops.Rename`, across mounts through `ops.RenameByCopy` | `cp -r` treats an archive as an opaque file; see the archive rule below |
| `mounts` | | lists each mount point and the URI resolved for it; read-only, ufs-specific; needs the new API described below |
| `help` | `help <cmd>` | generated from the registry, so it cannot drift |

Explicitly absent from the table: `sed`, `awk`, `curl`, `chmod`, `chown`, `ln`, `dd`, `tar`,
`gzip`, `xargs`, `sh`. Anything the model reaches for there gets `ErrUnsupportedShell` with a
pointer to what is available, which is also what `help` prints first.

Two rows name behaviour that `main` does not provide yet.

`cp -r` cannot get its archive rule from `ops.Rsync`. That function takes no options
(`Rsync(srcFS fs.FS, destFS types.WriteFS, dir string) error`), and its doc states the opposite
default: an auto-mounted archive's contents "are traversed like any other directory, so they
are written to destFS as a directory tree named after the mount point, such as
\"data.zip.d\", not as the archive file itself", with the archive itself copied as an opaque
file in addition (`ops/rsync.go:29-33`). It carries
`// TODO: Prevent archive traversal; see https://github.com/cloudfra/ufs/issues/46` at
`ops/rsync.go:37-38`. So phase 4 builds `cp -r` on `ops.ForEachFilename` and skips any name
under a mount whose name ends in nestFS's archive directory extension
(`archiveDirExt` at `nestfs.go:48`), which is the same choice open decision 5 makes for `find`
and `ls -R`. `cp -r` does not wait on issue #46: the command does the skipping itself rather
than changing shared `ops` behaviour, so the shared decision can be taken on its own schedule.

`mounts` has no API to build on today. The mount table is unexported — `mountMap` at
`nestfs.go:83`, with `mountsUnder` at `:162`, `getMount` at `:175` and `getClosestMount` at
`:186` — and `nestFS.URI()` at `nestfs.go:330` reports the outer URI without the archive
content directories that `mountArchive` adds at `nestfs.go:468`. Phase 2 therefore also adds
the enumeration to the root package:

```go
// MountInfo is one entry of a nestFS mount table.
type MountInfo struct {
        Name string // mount point, relative to the file system's root
        URI  string // the URI resolved for that mount; empty if it reports none
}

// Mount returns the mounts at or below name, sorted by name, including archive
// content directories that ufs mounted for itself. name must satisfy
// [fs.ValidPath]; use "." for the whole table.
func Mount(fsys FS, name string) ([]MountInfo, error)
```

This passes the repo's "no exported API that only tests need" rule: `mounts` is a command the
model calls, and an agent cannot learn that a `data.zip` is being shown to it as a `data.zip.d`
tree unless something can tell it which mounts exist and where each came from.

### cmd/ufsforagents

The binary keeps the flag surface from the stub and adds what a tool caller needs:

```text
-uri     URI of the mount to expose (required). Nested mounts and decorators work as usual,
         e.g. memory:?data=file:///srv/src or memory://?options=[{readOnly: true}]
-cmd     the command line to run (required in one-shot mode)
-cwd     working directory inside the mount, default "."
-json    machine-readable Result on stdout instead of raw stdout
-max-bytes, -timeout, -max-results    overrides for posixcli.Limits
```

One-shot mode pays a process start and a fresh mount per call, which is why the state model
is deliberately boring: no session, so no session to leak or race. Callers that need `cd` to
persist use either repeated `-cwd` or the in-process adapter. Exit codes are the table above;
`-json` puts the whole `Result` on stdout and leaves logs on stderr, so a caller never has to
separate them.

### ADK adapter

`agenttools/adk` is a separate Go module so `google.golang.org/adk` never enters the main
module's graph. `drivers/all` is already documented in this repo as pulling in GCS, go-git and
bbolt; adding the ADK and genai dependency tree to every `ufs` consumer is a worse version of
the same problem, and the adapter is a thin file that does not justify it.

Module separation says nothing about drivers, and that cost is already paid:
`cmd/ufsforagents/ufsforagents.go:27` blank-imports `drivers/all`, and `Makefile:19` lists
`ufsforagents` in `ALL_APPS`, so the merged binary links go-git, bbolt and the GCS client
today. The intended set for the phase-2 and phase-6 binary stays `drivers/all` — a tool caller
chooses the mount with `-uri`, so any URI has to work. The adapter takes the opposite side of
that line: `agenttools/adk` itself accepts an `fs.FS` handed to it by its host process and
imports no driver, so only its example program blank-imports `drivers/all`. A caller that
mounts GCS pays for the GCS client and nothing else. Narrowing the binary's driver set with
build tags is out of scope for every phase.

Verified against the current module docs (`google.golang.org/adk/v2/tool/functiontool`):

```go
func New[TArgs, TResults any](cfg Config, handler Func[TArgs, TResults]) (tool.Tool, error)
type Func[TArgs, TResults any] func(agent.Context, TArgs) (TResults, error)
type Config struct { Name, Description string; InputSchema, OutputSchema *jsonschema.Schema;
                     IsLongRunning, RequireConfirmation bool; ... }
```

Schemas are inferred from the argument and result structs, so the adapter is struct tags plus
a call:

```go
type posixArgs struct {
        Command string `json:"command" jsonschema:"POSIX command to run against the mounted file system, for example \"ls -l /src\" or \"grep -rn TODO /src\""`
}

type posixResults struct {
        Stdout    string `json:"stdout,omitempty"`
        Stderr    string `json:"stderr,omitempty"`
        ExitCode  int    `json:"exitCode"`
        Truncated bool   `json:"truncated,omitempty"`
        Notice    string `json:"notice,omitempty"`
}

func (a *adapter) run(ctx agent.Context, in posixArgs) (posixResults, error) {
        result, err := a.session.Run(ctx, in.Command)
        if err != nil {
                return posixResults{}, err
        }
        return posixResults{Stdout: result.Stdout, Stderr: result.Stderr,
                ExitCode: result.ExitCode, Truncated: result.Truncated, Notice: result.Notice}, nil
}
```

Design points carried across the boundary:

* One tool, `ufs_posix`, taking a whole command line — the tool list stays small and the model
  uses its existing CLI prior. A second, opt-in `Toolset` generates one typed tool per
  registry entry (`ufs_ls`, `ufs_grep`, …) for callers that want per-argument schemas and
  confirmation gating. `Config.RequireConfirmation` is the hook for that: the registry already
  knows which commands mutate the mount, so the adapter registers mutating commands with
  confirmation on and read-only commands with it off.
* A command failure is returned as a populated `Result` with `ExitCode != 0`, not as a Go
  error, so the model reads the failure and retries instead of the turn aborting.
* One `Session` per ADK session, held for the agent's lifetime, so `cd` and the mount survive
  across turns. ADK may run tools in parallel, so the adapter serializes `Run` — cheaper than
  making `Session` concurrency-safe and matching its documented single-caller contract.
* The instruction block the adapter returns for `Instruction` states the boundary: this is an
  emulated terminal, one command per call, no shell operators, output is truncated.

MCP is the same registry over a different transport. `google.golang.org/adk/v2/tool/mcptoolset`
consumes a server, so `agenttools/mcp` would make the binary useful to Claude, Gemini CLI and
other clients without an ADK dependency. It needs a new third-party dependency, so it is a
separate decision (see [Open decisions](#open-decisions)).

### Example session

```text
$ ufsforagents -uri 'archive://build.tar.gz' -cmd 'ls -l'
total 3
-rw-r--r-- 1970-01-01T00:00:00Z 1.2k  README.md
drwxr-xr-x                      -     src/
-rw-r--r-- 1970-01-01T00:00:00Z  18k  build.log

$ ufsforagents -uri 'archive://build.tar.gz' -cmd 'grep -rn TODO src'
src/main.go:42: TODO: rotate the signing key
src/main.go:88: TODO: drop the legacy header

$ ufsforagents -uri 'archive://build.tar.gz' -cmd 'touch notes.txt'
exit 1: cannot write to a read-only mount; this mount came from an archive, so writes need a
writable layer, for example memory:?data=archive://build.tar.gz

$ ufsforagents -uri 'memory:?data=archive://build.tar.gz' -cmd 'cp src/main.go src/main.go.bak && ls'
exit 2: unsupported: "&&" — this is an emulated terminal, not a shell. Run one command per
call. The result already carries stdout and an exit code.
```

## Testing

* `parse.go`: table-driven tests for quoting, globs, pipelines, redirects, and one case per
  rejected construct asserting the message names the token. A fuzz target on the tokenizer
  (`FuzzParse`) must not panic and must never return a command that uses a rejected construct.
* Per command: golden `Result` tables over a small fixed tree, run against `memory:`, a local
  directory, an `archive://` mount, and a `readOnly`-decorated mount. The read-only column is
  the one that earns its keep: every mutating command must report a permission failure rather
  than a panic or a silent success.
* Limits: truncation notices, `MaxResults`, `MaxFileBytes` skip, sorted output verified by
  running the same `ls`/`find` many times over `memFS` and requiring identical output.
* Path discipline: `..`, absolute paths, glob-to-nothing, redirect onto a directory,
  symlink read, and cross-mount `mv`.
* `cmd/ufsforagents`: extend the existing cases with exit-code and `-json` assertions.
* `agenttools/adk`: an example agent whose tool calls run against a prepared mount. The model
  call itself is skipped without an API key; the tool handler is tested directly with a fake
  `agent.Context` so the suite stays hermetic.
* `make presubmit` is the gate. Markdown lint (`rumdl check`) covers this file.

## Changes per file

| Path | Change |
|:--|:--|
| `agenttools/posixcli/posixcli.go` | `Session`, `Limits`, `Result`, `Run`, registry; keeps the stub's `Run` as a wrapper |
| `agenttools/posixcli/{parse,flags,exec,errors}.go` | new: tokenizer, parser, pipeline execution, error text |
| `agenttools/posixcli/<cmd>.go` | new: one file per command |
| `cmd/ufsforagents/ufsforagents.go` | `-cwd`, `-json`, limit flags, exit codes |
| `agenttools/adk/{go.mod,adk.go,toolset.go}` | new module: `functiontool` wrapper, opt-in toolset |
| `agenttools/mcp/` | later phase, pending the dependency decision |
| `nestfs.go` | new exported mount-table enumeration (`MountInfo`, `Mount`) for the `mounts` command; see the command table |
| `go.mod` | adds `mvdan.cc/sh/v3` for its `syntax` package only, pinned to `v3.14.1` by the phase-1 PR |
| `Makefile` | `ALL_APPS` already lists `ufsforagents`; the nested module needs a build/test target of its own |

## Implementation references

Every link below returned HTTP 200 when this spec was written.

Shell parsing:

* [`mvdan.cc/sh/v3/syntax`](https://pkg.go.dev/mvdan.cc/sh/v3/syntax) — the parser we adopt;
  see `Parser`, the `ParserOption` functions (`Variant`, `KeepComments`) and `LangPOSIX`.
* [`mvdan.cc/sh/v3/interp`](https://pkg.go.dev/mvdan.cc/sh/v3/interp) — the executor we do not
  adopt; `OpenHandler`, `ExecHandler` and friends are the seam inventory, and the reasons are in
  the parser section.
* [mvdan/sh on GitHub](https://github.com/mvdan/sh) — examples and the issue tracker.

POSIX text to follow rather than guess:

* [Utility Syntax Guidelines](https://pubs.opengroup.org/onlinepubs/9699919799/basedefs/V1_chap02.html)
  — the option syntax `ls`, `grep`, `find` and friends are expected to obey.
* [`ls`](https://pubs.opengroup.org/onlinepubs/9699919799/utilities/ls.html),
  [`grep`](https://pubs.opengroup.org/onlinepubs/9699919799/utilities/grep.html),
  [`find`](https://pubs.opengroup.org/onlinepubs/9699919799/utilities/find.html) — the flag
  tables copied in phases 2 and 3.

The io/fs surface the commands are built on:

* [`io/fs`](https://pkg.go.dev/io/fs) — `FS`, `ReadDirFS`, `ReadFileFS`, `StatFS`, `Glob`, and
  the `ValidPath` rule this package inherits.
* [`testing/fstest`](https://pkg.go.dev/testing/fstest) — `TestFS` and `MapFS` for the
  conformance tests.
* [`os.Root`](https://pkg.go.dev/os#Root) — the containment pattern in the standard library.

Agent integration:

* [ADK Go function tools](https://adk.dev/tools-custom/function-tools/) — `functiontool.New`,
  schema inference from struct tags, `RequireConfirmation`.
* [`google.golang.org/adk/v2/tool/functiontool`](https://pkg.go.dev/google.golang.org/adk/v2/tool/functiontool)
  — the exact signature the adapter compiles against.
* [`google.golang.org/adk/v2/tool/mcptoolset`](https://pkg.go.dev/google.golang.org/adk/v2/tool/mcptoolset)
  — how an MCP server here would be consumed with no ADK code of ours.
* [ADK tools overview](https://adk.dev/tools/) and [ADK MCP integration](https://adk.dev/mcp/).
* [MCP transports](https://modelcontextprotocol.io/docs/concepts/transports) and
  [the tools primitive in the specification](https://modelcontextprotocol.io/specification/2026-07-28/server/tools).
* [`modelcontextprotocol/go-sdk/mcp`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp)
  — `NewServer`, `AddTool`, `StdioTransport` for phase 8.

Testing style:

* [`go-internal/testscript`](https://github.com/rogpeppe/go-internal/testscript) — script-style
  command tests, the model for the command-table tests.

## Implementation plan

Each phase is a PR on top of current `main`, in this order. Nothing in a later phase blocks an
earlier one. Each phase has an exit criterion a reviewer can run against `cmd/ufsforagents`,
because "leaves the binary working" is not checkable: `posixcli.Run` returns an empty string
and a nil error today (`agenttools/posixcli/posixcli.go:29-33`), so the merged binary prints
nothing and exits 0 for every command line.

* Phase 1: `ufsforagents -uri memory: -cmd help` prints the registry on stdout and exits 0.
  `ufsforagents -uri memory: -cmd ls` prints `ls: not implemented in this build; supported:
  help` on stderr and exits 2 — `ls` is in the registry but `Session.Run` gains executors only
  in phase 2, so the honest answer is a push-back naming what works, not the `help` text and
  not an empty success. `-cmd nosuchcommand` exits 2 with `nosuchcommand: unknown command`, and
  `-cmd 'echo hi && ls'` exits 2 with the `ErrUnsupportedShell` text. The binary's flags and its
  single `fmt.Print` stay as they are; only the wrapper's body changes.
* Phase 2: `ufsforagents -uri memory: -cmd mounts` prints the mount's URI, and `ls -l` prints
  mode, size, RFC 3339 mtime and name for a file created through `-uri memory:` with `?data=`.

Phases 3 to 7 add commands, flags and modes, so their criteria are the command tests plus one
equivalent run through the binary for each new surface.

| Phase | PR | Contents | Cuts |
|:--|:--|:--|:--|
| 0 | this spec | design only | — |
| 1 | `posixcli: parse a command line` | `Session`, `Limits`, `Result`, `parse.go` over `syntax.Parser`, `flags.go`, `errors.go`, the registry, `help`, and `ErrUnsupportedShell` for every rejected construct. `Session.Run` executes only the built-in `help` here. Adds `mvdan.cc/sh/v3` to `go.mod`. | No executors, so every registered command gets the phase-1 push-back, and no file-system access at all, so the parser can be reviewed and fuzzed on its own. |
| 2 | `posixcli: read-only commands` | `pwd`, `ls`, `cat`, `head`, `tail`, `wc`, `stat`, `du`, `tree`, `mounts`; the output budget and sorted output. Wire `cmd/ufsforagents` to `Session` and keep the `Run` wrapper. `mounts` needs the new root-package `Mount`/`MountInfo` enumeration in `nestfs.go`. | No writes, no pipes. |
| 3 | `posixcli: search commands` | `grep` (streaming, binary detection, `--include`) and `find` on `ops.Walk`, including the archive-directory rule from open decision 5. | Pipeline stages still unsupported. |
| 4 | `posixcli: write commands` | `touch`, `mkdir`, `truncate`, `rm`, `rmdir`, `mv`, `cp`, `cd`; `ops`/`polyfills` dispatch; the read-only-mount messages. | Redirection still unsupported. |
| 5 | `posixcli: pipelines and redirection` | `>`/`>>`, `/dev/null`, bounded stage buffers, and `sort`/`uniq` as filters. | Last, because it is the part with the most ways to be wrong about buffering. |
| 6 | `cmd/ufsforagents: machine mode` | `-cwd`, `-json`, `-max-bytes`, `-timeout`, `-max-results`, and the exit-code contract end to end. | |
| 7 | `agenttools/adk: functiontool adapter` | nested module, `ufs_posix` tool, the opt-in per-command `Toolset` with `RequireConfirmation` for mutating commands, and the example agent. Needs the Makefile/CI target for a nested module. | MCP is not in this phase. |
| 8 | `agenttools/mcp: stdio server` | only if open decision 2 lands; needs the dependency decision first. | |

Ordering notes. The parser comes first on purpose: it is where untrusted model text becomes a
command, and phases 2 to 5 all inherit its guarantees. Search (phase 3) precedes writes
because it is read-only value an agent can use immediately, and because `grep` settles the
streaming and binary-file rules that redirection later reuses. The ADK adapter waits until the
registry is complete, since its per-command toolset is generated from it.

## Known limitations

* One-shot mode has no session, so `cd` is inert there and each call remounts. Remounting a
  `gs://` or git-backed mount per call is slow; those callers should use the in-process
  adapter.
* Pipelines buffer a stage's output in memory up to the output budget. There is no streaming
  between stages, so `find . | grep x` over a huge tree stops at the budget rather than
  streaming.
* `mtime`, mode bits and ownership are whatever the backing file system reports. On `memory:`
  and archives they are synthetic, so an agent reasoning about "changed since" gets what the
  driver gives it.
* No `Watch` wiring. An agent cannot wait for changes; it re-reads.
* Quoted-text extraction, code execution and diffing stay outside this package. The emulated
  terminal is for files, not for computing.

## Out of scope

* Shell features: variables, control flow, subshells, functions, job control, signals,
  terminal handling.
* Host access: any command reaching outside the mount, or spawning a host binary.
* Mount management from inside a command (`mount`, `umount`, `mkfs`) — the mount is chosen by
  the caller and is the trust boundary.
* `chmod`/`chown`/`ln`, and any permission model beyond the `readOnly` decorator.
* A TTY, readline, or interactive mode. The interface is one command, one result.

## Open decisions

1. ADK placement: separate module `agenttools/adk` (recommended) versus a documented snippet
   in the package doc with no code of our own. The separate module needs CI wiring; the
   snippet keeps the tree smaller but leaves every caller rewriting the adapter.
2. MCP: adopt a Go MCP server dependency in a nested module, or leave the interface at the CLI
   and let external wrappers bridge it. This is the one place a new third-party dependency
   enters the repo.
3. What the one-shot `Run(_ context.Context, _ fs.FS, _ string) (string, error)` reports for a
   failing command: an error built from `Result.Stderr`, or `Stderr` as the returned string with
   a nil error so that `ExitCode`, `Truncated` and `Notice` are visible only through `-json`. A
   two-value return cannot carry all four fields, and the stub is unreleased so nothing here is
   load-bearing yet. Whether the function exists at all is settled — phase 2 keeps it so the
   binary's tests read as they do now — so only its failure shape is open.
4. `grep` default: fixed-string (safer, matches what most agents mean) versus extended regex.
   The table above says fixed-string with `-E` to opt in; a reviewer may prefer the opposite.
5. Should `find`/`ls -R` descend into virtual archive directories by default? The table says
   no, following `ops.Walk`, with `-archives` to opt in. An agent looking for a file inside a
   `.zip` may not know to ask. `cp -r` faces the same question and starts from the opposite
   default, because `ops.Rsync` traverses archives and takes no option to stop it; the command
   table explains how `cp -r` avoids that without waiting on
   [issue #46](https://github.com/cloudfra/ufs/issues/46). A reviewer who settles this for `find`
   should settle it for `cp -r` in the same pass.
6. `mvdan.cc/sh/v3/syntax` versus a hand-written tokenizer. The parser section argues for
   borrowing the tokenizer, which adds one BSD-3 module with no third-party dependencies of its
   own. A reviewer who wants zero new dependencies in `go.mod` should say so: the fallback is
   about 150 lines of our own quoting rules, and every phase after 1 is unchanged either way.
