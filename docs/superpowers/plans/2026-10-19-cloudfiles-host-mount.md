# Cloud Files Host Mount Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split `host` into isolated, separately importable mount drivers
(FUSE, ProjFS, Cloud Files) chosen by a registry, and add a read-write Windows
Cloud Files (`cldapi.dll`) driver that streams reads through placeholders and
uploads writes on close.

**Architecture:** `host` keeps only `MountServer`, `Mount`, options and a
backend registry with auto-selection. Each driver is its own package that
registers in `init()`; each Win32 API wrapper is an `internal` package of its
driver that knows nothing of ufs. A shared, exported conformance suite in
`host/hosttest` runs against every driver.

**Tech Stack:** Go 1.27, `golang.org/x/sys/windows` (pure syscall bindings, no
cgo), `github.com/hanwen/go-fuse/v2` (Linux only), GitHub Actions on
self-hosted runners.

**Spec:** `docs/superpowers/specs/2026-10-19-cloudfiles-host-mount-design.md`
(read it first; this plan argues from it).

**Tracking:** GitHub issue and pull request linked in the "Handoff" section.

## Global Constraints

- Module path: `github.com/cloudfra/ufs`.
- Always build, test and lint with `make` (`make test`, `make lint`,
  `make presubmit` before pushing). Do not run bare `go test` as the final
  check. `GOOS=windows go test -c -o /dev/null ./host/...` and
  `GOOS=windows build/toolchain/bin/golangci-lint run ./host/...` are fine for
  fast local checks of Windows code from Linux.
- No cgo, no new module dependencies.
- New Go files start with the Apache 2.0 header used across the repo
  (`// Copyright 2026 Jeremy Edwards` + license block). New shell scripts use
  `#!/usr/bin/env bash`, the `# Copyright 2026 Cloudfra` header, a
  description comment and `set -euo pipefail` (see `scripts/prepare-ci.sh`).
- No inline multi-line scripts in workflow YAML: put them in `scripts/` and
  call them.
- `errcheck` runs with `check-blank: true`: `_ = f()` and `x, _ := f()` are
  reported. Route `LazyProc.Call` through one helper with one `//nolint`
  (pattern: `projfsCall` in `host/projfs_windows.go`).
- Never pass a Go pointer to Windows as a callback context; pass an integer ID
  looked up in a `sync.Map` (checkptr aborts under `-race` otherwise).
- `LazyProc.Call`'s third result is `GetLastError` and is meaningless for
  HRESULT-returning APIs: check only the returned HRESULT.
- Any helper that forwards `uintptr(unsafe.Pointer(...))` arguments to
  `LazyProc.Call` must be marked `//go:uintptrescapes`.
- The Cloud Files wrapper and driver build only on
  `windows && (amd64 || arm64)`. On `windows/386` (a release target) the
  driver's `Available()` returns an error saying Cloud Files needs 64-bit
  Windows.
- Cloud Files tests always run on Windows (no opt-in variable). ProjFS tests
  keep `UFS_ENABLE_PROJFS_TEST`.
- Pull requests are squashed to one commit before review is requested, are
  not drafts, and end with the Claude Code attribution lines.

---

## Handoff

Start here in a new session.

- Branch: `cloudfiles-mount` (from `main` at `380b71c`).
- Issue: see the PR description ("Closes #...").
- PR: `gh pr list --head cloudfiles-mount`.
- Status: spec and this plan are written. **Next task: Task 1.** Tick the
  checkboxes in this file as tasks land and commit the file with the work, so
  the file always shows where to resume.
- Before each task: `git pull`, read the task, read the files it lists.
- After each task: `make test` and `make lint` (lint-vuln may fail locally on
  an out-of-date Go toolchain; CI uses the latest), commit, push, and check CI
  with `gh pr checks`.
- Self-hosted CI runners:
  - `Build Linux` (`linux`, `large`): FUSE tests run here. A FUSE test that
    fails to unmount leaves a dead mount; `scripts/prepare-ci.sh` cleans it.
  - `Build Windows` (`windows`, `large`, `desktop-experience`): ProjFS and
    Cloud Files tests, including a `-race` deflake run.
  - `build-windows-root` (`windows`, `root`, Server Core): no ProjFS; Cloud
    Files availability unknown (Task 9 finds out).
- Lessons from the ProjFS work (PR #467) that apply here:
  - ProjFS sends no notifications for the provider process's own I/O, so its
    tests do deletes/renames in a child process (`host/hostop_windows_test.go`).
    On Linux, forking during parallel FUSE tests makes unmounts fail with
    EBUSY, so the child-process helper must stay Windows-only.
  - Read CI failure logs with
    `gh api --allow-escape-sequences repos/cloudfra/ufs/actions/jobs/<id>/logs`
    (`gh run view --log` returns nothing while the run is in progress).
  - Passing tests print nothing in `go test` package mode, so verify a test
    ran from coverage or by failing it deliberately once.

## File Structure

```text
drivers/types/deviceinfo.go             + Remote()
drivers/core/localfs_deviceinfo_linux.go   network fs types remote
drivers/core/localfs_deviceinfo_windows.go DRIVE_REMOTE remote
host/host.go                            MountServer, Mount, Option, WithBackend
host/registry.go                        Backend, RegisterBackend, selection
host/host_test.go                       selection tests with fake backends
host/hosttest/hosttest.go               Backend, Caps, Run (conformance suite)
host/hosttest/read.go                   read conformance tests (moved)
host/hosttest/write.go                  write conformance tests (new)
host/hosttest/childop.go                Main, Remove, Rename, WriteFile (child process)
internal/hostutil/clamp.go              clamp helpers (moved from host/math.go)
host/fuse/fuse_linux.go                 FUSE driver (moved)
host/fuse/backend.go                    Name, registration, unavailable off Linux
host/fuse/fuse_linux_test.go            FUSE tests (moved)
host/projfs/backend.go                  Name, registration
host/projfs/mount_windows.go            ProjFS driver (moved)
host/projfs/mount_windows_test.go       ProjFS tests (moved)
host/projfs/internal/projfslib/*.go     projectedfslib.dll wrapper + errors
host/cloudfiles/backend.go              Name, registration, Available
host/cloudfiles/server_windows.go       mount/unmount lifecycle
host/cloudfiles/callbacks_windows.go    callbacks
host/cloudfiles/ids_windows.go          placeholder ID ↔ path map
host/cloudfiles/sync_windows.go         watcher + upload queue
host/cloudfiles/*_test.go               driver tests
host/cloudfiles/internal/cfapi/types_windows.go     struct layouts, constants
host/cloudfiles/internal/cfapi/syscalls_windows.go  bindings
host/cloudfiles/internal/cfapi/errors_windows.go    error table
host/cloudfiles/internal/cfapi/*_test.go            layout and error tests
host/all/all.go                         blank-imports the three drivers
cmd/ufsmount/ufsmount.go                -backend flag, imports host/all
.golangci.yml                           depguard isolation rules
.github/workflows/deploy.yaml           unchanged env for cloudfiles (none)
CLAUDE.md, docs/ufsmount.md             documentation
```

---

### Task 1: DeviceInfo.Remote and network devices

**Files:**

- Modify: `drivers/types/deviceinfo.go`
- Modify: `drivers/core/localfs_deviceinfo_linux.go` (function
  `linuxMakeDeviceInfo`, `linuxDeviceTypeAndThreads`)
- Modify: `drivers/core/localfs_deviceinfo_windows.go` (`windowsDriveInfo`)
- Test: `drivers/types/deviceinfo_test.go`,
  `drivers/core/localfs_deviceinfo_linux_test.go`

**Interfaces:**

- Produces: `func (info DeviceInfo) Remote() bool`.

- [ ] **Step 1: Write the failing tests**

In `drivers/types/deviceinfo_test.go` add:

```go
func TestDeviceInfoRemote(t *testing.T) {
	t.Parallel()
	if NewDeviceInfo("a", "fixed", 1, false).Remote() {
		t.Error("local device reports Remote() = true")
	}
	if !NewDeviceInfo("gs://b", "network", 1, true).Remote() {
		t.Error("remote device reports Remote() = false")
	}
	if DefaultDeviceInfo.Remote() {
		t.Error("DefaultDeviceInfo.Remote() = true, want false")
	}
}
```

In `drivers/core/localfs_deviceinfo_linux_test.go` add a table test feeding
`linuxDeviceMapFromReader` one `/proc/mounts` line per network type and
checking `Remote()` on `["."]`:

```go
func TestLinuxDeviceMapRemote(t *testing.T) {
	t.Parallel()
	for fsType, want := range map[string]bool{
		"ext4": false, "tmpfs": false, "nfs": true, "nfs4": true,
		"cifs": true, "smb3": true, "fuse.sshfs": true, "9p": true,
		"afs": true, "ceph": true, "glusterfs": true,
	} {
		line := "server:/export /mnt " + fsType + " rw 0 0\n"
		dm := linuxDeviceMapFromReader("/mnt/data", strings.NewReader(line))
		if got := dm["."].Remote(); got != want {
			t.Errorf("%s: Remote() = %t, want %t", fsType, got, want)
		}
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `go test ./drivers/types/ ./drivers/core/ -run 'Remote'`
Expected: compile error `info.Remote undefined`.

- [ ] **Step 3: Implement**

`drivers/types/deviceinfo.go`, after `DeviceType`:

```go
// Remote reports whether the device is on another machine, so that I/O may
// be slow and host mounts should not assume local access.
func (info DeviceInfo) Remote() bool {
	return info.remote
}
```

`drivers/core/localfs_deviceinfo_linux.go`:

```go
// linuxNetworkFSTypes are /proc/mounts file system types whose data lives on
// another machine.
var linuxNetworkFSTypes = map[string]bool{
	"nfs": true, "nfs4": true, "cifs": true, "smb3": true, "smbfs": true,
	"fuse.sshfs": true, "9p": true, "afs": true, "ceph": true,
	"glusterfs": true, "fuse.glusterfs": true,
}
```

and in `linuxMakeDeviceInfo` replace the final `false` with
`linuxNetworkFSTypes[m.fsType]`.

`drivers/core/localfs_deviceinfo_windows.go`: in the `DRIVE_REMOTE` case
change the last argument to `true`.

- [ ] **Step 4: Run tests**

Run: `make test` → PASS. Run `GOOS=windows go vet ./drivers/core/`.

- [ ] **Step 5: Commit**

```bash
git add drivers/types drivers/core
git commit -m "Report remote devices in DeviceInfo"
```

---

### Task 2: Backend registry and selection in host

**Files:**

- Create: `host/registry.go`, `host/host_test.go`
- Modify: `host/host.go`
- Modify: `host/fuse_linux.go` (temporary registration, removed in Task 4)
- Modify: `host/mount_windows.go` (temporary registration, removed in Task 5)
- Delete: `host/host_other.go`

**Interfaces:**

- Produces (package `host`):

```go
type Backend interface {
	Name() string
	Available() error
	Suits(fsys types.ReadFS) bool
	Priority() int
	Mount(ctx context.Context, fsys types.ReadFS, mountPath string) (MountServer, error)
}
func RegisterBackend(b Backend)
type Option func(*options)
func WithBackend(name string) Option
func Mount(ctx context.Context, fsys types.ReadFS, mountPath string, opts ...Option) (MountServer, error)
func AllLocal(fsys types.ReadFS) bool // no device in GetDeviceInfo() is remote
var ErrNoBackend = errors.New("no host mount backend")
```

- [ ] **Step 1: Write the failing tests** (`host/host_test.go`, no build tag)

```go
package host

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
)

type fakeBackend struct {
	name     string
	priority int
	avail    error
	suits    bool
	mounted  *string
}

func (b fakeBackend) Name() string                 { return b.name }
func (b fakeBackend) Available() error             { return b.avail }
func (b fakeBackend) Suits(types.ReadFS) bool      { return b.suits }
func (b fakeBackend) Priority() int                { return b.priority }
func (b fakeBackend) Mount(context.Context, types.ReadFS, string) (MountServer, error) {
	*b.mounted = b.name
	return nil, nil
}

func withBackends(t *testing.T, bs ...Backend) {
	t.Helper()
	saved := snapshotBackends()
	t.Cleanup(func() { restoreBackends(saved) })
	restoreBackends(nil)
	for _, b := range bs {
		RegisterBackend(b)
	}
}

func TestMountSelection(t *testing.T) {
	var got string
	cfiles := fakeBackend{name: "cloudfiles", priority: 10, suits: true, mounted: &got}
	projfs := fakeBackend{name: "projfs", priority: 20, suits: true, mounted: &got}
	tests := []struct {
		name     string
		backends []Backend
		opts     []Option
		want     string
		wantErr  string
	}{
		{"highest priority", []Backend{cfiles, projfs}, nil, "projfs", ""},
		{"skips unsuited", []Backend{cfiles, fakeBackend{name: "projfs", priority: 20, mounted: &got}}, nil, "cloudfiles", ""},
		{"skips unavailable", []Backend{cfiles, fakeBackend{name: "projfs", priority: 20, suits: true, avail: errors.New("no dll"), mounted: &got}}, nil, "cloudfiles", ""},
		{"forced ignores Suits", []Backend{cfiles, fakeBackend{name: "projfs", priority: 20, mounted: &got}}, []Option{WithBackend("projfs")}, "projfs", ""},
		{"forced unavailable", []Backend{fakeBackend{name: "projfs", avail: errors.New("no dll"), mounted: &got}}, []Option{WithBackend("projfs")}, "", "no dll"},
		{"forced unregistered", []Backend{cfiles}, []Option{WithBackend("projfs")}, "", "github.com/cloudfra/ufs/host/projfs"},
		{"none registered", nil, nil, "", "github.com/cloudfra/ufs/host/all"},
		{"none suitable", []Backend{fakeBackend{name: "x", avail: errors.New("broken"), mounted: &got}}, nil, "", "x: broken"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got = ""
			withBackends(t, tc.backends...)
			_, err := Mount(t.Context(), nil, "dir", tc.opts...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Mount() error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Mount() mounted %q, err %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestRegisterBackendDuplicatePanics(t *testing.T) {
	var got string
	withBackends(t, fakeBackend{name: "a", mounted: &got})
	defer func() {
		if recover() == nil {
			t.Error("RegisterBackend of a duplicate name did not panic")
		}
	}()
	RegisterBackend(fakeBackend{name: "a", mounted: &got})
}
```

These tests mutate the global registry, so they do not call `t.Parallel()`.

- [ ] **Step 2: Run to see them fail**

Run: `go test ./host/ -run 'MountSelection|RegisterBackend'` → compile errors.

- [ ] **Step 3: Implement `host/registry.go`**

```go
package host

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/cloudfra/ufs/drivers/types"
)

// Backend is a host mount driver. Drivers live in their own packages under
// host/ and register themselves in init() with RegisterBackend, so a program
// only compiles the drivers it imports.
type Backend interface {
	// Name is the driver name used with WithBackend, e.g. "cloudfiles".
	Name() string
	// Available returns nil when the driver can mount on this host, else an
	// error that says why and how to fix it.
	Available() error
	// Suits reports whether automatic selection may pick this driver for
	// fsys. A forced driver (WithBackend) is used even if it does not suit.
	Suits(fsys types.ReadFS) bool
	// Priority orders drivers for automatic selection; the highest wins.
	Priority() int
	// Mount mounts fsys at mountPath.
	Mount(ctx context.Context, fsys types.ReadFS, mountPath string) (MountServer, error)
}

// ErrNoBackend is returned (wrapped) when no registered driver can mount.
var ErrNoBackend = errors.New("no host mount backend")

var (
	backendsMu sync.RWMutex
	backends   = map[string]Backend{}
)

// RegisterBackend makes a driver available to Mount. It panics if a driver
// with the same name is already registered.
func RegisterBackend(b Backend) {
	backendsMu.Lock()
	defer backendsMu.Unlock()
	if _, ok := backends[b.Name()]; ok {
		panic(fmt.Sprintf("host: backend %q registered twice", b.Name()))
	}
	backends[b.Name()] = b
}

func snapshotBackends() map[string]Backend {
	backendsMu.RLock()
	defer backendsMu.RUnlock()
	m := make(map[string]Backend, len(backends))
	for k, v := range backends {
		m[k] = v
	}
	return m
}

func restoreBackends(m map[string]Backend) {
	backendsMu.Lock()
	defer backendsMu.Unlock()
	backends = map[string]Backend{}
	for k, v := range m {
		backends[k] = v
	}
}

// AllLocal reports whether no device backing fsys is remote.
func AllLocal(fsys types.ReadFS) bool {
	for _, info := range fsys.GetDeviceInfo() {
		if info.Remote() {
			return false
		}
	}
	return true
}

func selectBackend(fsys types.ReadFS, name string) (Backend, error) {
	all := snapshotBackends()
	if name != "" {
		b, ok := all[name]
		if !ok {
			return nil, fmt.Errorf("%w %q: import github.com/cloudfra/ufs/host/%s", ErrNoBackend, name, name)
		}
		if err := b.Available(); err != nil {
			return nil, fmt.Errorf("host mount backend %q is not available: %w", name, err)
		}
		return b, nil
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("%w registered: import github.com/cloudfra/ufs/host/all or a driver package", ErrNoBackend)
	}
	sorted := slices.SortedFunc(func(yield func(Backend) bool) {
		for _, b := range all {
			if !yield(b) {
				return
			}
		}
	}, func(a, b Backend) int {
		return cmp.Or(cmp.Compare(b.Priority(), a.Priority()), cmp.Compare(a.Name(), b.Name()))
	})
	var reasons []string
	for _, b := range sorted {
		if err := b.Available(); err != nil {
			reasons = append(reasons, fmt.Sprintf("%s: %v", b.Name(), err))
			continue
		}
		if !b.Suits(fsys) {
			reasons = append(reasons, fmt.Sprintf("%s: does not suit %v", b.Name(), fsys))
			continue
		}
		slog.Info("host: selected mount backend", "backend", b.Name(), "skipped", reasons)
		return b, nil
	}
	return nil, fmt.Errorf("%w can mount %v: %s", ErrNoBackend, fsys, strings.Join(reasons, "; "))
}
```

`host/host.go` (replace `Mount`; keep `MountServer` and the package doc,
updating the doc to name the driver packages):

```go
type options struct {
	backend string
}

// Option configures Mount.
type Option func(*options)

// WithBackend forces Mount to use the named driver (for example
// cloudfiles.Name) instead of choosing one automatically.
func WithBackend(name string) Option {
	return func(o *options) { o.backend = name }
}

// Mount mounts fsys at mountPath with a registered driver. Without
// WithBackend it picks the highest-priority available driver that suits fsys.
func Mount(ctx context.Context, fsys types.ReadFS, mountPath string, opts ...Option) (MountServer, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	b, err := selectBackend(fsys, o.backend)
	if err != nil {
		return nil, err
	}
	return b.Mount(ctx, fsys, mountPath)
}
```

Temporary adapters so the package keeps working until Tasks 4 and 5 move the
drivers out: in `host/fuse_linux.go` rename `func mount(` to
`func fuseMount(` and add

```go
type fuseBackend struct{}

func (fuseBackend) Name() string                   { return "fuse" }
func (fuseBackend) Available() error               { return nil }
func (fuseBackend) Suits(types.ReadFS) bool        { return true }
func (fuseBackend) Priority() int                  { return 10 }
func (fuseBackend) Mount(ctx context.Context, fsys types.ReadFS, p string) (MountServer, error) {
	return fuseMount(ctx, fsys, p)
}

func init() { RegisterBackend(fuseBackend{}) }
```

and the same in `host/mount_windows.go` for `projfsBackend` (name `projfs`,
priority 20, `Available` = `projfsAvailable()`, `Suits` = `AllLocal(fsys)`).
Delete `host/host_other.go` (selection now returns `ErrNoBackend`).

- [ ] **Step 4: Run tests**

Run: `make test`, `GOOS=windows go test -c -o /dev/null ./host/`,
`GOOS=darwin go vet ./host/`. Expected: PASS / builds.

- [ ] **Step 5: Commit**

```bash
git add host
git commit -m "Choose host mount drivers from a registry"
```

---

### Task 3: hostutil and the shared conformance suite

**Files:**

- Create: `internal/hostutil/clamp.go`, `internal/hostutil/clamp_test.go`
  (move `host/math.go`, `host/math_test.go`; export `ClampToUint32`,
  `ClampToUint64`, `ClampToInt64`)
- Create: `host/hosttest/hosttest.go`, `host/hosttest/read.go`,
  `host/hosttest/childop.go`, `host/hosttest/childop_windows.go`,
  `host/hosttest/childop_other.go`
- Delete: `host/mount_conformance_test.go`, `host/hostop_*_test.go`
- Modify: `host/fuse_linux.go`, `host/mount_windows.go` (use hostutil)
- Modify: `host/mount_conformance_linux_test.go`,
  `host/mount_conformance_windows_test.go` (call `hosttest.Run`)

**Interfaces:**

- Produces (package `hosttest`):

```go
type Caps struct {
	Write     bool // creates, writes, mkdir and renames reach fsys
	Delete    bool // deletes through the mount reach fsys (ProjFS denies them)
	DirRename bool // directories can be renamed through the mount
	ChildIO   bool // mutating I/O must come from another process
}
type Backend struct {
	Name  string
	Mount func(t *testing.T, fsys types.ReadFS) string // returns the mount dir
	Caps  Caps
}
func Run(t *testing.T, b Backend)            // runs every conformance test as subtests
func Main(m *testing.M)                       // TestMain helper for child-process ops
func Rename(ctx context.Context, b Backend, oldPath, newPath string) error
func Remove(ctx context.Context, b Backend, name string) error
func WriteFile(ctx context.Context, b Backend, name string, data []byte) error
```

`Rename`/`Remove`/`WriteFile` run in a child process when `b.Caps.ChildIO`
is set (Windows only; see `childop_windows.go`) and in-process otherwise.

- [ ] **Step 1: Move the clamp helpers**

```bash
mkdir -p internal/hostutil
git mv host/math.go internal/hostutil/clamp.go
git mv host/math_test.go internal/hostutil/clamp_test.go
```

Change `package host` to `package hostutil`, export the three functions
(`clampToUint32` → `ClampToUint32`, etc.) in both files and their callers in
`host/fuse_linux.go` and `host/mount_windows.go`
(`hostutil.ClampToUint32(...)`).

- [ ] **Step 2: Create the suite**

Move every `TestMountConformance*` function body from
`host/mount_conformance_test.go` into `host/hosttest/read.go` as unexported
functions `readFile(t, b)`, `stat(t, b)`, `readDir(t, b)`,
`statNotExist(t, b)`, `nestedRead(t, b)`, `largeFile(t, b)`, `nullFS(t, b)`,
`nestedOverlay(t, b)`, `rsyncArchive(t, b)`, `rename(t, b)`, replacing
`backend.mount(t, fsys)` with `b.Mount(t, fsys)` (it now returns the mount
directory string), `m.mountDir` with that string, `hostRename(...)` with
`Rename(t.Context(), b, ...)`, and `backend.noDirRename` with
`!b.Caps.DirRename`. `hosttest.go`:

```go
// Package hosttest is the conformance suite every host mount driver runs.
package hosttest

// Caps lists what a driver supports, so Run only runs the tests that apply.
type Caps struct {
	Write     bool // creates, writes, mkdir and renames reach fsys
	Delete    bool // deletes through the mount reach fsys (ProjFS denies them)
	DirRename bool // directories can be renamed through the mount
	ChildIO   bool // mutating I/O must come from another process
}

// Backend describes the driver under test.
type Backend struct {
	Name  string
	Mount func(t *testing.T, fsys types.ReadFS) string // mounts fsys, returns the mount directory
	Caps  Caps
}

// Run runs every conformance test against b as parallel subtests.
func Run(t *testing.T, b Backend) {
	t.Helper()
	tests := map[string]func(*testing.T, Backend){
		"ReadFile": readFile, "Stat": stat, "ReadDir": readDir,
		"StatNotExist": statNotExist, "NestedRead": nestedRead,
		"LargeFile": largeFile, "NullFS": nullFS,
		"NestedOverlay": nestedOverlay, "RsyncArchive": rsyncArchive,
		"Rename": rename,
	}
	for _, name := range slices.Sorted(maps.Keys(tests)) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tests[name](t, b)
		})
	}
}
```

`childop.go` holds `Rename`, `Remove`, `WriteFile` that call `runOp`
in-process unless `b.Caps.ChildIO`, then `childOp`. `childop_windows.go`
holds `Main` and `childOp` (the content of today's
`host/hostop_windows_test.go`, with `hostOpEnv` and the
`exec.CommandContext(ctx, os.Args[0], ...)` logic, plus a `"write"` op that
writes `os.Args[3]` bytes read from stdin). `childop_other.go`
(`//go:build !windows`) defines `Main(m)` as `os.Exit(m.Run())` and
`childOp` returning `errors.ErrUnsupported`. Keep the comment explaining
why Linux must not fork (EBUSY on parallel FUSE unmounts).

- [ ] **Step 3: Use the suite**

`host/mount_conformance_linux_test.go`:

```go
func TestMountConformance(t *testing.T) {
	t.Parallel()
	requireFUSE(t)
	hosttest.Run(t, hosttest.Backend{
		Name:  "fuse",
		Mount: testMount,
		Caps:  hosttest.Caps{Write: true, Delete: true, DirRename: true},
	})
}
```

`host/mount_conformance_windows_test.go`: the same with `Name: "projfs"`,
`Mount: testProjFSMount`, `Caps{Write: true, Delete: false, DirRename: false, ChildIO: true}`
and `requireProjFS(t)`. Add `func TestMain(m *testing.M) { hosttest.Main(m) }`
to a Windows test file, and replace `hostRemove`/`hostRename` calls in
`host/mount_windows_test.go` with `hosttest.Remove/Rename(t.Context(), projfsBackend, ...)`.

- [ ] **Step 4: Verify**

Run: `make test`, `GOOS=windows go test -c -o /dev/null ./host/...`,
`make lint`. Expected: PASS; the conformance tests appear as
`TestMountConformance/<Name>`.

- [ ] **Step 5: Commit**

```bash
git add -A host internal/hostutil
git commit -m "Move the host mount conformance suite into host/hosttest"
```

---

### Task 4: Move FUSE into host/fuse

**Files:**

- Move: `host/fuse_linux.go` → `host/fuse/fuse_linux.go`;
  `host/fuse_linux_test.go`, `host/fuse_*testinroot_linux_test.go`,
  `host/mount_conformance_linux_test.go` → `host/fuse/`
- Create: `host/fuse/backend.go`, `host/fuse/backend_other.go`

**Interfaces:**

- Consumes: `host.RegisterBackend`, `host.MountServer`, `hostutil.Clamp*`,
  `hosttest.Run`.
- Produces: package `fuse` with `const Name = "fuse"`.
- [ ] **Step 1: Move**

```bash
mkdir -p host/fuse
git mv host/fuse_linux.go host/fuse/fuse_linux.go
for f in fuse_linux_test.go fuse_testinroot_linux_test.go fuse_notestinroot_linux_test.go mount_conformance_linux_test.go; do git mv host/$f host/fuse/$f; done
```

Change `package host` → `package fuse`. Qualify `MountServer` as
`host.MountServer`. Remove the temporary `fuseBackend`/`init` added in Task 2
from `fuse_linux.go`.

- [ ] **Step 2: Register** (`host/fuse/backend.go`, no build tag)

```go
// Package fuse mounts a ufs file system on Linux with FUSE. Import it (or
// host/all) to make the "fuse" driver available to host.Mount.
package fuse

// Name is the driver name for host.WithBackend.
const Name = "fuse"

type backend struct{}

func (backend) Name() string                  { return Name }
func (backend) Available() error              { return available() }
func (backend) Suits(types.ReadFS) bool       { return true }
func (backend) Priority() int                 { return 10 }
func (backend) Mount(ctx context.Context, fsys types.ReadFS, p string) (host.MountServer, error) {
	return mount(ctx, fsys, p)
}

func init() { host.RegisterBackend(backend{}) }
```

In `fuse_linux.go` add `func available() error { return nil }` (go-fuse
reports a missing /dev/fuse at mount time). `backend_other.go`
(`//go:build !linux`):

```go
func available() error {
	return fmt.Errorf("FUSE mounts need Linux, not %s", runtime.GOOS)
}

func mount(context.Context, types.ReadFS, string) (host.MountServer, error) {
	return nil, available()
}
```

- [ ] **Step 3: Verify**

Run: `make test` (Linux runs the FUSE tests from `host/fuse`),
`GOOS=windows go vet ./host/...`, `GOOS=darwin go vet ./host/...`.

- [ ] **Step 4: Commit**

```bash
git add -A host
git commit -m "Move the FUSE driver into host/fuse"
```

---

### Task 5: Extract projfslib and move ProjFS into host/projfs

**Files:**

- Create: `host/projfs/internal/projfslib/types_windows.go` (structs and
  constants from `host/projfs_windows.go`), `syscalls_windows.go` (the
  `proc*` vars, `call` (renamed `projfsCall`), `Available` (was
  `projfsAvailable`), exported wrappers `MarkDirectoryAsPlaceholder`,
  `StartVirtualizing`, `StopVirtualizing`, `WritePlaceholderInfo`,
  `WriteFileData`, `AllocateAlignedBuffer`, `FreeAlignedBuffer`,
  `FillDirEntryBuffer`, `FileNameCompare`, `FileNameMatch`),
  `errors_windows.go` (from `host/projfs_errors_windows.go`),
  `layout_windows_test.go`, `errors_windows_test.go`
- Move: `host/mount_windows.go` → `host/projfs/mount_windows.go`,
  `host/mount_windows_test.go`, `host/mount_conformance_windows_test.go` →
  `host/projfs/`
- Create: `host/projfs/backend.go`, `host/projfs/backend_other.go`
- Delete: `host/projfs_windows.go`, `host/projfs_errors_windows.go`

**Interfaces:**

- Produces: package `projfs` with `const Name = "projfs"`; package
  `projfslib` exported API above plus:

```go
type Call string // the projectedfslib function that failed, e.g. "PrjStartVirtualizing"
type Error struct {
	Call    Call
	HRESULT uint32
	Errno   windows.Errno
}
func (e *Error) Error() string
func (e *Error) Unwrap() error
func HRESULTToError(call Call, hr uintptr) error // nil for S_OK
```

`projfslib` must not import anything from `github.com/cloudfra/ufs`.

- [ ] **Step 1: Write the projfslib tests**

`layout_windows_test.go` pins the struct sizes the bindings rely on (values
from `projectedfslib.h`, 64-bit):

```go
func TestLayout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		got, want uintptr
	}{
		{"PlaceholderInfo", unsafe.Sizeof(PlaceholderInfo{}), 344},
		{"CallbackData", unsafe.Sizeof(CallbackData{}), 96},
		{"Callbacks", unsafe.Sizeof(Callbacks{}), 64},
		{"FileBasicInfo", unsafe.Sizeof(FileBasicInfo{}), 56},
		{"NotificationMapping", unsafe.Sizeof(NotificationMapping{}), 16},
		{"StartVirtualizingOptions", unsafe.Sizeof(StartVirtualizingOptions{}), 32},
	} {
		if tc.got != tc.want {
			t.Errorf("sizeof(%s) = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}
```

`errors_windows_test.go`: move `TestHresultToError` from
`host/mount_windows_test.go`, adapted to `HRESULTToError(Call, hr)`, and add:

```go
func TestErrorTable(t *testing.T) {
	t.Parallel()
	for errno, entry := range errorTable {
		if entry.description == "" || entry.resolution[""] == "" {
			t.Errorf("%v: needs a description and a default resolution", errno)
		}
	}
}
```

- [ ] **Step 2: Error table structure** (`errors_windows.go`)

```go
type errorInfo struct {
	description string          // what the error means
	resolution  map[Call]string // what to do, by failing call; "" is the default
}

// errorTable holds every entry of today's projfsResolutions, converted below.
var errorTable = map[windows.Errno]errorInfo{
	windows.ERROR_FILE_SYSTEM_VIRTUALIZATION_UNAVAILABLE: {
		description: "the ProjFS filter driver (PrjFlt) is not running",
		resolution: map[Call]string{
			"": "reboot after enabling ProjFS, or run 'fltmc load PrjFlt' as administrator and check that 'fltmc filters' lists PrjFlt",
		},
	},
	// ... one entry per remaining projfsResolutions key, same shape.
}
```

Convert each `projfsResolutions` entry to `{description, {"": resolution}}`,
splitting today's "cause: fix" strings at the colon. Add call-specific
resolutions where they differ, at least:

- `ERROR_ACCESS_DENIED`: `PrjMarkDirectoryAsPlaceholder`/`PrjStartVirtualizing`
  → "the current user cannot write to the mount directory"; default → "the
  mounted file system refused the change, it may be read-only".
- `ERROR_NOT_SUPPORTED`: `PrjMarkDirectoryAsPlaceholder` → non-NTFS volume;
  default → also mentions partial directories cannot be renamed.

`Error()` renders
`"<call>: <errno message> (HRESULT 0x%08X): <description>; <resolution>"`,
the resolution chosen by `Call` with the default as fallback.

- [ ] **Step 3: Move the driver**

```bash
mkdir -p host/projfs/internal/projfslib
git mv host/mount_windows.go host/projfs/mount_windows.go
git mv host/mount_windows_test.go host/projfs/mount_windows_test.go
git mv host/mount_conformance_windows_test.go host/projfs/mount_conformance_windows_test.go
```

Split `host/projfs_windows.go` into `projfslib/types_windows.go` and
`projfslib/syscalls_windows.go` (export the struct and constant names:
`prjPlaceholderInfo` → `PlaceholderInfo`, `prjNotifyPreDelete` →
`NotifyPreDelete`, ...), then `git rm host/projfs_windows.go`. Move
`host/projfs_errors_windows.go` to `projfslib/errors_windows.go` in the new
structure. Update `mount_windows.go` to call `projfslib.*`, use `package projfs`,
`host.MountServer`, and drop the Task 2 temporary registration.

`host/projfs/backend.go`: like FUSE's, with `Name = "projfs"`,
`Priority() 20`, `Suits(fsys) = host.AllLocal(fsys)`,
`Available() = projfslib.Available()` on Windows. `backend_other.go`
(`//go:build !windows`): `available()` returns "ProjFS mounts need Windows";
`mount` returns it.

- [ ] **Step 4: Verify**

Run: `GOOS=windows go test -c -o /dev/null ./host/...`,
`GOOS=windows build/toolchain/bin/golangci-lint run ./host/...`, `make test`,
`make lint`. Push and confirm `Build Windows` passes the ProjFS tests.

- [ ] **Step 5: Commit**

```bash
git add -A host
git commit -m "Move the ProjFS driver into host/projfs with its own API wrapper"
```

---

### Task 6: host/all, ufsmount -backend, isolation rules

**Files:**

- Create: `host/all/all.go`
- Modify: `cmd/ufsmount/ufsmount.go`, `docs/ufsmount.md`, `.golangci.yml`,
  `CLAUDE.md` (Host mount section and file table)
- [ ] **Step 1: host/all**

```go
// Package all registers every host mount driver. Import it for its side
// effect: import _ "github.com/cloudfra/ufs/host/all".
package all

import (
	_ "github.com/cloudfra/ufs/host/cloudfiles" // add in Task 9
	_ "github.com/cloudfra/ufs/host/fuse"
	_ "github.com/cloudfra/ufs/host/projfs"
)
```

(Until Task 9 lands, omit the cloudfiles import.)

- [ ] **Step 2: ufsmount**

Add `backendFlag = flag.String("backend", "auto", "Host mount driver: auto, fuse, projfs or cloudfiles.")`,
blank-import `host/all`, and pass `host.WithBackend(*backendFlag)` unless it
is `auto`. Document the flag and the selection rule in `docs/ufsmount.md`.

- [ ] **Step 3: depguard rules** (`.golangci.yml`, under `depguard.rules`)

```yaml
        host-core:
          files: ["**/host/*.go"]
          deny:
            - pkg: "github.com/cloudfra/ufs/host/fuse"
              desc: "host must not depend on a driver"
            - pkg: "github.com/cloudfra/ufs/host/projfs"
              desc: "host must not depend on a driver"
            - pkg: "github.com/cloudfra/ufs/host/cloudfiles"
              desc: "host must not depend on a driver"
            - pkg: "github.com/hanwen/go-fuse/v2"
              desc: "only host/fuse may use go-fuse"
        host-drivers-fuse:
          files: ["**/host/fuse/**/*.go"]
          deny:
            - pkg: "github.com/cloudfra/ufs/host/projfs"
              desc: "drivers must not depend on each other"
            - pkg: "github.com/cloudfra/ufs/host/cloudfiles"
              desc: "drivers must not depend on each other"
        host-drivers-projfs:
          files: ["**/host/projfs/**/*.go"]
          deny:
            - pkg: "github.com/cloudfra/ufs/host/fuse"
              desc: "drivers must not depend on each other"
            - pkg: "github.com/cloudfra/ufs/host/cloudfiles"
              desc: "drivers must not depend on each other"
        host-drivers-cloudfiles:
          files: ["**/host/cloudfiles/**/*.go"]
          deny:
            - pkg: "github.com/cloudfra/ufs/host/fuse"
              desc: "drivers must not depend on each other"
            - pkg: "github.com/cloudfra/ufs/host/projfs"
              desc: "drivers must not depend on each other"
        win32-wrappers:
          files:
            - "**/host/projfs/internal/projfslib/**/*.go"
            - "**/host/cloudfiles/internal/cfapi/**/*.go"
          deny:
            - pkg: "github.com/cloudfra/ufs"
              desc: "Win32 API wrappers must not depend on ufs"
```

Check the rule fires: temporarily add `_ "github.com/cloudfra/ufs/host/fuse"`
to `host/host.go`, run `make lint-go`, see a depguard error, remove it.

- [ ] **Step 4: Docs**

`CLAUDE.md` "Host mount" section: replace the file table with the new layout
(host core, host/fuse, host/projfs + projfslib, host/cloudfiles + cfapi,
host/all, host/hosttest, internal/hostutil), the selection rule, and the
`WithBackend` / `-backend` usage.

- [ ] **Step 5: Verify and commit**

Run: `make test`, `make lint`.

```bash
git add -A host cmd docs .golangci.yml CLAUDE.md
git commit -m "Add host/all, the ufsmount -backend flag and driver isolation rules"
```

---

### Task 7: cfapi types and layouts

**Files:**

- Create: `host/cloudfiles/internal/cfapi/doc.go` (package doc, no build tag)
- Create: `host/cloudfiles/internal/cfapi/types_windows.go`
  (`//go:build windows && (amd64 || arm64)`)
- Test: `host/cloudfiles/internal/cfapi/layout_windows_test.go`

**Interfaces:**

- Produces the Go types below. Field names follow `cfapi.h` without the
  `CF_` prefix. Source of truth for every layout:
  `https://raw.githubusercontent.com/microsoft/win32metadata/main/generation/WinSDK/RecompiledIdlHeaders/um/cfapi.h`.

- [ ] **Step 1: Write the layout test**

Windows is LLP64: pointers 8 bytes, `LONG`/`ULONG`/`DWORD`/enums 4 bytes,
`LARGE_INTEGER`/`USN` 8 bytes aligned to 8, `GUID` 16 bytes aligned to 4.
Expected sizes and offsets:

```go
func TestLayout(t *testing.T) {
	t.Parallel()
	check := func(name string, got, want uintptr) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	check("sizeof(FsMetadata)", unsafe.Sizeof(FsMetadata{}), 48)
	check("offsetof(FsMetadata.FileSize)", unsafe.Offsetof(FsMetadata{}.FileSize), 40)

	var pci PlaceholderCreateInfo
	check("sizeof(PlaceholderCreateInfo)", unsafe.Sizeof(pci), 88)
	check("PlaceholderCreateInfo.FsMetadata", unsafe.Offsetof(pci.FsMetadata), 8)
	check("PlaceholderCreateInfo.FileIdentity", unsafe.Offsetof(pci.FileIdentity), 56)
	check("PlaceholderCreateInfo.FileIdentityLength", unsafe.Offsetof(pci.FileIdentityLength), 64)
	check("PlaceholderCreateInfo.Flags", unsafe.Offsetof(pci.Flags), 68)
	check("PlaceholderCreateInfo.Result", unsafe.Offsetof(pci.Result), 72)
	check("PlaceholderCreateInfo.CreateUsn", unsafe.Offsetof(pci.CreateUsn), 80)

	check("sizeof(SyncPolicies)", unsafe.Sizeof(SyncPolicies{}), 24)
	var reg SyncRegistration
	check("sizeof(SyncRegistration)", unsafe.Sizeof(reg), 72)
	check("SyncRegistration.ProviderName", unsafe.Offsetof(reg.ProviderName), 8)
	check("SyncRegistration.SyncRootIdentityLength", unsafe.Offsetof(reg.SyncRootIdentityLength), 32)
	check("SyncRegistration.FileIdentity", unsafe.Offsetof(reg.FileIdentity), 40)
	check("SyncRegistration.ProviderID", unsafe.Offsetof(reg.ProviderID), 52)

	var ci CallbackInfo
	check("sizeof(CallbackInfo)", unsafe.Sizeof(ci), 152)
	check("CallbackInfo.ConnectionKey", unsafe.Offsetof(ci.ConnectionKey), 8)
	check("CallbackInfo.CallbackContext", unsafe.Offsetof(ci.CallbackContext), 16)
	check("CallbackInfo.FileSize", unsafe.Offsetof(ci.FileSize), 80)
	check("CallbackInfo.FileIdentity", unsafe.Offsetof(ci.FileIdentity), 88)
	check("CallbackInfo.FileIdentityLength", unsafe.Offsetof(ci.FileIdentityLength), 96)
	check("CallbackInfo.NormalizedPath", unsafe.Offsetof(ci.NormalizedPath), 104)
	check("CallbackInfo.TransferKey", unsafe.Offsetof(ci.TransferKey), 112)
	check("CallbackInfo.ProcessInfo", unsafe.Offsetof(ci.ProcessInfo), 136)
	check("CallbackInfo.RequestKey", unsafe.Offsetof(ci.RequestKey), 144)

	check("sizeof(CallbackParameters)", unsafe.Sizeof(CallbackParameters{}), 64)
	var fd FetchDataParameters
	check("FetchData.RequiredFileOffset", unsafe.Offsetof(fd.RequiredFileOffset), 16)
	check("FetchData.RequiredLength", unsafe.Offsetof(fd.RequiredLength), 24)
	var rn RenameParameters
	check("Rename.TargetPath", unsafe.Offsetof(rn.TargetPath), 16)
	var fp FetchPlaceholdersParameters
	check("FetchPlaceholders.Pattern", unsafe.Offsetof(fp.Pattern), 16)

	check("sizeof(CallbackRegistration)", unsafe.Sizeof(CallbackRegistration{}), 16)
	var oi OperationInfo
	check("sizeof(OperationInfo)", unsafe.Sizeof(oi), 48)
	check("OperationInfo.ConnectionKey", unsafe.Offsetof(oi.ConnectionKey), 8)
	check("OperationInfo.TransferKey", unsafe.Offsetof(oi.TransferKey), 16)
	check("OperationInfo.RequestKey", unsafe.Offsetof(oi.RequestKey), 40)

	// CF_SIZE_OF_OP_PARAM(member) = offset of the union (8) + sizeof(member).
	check("sizeof(TransferDataParameters)", unsafe.Sizeof(TransferDataParameters{}), 40)
	check("sizeof(TransferPlaceholdersParameters)", unsafe.Sizeof(TransferPlaceholdersParameters{}), 40)
	check("sizeof(AckParameters)", unsafe.Sizeof(AckParameters{}), 16)
}
```

- [ ] **Step 2: Run to see it fail**

Run: `GOOS=windows go test -c -o /dev/null ./host/cloudfiles/internal/cfapi/`
→ undefined types.

- [ ] **Step 3: Write the types** (`types_windows.go`)

```go
// FileBasicInfo maps FILE_BASIC_INFO.
type FileBasicInfo struct {
	CreationTime   int64
	LastAccessTime int64
	LastWriteTime  int64
	ChangeTime     int64
	FileAttributes uint32
	_              [4]byte
}

// FsMetadata maps CF_FS_METADATA.
type FsMetadata struct {
	BasicInfo FileBasicInfo
	FileSize  int64
}

// PlaceholderCreateInfo maps CF_PLACEHOLDER_CREATE_INFO.
type PlaceholderCreateInfo struct {
	RelativeFileName   *uint16
	FsMetadata         FsMetadata
	FileIdentity       unsafe.Pointer
	FileIdentityLength uint32
	Flags              uint32 // PlaceholderCreateFlag*
	Result             int32  // HRESULT
	_                  [4]byte
	CreateUsn          int64
}

// HydrationPolicy maps CF_HYDRATION_POLICY.
type HydrationPolicy struct{ Primary, Modifier uint16 }

// PopulationPolicy maps CF_POPULATION_POLICY.
type PopulationPolicy struct{ Primary, Modifier uint16 }

// SyncPolicies maps CF_SYNC_POLICIES.
type SyncPolicies struct {
	StructSize            uint32
	Hydration             HydrationPolicy
	Population            PopulationPolicy
	InSync                uint32
	HardLink              uint32
	PlaceholderManagement uint32
}

// SyncRegistration maps CF_SYNC_REGISTRATION.
type SyncRegistration struct {
	StructSize             uint32
	_                      [4]byte
	ProviderName           *uint16
	ProviderVersion        *uint16
	SyncRootIdentity       unsafe.Pointer
	SyncRootIdentityLength uint32
	_                      [4]byte
	FileIdentity           unsafe.Pointer
	FileIdentityLength     uint32
	ProviderID             windows.GUID
}

// ProcessInfo maps CF_PROCESS_INFO (fields up to SessionId).
type ProcessInfo struct {
	StructSize    uint32
	ProcessID     uint32
	ImagePath     *uint16
	PackageName   *uint16
	ApplicationID *uint16
	CommandLine   *uint16
	SessionID     uint32
}

// CallbackInfo maps CF_CALLBACK_INFO.
type CallbackInfo struct {
	StructSize             uint32
	_                      [4]byte
	ConnectionKey          int64
	CallbackContext        uintptr
	VolumeGUIDName         *uint16
	VolumeDosName          *uint16
	VolumeSerialNumber     uint32
	_                      [4]byte
	SyncRootFileID         int64
	SyncRootIdentity       unsafe.Pointer
	SyncRootIdentityLength uint32
	_                      [4]byte
	FileID                 int64
	FileSize               int64
	FileIdentity           unsafe.Pointer
	FileIdentityLength     uint32
	_                      [4]byte
	NormalizedPath         *uint16
	TransferKey            int64
	PriorityHint           uint8
	_                      [7]byte
	CorrelationVector      uintptr
	ProcessInfo            *ProcessInfo
	RequestKey             int64
}

// CallbackParameters maps CF_CALLBACK_PARAMETERS as raw bytes; read it
// through the typed views below (each starts with ParamSize).
type CallbackParameters struct {
	ParamSize uint32
	_         [60]byte
}

// FetchDataParameters is the FetchData view of CF_CALLBACK_PARAMETERS.
type FetchDataParameters struct {
	ParamSize             uint32
	_                     [4]byte
	Flags                 uint32
	_                     [4]byte
	RequiredFileOffset    int64
	RequiredLength        int64
	OptionalFileOffset    int64
	OptionalLength        int64
	LastDehydrationTime   int64
	LastDehydrationReason uint32
	_                     [4]byte
}

// CancelParameters is the Cancel view (FetchData range at 16/24).
type CancelParameters struct {
	ParamSize  uint32
	_          [4]byte
	Flags      uint32
	_          [4]byte
	FileOffset int64
	Length     int64
}

// FetchPlaceholdersParameters is the FetchPlaceholders view.
type FetchPlaceholdersParameters struct {
	ParamSize uint32
	_         [4]byte
	Flags     uint32
	_         [4]byte
	Pattern   *uint16
}

// FlagsParameters is the view for CloseCompletion, Delete and the other
// callbacks whose parameters are only Flags.
type FlagsParameters struct {
	ParamSize uint32
	_         [4]byte
	Flags     uint32
}

// RenameParameters is the Rename view.
type RenameParameters struct {
	ParamSize  uint32
	_          [4]byte
	Flags      uint32
	_          [4]byte
	TargetPath *uint16
}

// CallbackRegistration maps CF_CALLBACK_REGISTRATION.
type CallbackRegistration struct {
	Type     uint32
	_        [4]byte
	Callback uintptr
}

// OperationInfo maps CF_OPERATION_INFO.
type OperationInfo struct {
	StructSize        uint32
	Type              uint32 // OperationType*
	ConnectionKey     int64
	TransferKey       int64
	CorrelationVector uintptr
	SyncStatus        uintptr
	RequestKey        int64
}

// TransferDataParameters is CF_OPERATION_PARAMETERS for TRANSFER_DATA.
type TransferDataParameters struct {
	ParamSize        uint32
	_                [4]byte
	Flags            uint32
	CompletionStatus int32 // NTSTATUS
	Buffer           unsafe.Pointer
	Offset           int64
	Length           int64
}

// TransferPlaceholdersParameters is CF_OPERATION_PARAMETERS for
// TRANSFER_PLACEHOLDERS.
type TransferPlaceholdersParameters struct {
	ParamSize             uint32
	_                     [4]byte
	Flags                 uint32
	CompletionStatus      int32
	PlaceholderTotalCount int64
	PlaceholderArray      *PlaceholderCreateInfo
	PlaceholderCount      uint32
	EntriesProcessed      uint32
}

// AckParameters is CF_OPERATION_PARAMETERS for ACK_DELETE and ACK_RENAME.
type AckParameters struct {
	ParamSize        uint32
	_                [4]byte
	Flags            uint32
	CompletionStatus int32
}
```

Constants (values from the header):

```go
const (
	CallbackTypeFetchData               = 0
	CallbackTypeCancelFetchData         = 2
	CallbackTypeFetchPlaceholders       = 3
	CallbackTypeCancelFetchPlaceholders = 4
	CallbackTypeNotifyFileCloseCompletion = 6
	CallbackTypeNotifyDelete            = 9
	CallbackTypeNotifyRename            = 11
	CallbackTypeNone                    = 0xffffffff

	OperationTypeTransferData         = 0
	OperationTypeTransferPlaceholders = 4
	OperationTypeAckDelete            = 6
	OperationTypeAckRename            = 7

	HydrationPolicyProgressive              = 1
	HydrationModifierStreamingAllowed       = 0x0002
	HydrationModifierAutoDehydrationAllowed = 0x0004
	PopulationPolicyFull                    = 2
	InSyncPolicyTrackAll                    = 0x00ffffff
	HardlinkPolicyNone                      = 0
	PlaceholderManagementDefault            = 0

	RegisterFlagUpdate                       = 0x1
	RegisterFlagMarkInSyncOnRoot             = 0x4
	ConnectFlagRequireProcessInfo            = 0x2
	ConnectFlagRequireFullFilePath           = 0x4
	PlaceholderCreateFlagDisableOnDemandPopulation = 0x1
	PlaceholderCreateFlagMarkInSync          = 0x2
	TransferPlaceholdersFlagDisableOnDemandPopulation = 0x2
	ConvertFlagMarkInSync                    = 0x1
	ConvertFlagDehydrate                     = 0x2
	InSyncStateInSync                        = 1
	CallbackCloseCompletionFlagDeleted       = 0x1
	CallbackDeleteFlagIsDirectory            = 0x1
	CallbackRenameFlagIsDirectory            = 0x1
	CallbackRenameFlagSourceInScope          = 0x2
	CallbackRenameFlagTargetInScope          = 0x4
	PlaceholderStatePlaceholder              = 0x1
	PlaceholderStateInSync                   = 0x8
	EOF                                      = -1 // CF_EOF
)
```

`doc.go`:

```go
// Package cfapi wraps the Windows Cloud Files API (cldapi.dll) for the
// cloudfiles host mount driver. It holds only the API: bindings, struct
// layouts, constants and the error table, and imports nothing from ufs.
// It builds on 64-bit Windows only (amd64, arm64).
package cfapi
```

- [ ] **Step 4: Verify**

Push (layout tests run on `Build Windows`); locally
`GOOS=windows go vet ./host/cloudfiles/...` and
`GOOS=windows GOARCH=arm64 go vet ./host/cloudfiles/...`.

- [ ] **Step 5: Commit**

```bash
git add host/cloudfiles
git commit -m "Add the cfapi struct layouts"
```

---

### Task 8: cfapi bindings and error table

**Files:**

- Create: `host/cloudfiles/internal/cfapi/syscalls_windows.go`,
  `errors_windows.go`
- Test: `errors_windows_test.go`

**Interfaces:**

- Produces:

```go
func Available() error
func RegisterSyncRoot(path string, reg *SyncRegistration, policies *SyncPolicies, flags uint32) error
func UnregisterSyncRoot(path string) error
func ConnectSyncRoot(path string, table []CallbackRegistration, ctx uintptr, flags uint32) (connectionKey int64, err error)
func DisconnectSyncRoot(connectionKey int64) error
func Execute(info *OperationInfo, params unsafe.Pointer) error // params: *TransferDataParameters etc.
func ConvertToPlaceholder(h windows.Handle, identity []byte, flags uint32) (usn int64, err error)
func SetInSyncState(h windows.Handle, usn *int64) error
func DehydratePlaceholder(h windows.Handle, offset, length int64) error
func GetPlaceholderStateFromFileInfo(h windows.Handle) (uint32, error) // via GetFileInformationByHandleEx(FileAttributeTagInfo)
type Call string
type Error struct { Call Call; HRESULT uint32; Errno windows.Errno }
func HRESULTToError(call Call, hr uintptr) error
func NTStatus(err error) windows.NTStatus // Go error → callback completion status
```

- [ ] **Step 1: Write the error tests**

```go
func TestErrorTable(t *testing.T) {
	t.Parallel()
	for errno, entry := range errorTable {
		if entry.description == "" || entry.resolution[""] == "" {
			t.Errorf("%v (%d): needs a description and a default resolution", errno, uint32(errno))
		}
	}
	for _, errno := range []windows.Errno{
		windows.ERROR_CLOUD_FILE_SYNC_ROOT_METADATA_CORRUPT, windows.ERROR_CLOUD_FILE_PROVIDER_NOT_RUNNING,
		windows.ERROR_CLOUD_FILE_METADATA_CORRUPT, windows.ERROR_CLOUD_FILE_METADATA_TOO_LARGE,
		windows.ERROR_CLOUD_FILE_PROPERTY_BLOB_TOO_LARGE, windows.ERROR_CLOUD_FILE_PROPERTY_BLOB_CHECKSUM_MISMATCH,
		windows.ERROR_CLOUD_FILE_TOO_MANY_PROPERTY_BLOBS, windows.ERROR_CLOUD_FILE_PROPERTY_VERSION_NOT_SUPPORTED,
		windows.ERROR_NOT_A_CLOUD_FILE, windows.ERROR_CLOUD_FILE_NOT_IN_SYNC,
		windows.ERROR_CLOUD_FILE_ALREADY_CONNECTED, windows.ERROR_CLOUD_FILE_NOT_SUPPORTED,
		windows.ERROR_CLOUD_FILE_INVALID_REQUEST, windows.ERROR_CLOUD_FILE_READ_ONLY_VOLUME,
		windows.ERROR_CLOUD_FILE_CONNECTED_PROVIDER_ONLY, windows.ERROR_CLOUD_FILE_VALIDATION_FAILED,
		windows.ERROR_CLOUD_FILE_AUTHENTICATION_FAILED, windows.ERROR_CLOUD_FILE_INSUFFICIENT_RESOURCES,
		windows.ERROR_CLOUD_FILE_NETWORK_UNAVAILABLE, windows.ERROR_CLOUD_FILE_UNSUCCESSFUL,
		windows.ERROR_CLOUD_FILE_NOT_UNDER_SYNC_ROOT, windows.ERROR_CLOUD_FILE_IN_USE,
		windows.ERROR_CLOUD_FILE_PINNED, windows.ERROR_CLOUD_FILE_REQUEST_ABORTED,
		windows.ERROR_CLOUD_FILE_PROPERTY_CORRUPT, windows.ERROR_CLOUD_FILE_ACCESS_DENIED,
		windows.ERROR_CLOUD_FILE_INCOMPATIBLE_HARDLINKS, windows.ERROR_CLOUD_FILE_PROPERTY_LOCK_CONFLICT,
		windows.ERROR_CLOUD_FILE_REQUEST_CANCELED, windows.ERROR_CLOUD_FILE_PROVIDER_TERMINATED,
		windows.ERROR_NOT_A_CLOUD_SYNC_ROOT, windows.ERROR_CLOUD_FILE_REQUEST_TIMEOUT,
		windows.ERROR_ACCESS_DENIED, windows.ERROR_SHARING_VIOLATION, windows.ERROR_PATH_NOT_FOUND,
		windows.ERROR_DIRECTORY, windows.ERROR_NOT_SUPPORTED, windows.ERROR_INVALID_PARAMETER,
		windows.ERROR_INSUFFICIENT_BUFFER, windows.ERROR_DISK_FULL, windows.ERROR_NOT_ENOUGH_MEMORY,
		windows.ERROR_OUTOFMEMORY, windows.ERROR_MOD_NOT_FOUND, windows.ERROR_PROC_NOT_FOUND,
		windows.ERROR_FILE_NOT_FOUND, windows.ERROR_DIR_NOT_EMPTY, windows.ERROR_ALREADY_EXISTS,
	} {
		if _, ok := errorTable[errno]; !ok {
			t.Errorf("errorTable has no entry for %v (%d)", errno, uint32(errno))
		}
	}
}

func TestErrorMessageDependsOnCall(t *testing.T) {
	t.Parallel()
	hr := uintptr(0x80070000 | uint32(windows.ERROR_ACCESS_DENIED))
	mount := HRESULTToError("CfRegisterSyncRoot", hr).Error()
	del := HRESULTToError("CfExecute(ACK_DELETE)", hr).Error()
	if mount == del {
		t.Errorf("ERROR_ACCESS_DENIED gives the same message for mount and delete: %q", mount)
	}
	if !errors.Is(HRESULTToError("CfExecute(TRANSFER_DATA)", 0x80070002), fs.ErrNotExist) {
		t.Error("HRESULT_FROM_WIN32(ERROR_FILE_NOT_FOUND) does not match fs.ErrNotExist")
	}
	if HRESULTToError("CfRegisterSyncRoot", 0) != nil {
		t.Error("S_OK is not nil")
	}
}

func TestNTStatus(t *testing.T) {
	t.Parallel()
	for err, want := range map[error]windows.NTStatus{
		nil:                  windows.STATUS_SUCCESS,
		fs.ErrNotExist:       windows.STATUS_OBJECT_NAME_NOT_FOUND,
		fs.ErrPermission:     windows.STATUS_ACCESS_DENIED,
		fs.ErrExist:          windows.STATUS_OBJECT_NAME_COLLISION,
		errors.New("other"):  windows.STATUS_CLOUD_FILE_UNSUCCESSFUL,
	} {
		if got := NTStatus(err); got != want {
			t.Errorf("NTStatus(%v) = %#x, want %#x", err, uint32(got), uint32(want))
		}
	}
}
```

- [ ] **Step 2: Implement the bindings** (`syscalls_windows.go`)

Follow `host/projfs/internal/projfslib/syscalls_windows.go`: one
`windows.NewLazySystemDLL("cldapi.dll")`, a `procs` slice that `Available()`
`Find()`s (`CfRegisterSyncRoot`, `CfUnregisterSyncRoot`, `CfConnectSyncRoot`,
`CfDisconnectSyncRoot`, `CfExecute`, `CfConvertToPlaceholder`,
`CfSetInSyncState`, `CfDehydratePlaceholder`, `CfGetPlaceholderStateFromFileInfo`,
`CfGetPlatformInfo`), and

```go
// call calls a Cloud Files function and returns its HRESULT. Cloud Files
// functions do not set the thread's last error, so LazyProc.Call's error
// (from GetLastError) is meaningless.
//
//go:uintptrescapes
func call(proc *windows.LazyProc, args ...uintptr) uintptr {
	r, _, _ := proc.Call(args...) //nolint:errcheck // not set by Cloud Files, see above
	return r
}
```

Every exported wrapper returns `HRESULTToError("<CfName>", hr)`; `Execute`
names the call `CfExecute(<op>)` from `info.Type`
(`TRANSFER_DATA`, `TRANSFER_PLACEHOLDERS`, `ACK_DELETE`, `ACK_RENAME`).
`DisconnectSyncRoot` passes the key by value: `call(proc, uintptr(key))`.
`DehydratePlaceholder` passes the two `LARGE_INTEGER`s by value as
`uintptr(offset), uintptr(length)` (valid on 64-bit only, which is why the
package is 64-bit only). `Available()` also fails with the
`ERROR_MOD_NOT_FOUND` resolution when `cldapi.dll` is missing and
`ERROR_PROC_NOT_FOUND` when a function is.

- [ ] **Step 3: Implement the error table** (`errors_windows.go`)

Same shape as projfslib (`errorInfo{description, resolution map[Call]string}`,
`Error`, `HRESULTToError`). One entry per code in the test list. Descriptions
come from the code's meaning; resolutions are per call. Required call-specific
resolutions (at minimum):

| Code | Call | Resolution |
|:-----|:-----|:-----------|
| ERROR_CLOUD_FILE_PROVIDER_NOT_RUNNING | default | the mount is not connected; it was closed or crashed: remount |
| ERROR_CLOUD_FILE_PROVIDER_NOT_RUNNING | CfDisconnectSyncRoot, CfUnregisterSyncRoot | already disconnected; safe to ignore |
| ERROR_CLOUD_FILE_ALREADY_CONNECTED | CfConnectSyncRoot | another process (another ufsmount?) is serving this directory: stop it or mount elsewhere |
| ERROR_NOT_A_CLOUD_SYNC_ROOT | CfConnectSyncRoot | CfRegisterSyncRoot did not run or was undone: mount on a new empty directory |
| ERROR_CLOUD_FILE_NOT_UNDER_SYNC_ROOT | default | the file is outside the mount directory |
| ERROR_CLOUD_FILE_IN_USE | CfDehydratePlaceholder, CfSetInSyncState | a process has the file open; retried after the next close |
| ERROR_CLOUD_FILE_NOT_IN_SYNC | CfDehydratePlaceholder | the file changed locally and is not uploaded yet; it is uploaded on close |
| ERROR_CLOUD_FILE_PINNED | CfDehydratePlaceholder | the user pinned the file ("Always keep on this device"); it is left hydrated |
| ERROR_CLOUD_FILE_READ_ONLY_VOLUME | default | the volume is read-only: mount on a writable NTFS volume |
| ERROR_CLOUD_FILE_INCOMPATIBLE_HARDLINKS | default | hard links are not supported in a Cloud Files mount: remove them |
| ERROR_CLOUD_FILE_REQUEST_TIMEOUT | CfExecute(TRANSFER_DATA) | the mounted file system answered too slowly (60 s limit); for remote storage check the network |
| ERROR_ACCESS_DENIED | CfRegisterSyncRoot, CfConnectSyncRoot | the current user cannot write to the mount directory, or it is a system/protected folder |
| ERROR_ACCESS_DENIED | CfExecute(ACK_DELETE), CfExecute(ACK_RENAME) | the mounted file system refused the change; it may be read-only |
| ERROR_NOT_SUPPORTED, ERROR_CLOUD_FILE_NOT_SUPPORTED | CfRegisterSyncRoot | Cloud Files needs a local NTFS volume (not ReFS, FAT, a network share or a RAM disk) |
| ERROR_MOD_NOT_FOUND | default | cldapi.dll is missing: Cloud Files needs Windows 10 1709+ or Windows Server 2016+ with Desktop Experience |
| ERROR_PROC_NOT_FOUND | default | cldapi.dll is too old for ufs: update Windows (Windows 10 1709+) |
| ERROR_INVALID_PARAMETER | default | Cloud Files rejected an argument, a bug in the ufs bindings: report it at <https://github.com/cloudfra/ufs/issues> with the log |
| ERROR_FILE_NOT_FOUND | default | the file was removed from the mounted file system |
| ERROR_DIRECTORY | CfRegisterSyncRoot | the mount path is not a directory |
| ERROR_PATH_NOT_FOUND | CfRegisterSyncRoot | create the mount directory first |

Also add the filter-driver case: when `CfConnectSyncRoot` fails with
`ERROR_CLOUD_FILE_PROVIDER_NOT_RUNNING` or `ERROR_NOT_SUPPORTED`, the
resolution includes "check `fltmc filters` lists CldFlt; if not, run
`fltmc load cldflt` as administrator".

`NTStatus(err)`: `nil` → `STATUS_SUCCESS`; `fs.ErrNotExist` →
`STATUS_OBJECT_NAME_NOT_FOUND`; `fs.ErrPermission` → `STATUS_ACCESS_DENIED`;
`fs.ErrExist` → `STATUS_OBJECT_NAME_COLLISION`; an `*Error` whose Errno is in
the cloud-file range maps to the matching `STATUS_CLOUD_FILE_*`; otherwise
`STATUS_CLOUD_FILE_UNSUCCESSFUL`.

- [ ] **Step 4: Verify and commit**

`GOOS=windows go test -c -o /dev/null ./host/cloudfiles/...`, lint for
windows/amd64 and windows/arm64, push, check `Build Windows`.

```bash
git add host/cloudfiles
git commit -m "Add the cfapi bindings and error table"
```

---

### Task 9: Cloud Files spike: mount, list, stream (CI checkpoint)

**Files:**

- Create: `host/cloudfiles/backend.go`, `backend_unsupported.go`
  (`//go:build !(windows && (amd64 || arm64))`),
  `server_windows.go`, `callbacks_windows.go`, `ids_windows.go`
  (all `//go:build windows && (amd64 || arm64)` from here on; "_windows"
  below means that tag)
- Test: `host/cloudfiles/spike_windows_test.go`,
  `host/cloudfiles/ids_windows_test.go`
- Modify: `host/all/all.go` (add the cloudfiles import)

**Interfaces:**

- Consumes: `cfapi.*`, `host.RegisterBackend`, `host.MountServer`.
- Produces: package `cloudfiles`, `const Name = "cloudfiles"`; backend with
  `Priority() 10`, `Suits` always true; internal
  `type server struct` with `mountPath string`, `fsys types.ReadFS`,
  `wfs types.WriteFS` (nil when read-only), `id uintptr`,
  `connKey int64`, `ids *idMap`, `done chan struct{}`, `closeOnce sync.Once`.
- [ ] **Step 1: ID map test and implementation** (`ids_windows.go`)

```go
// idMap maps placeholder IDs (stored in each placeholder's FileIdentity) to
// the placeholder's current path in the mounted file system.
type idMap struct {
	mu    sync.Mutex
	paths map[uint64]string
	ids   map[string]uint64
	last  uint64
}

func newIDMap() *idMap { return &idMap{paths: map[uint64]string{}, ids: map[string]uint64{}} }

// id returns the ID for p, assigning one the first time.
func (m *idMap) id(p string) uint64

// path returns the current path of id.
func (m *idMap) path(id uint64) (string, bool)

// rename moves the IDs at or below oldPath to newPath.
func (m *idMap) rename(oldPath, newPath string)

// remove forgets the IDs at or below p.
func (m *idMap) remove(p string)

// identity encodes id as the 8-byte little-endian FileIdentity.
func identity(id uint64) []byte

// fromIdentity decodes a FileIdentity; ok is false when it is not 8 bytes.
func fromIdentity(p unsafe.Pointer, n uint32) (uint64, bool)
```

Test: assign IDs for `a.txt`, `dir/f.txt`, `dir/sub/g.txt`, `dirx/h.txt`,
rename `dir` → `moved` and `a.txt` → `b.txt`, check `path()` of each, check
`dirx/h.txt` is unchanged, `remove("moved")` drops two IDs, and
`fromIdentity(identity(42))` round-trips.

- [ ] **Step 2: Backend** (`backend.go`)

```go
// Package cloudfiles mounts a ufs file system on Windows with the Cloud
// Files API, the sync-engine API behind OneDrive. Files appear as cloud
// placeholders; reads stream from the mounted file system and writes upload
// when the last handle closes. Import it (or host/all) to make the
// "cloudfiles" driver available to host.Mount.
package cloudfiles

const Name = "cloudfiles"

type backend struct{}

func (backend) Name() string            { return Name }
func (backend) Available() error        { return available() }
func (backend) Suits(types.ReadFS) bool { return true }
func (backend) Priority() int           { return 10 }
func (backend) Mount(ctx context.Context, fsys types.ReadFS, p string) (host.MountServer, error) {
	return mount(ctx, fsys, p)
}

func init() { host.RegisterBackend(backend{}) }
```

`backend_unsupported.go`: `available()` returns
`fmt.Errorf("Cloud Files mounts need 64-bit Windows, not %s/%s", runtime.GOOS, runtime.GOARCH)`;
`mount` returns it. The windows file: `available()` is `cfapi.Available()`.

- [ ] **Step 3: Mount and unmount** (`server_windows.go`)

`mount(ctx, fsys, mountPath)`:

1. `cfapi.Available()`; resolve `mountPath` to an absolute path; `os.ReadDir`
   it and fail unless empty (wrap with a message naming the directory). Check
   the volume is NTFS with `windows.GetVolumeInformation` on the volume root
   (`ERROR_NOT_SUPPORTED` table entry otherwise).
2. Build `SyncRegistration{StructSize, ProviderName "ufs", ProviderVersion
   internal version, SyncRootIdentity = UTF-16 of mountPath,
   ProviderID = providerID}` with
   `var providerID = windows.GUID{0x6f1c3b2a, 0x4d5e, 0x4a7b, [8]byte{0x9c, 0x1d, 0x2e, 0x3f, 0x40, 0x51, 0x62, 0x73}}`
   and `SyncPolicies{StructSize: 24, Hydration: {Progressive, StreamingAllowed|AutoDehydrationAllowed}, Population: {Full, 0}, InSync: TrackAll, HardLink: None}`.
   `cfapi.RegisterSyncRoot(mountPath, &reg, &pol, RegisterFlagUpdate)`.
3. Allocate a server ID (`atomic.Uintptr`), `servers.Store(id, s)`.
4. `cfapi.ConnectSyncRoot(mountPath, table, s.id, RequireProcessInfo|RequireFullFilePath)`
   with `table` built from package-level callbacks created once with
   `windows.NewCallback` (`sync.Once`) for FETCH_PLACEHOLDERS, FETCH_DATA,
   CANCEL_FETCH_DATA, NOTIFY_FILE_CLOSE_COMPLETION, NOTIFY_DELETE,
   NOTIFY_RENAME, terminated by `{Type: CallbackTypeNone}`.
5. On any failure after registration: unregister and `servers.Delete`.
6. Close on `ctx.Done()` in a goroutine, as the ProjFS server does.

`Close()` (once): `cfapi.DisconnectSyncRoot`, `cfapi.UnregisterSyncRoot`,
remove the placeholders (Task 13 refines this), `servers.Delete(s.id)`,
`close(s.done)`. `Wait()` blocks on `s.done`.

- [ ] **Step 4: Listing and reading** (`callbacks_windows.go`)

Every callback has the signature
`func(info *cfapi.CallbackInfo, params *cfapi.CallbackParameters) uintptr`,
starts with `s := serverFor(info)` (`servers.Load(info.CallbackContext)`;
nil → log and return 0) and computes the request's backing path with
`s.backingPath(info)`: decode `info.FileIdentity` with `fromIdentity` and look
it up in `s.ids`; else convert `info.NormalizedPath` (a volume-relative path
like `\Users\me\mnt\dir\a.txt`) to a path relative to the sync root and to
forward slashes; the root is `"."`.

- `fetchPlaceholders`: `ReadDir(dir)`. For each entry build a
  `PlaceholderCreateInfo` with `RelativeFileName` (name only),
  `FsMetadata` (sizes, `windows.NsecToFiletime(modTime)` for all four times,
  `FILE_ATTRIBUTE_DIRECTORY` or `FILE_ATTRIBUTE_NORMAL`, plus
  `FILE_ATTRIBUTE_READONLY` when `s.wfs == nil`), `FileIdentity` =
  `identity(s.ids.id(path.Join(dir, name)))` (keep the byte slices alive in a
  local slice until `Execute` returns), flags
  `PlaceholderCreateFlagMarkInSync`, plus
  `PlaceholderCreateFlagDisableOnDemandPopulation` for files. Send them with
  one `Execute` (`OperationTypeTransferPlaceholders`, flags
  `TransferPlaceholdersFlagDisableOnDemandPopulation`,
  `PlaceholderTotalCount = len`, `CompletionStatus = STATUS_SUCCESS`). On a
  ReadDir error, `Execute` with `CompletionStatus = cfapi.NTStatus(err)` and
  no entries.
- `fetchData`: view params as `*cfapi.FetchDataParameters`. Open the backing
  file; read `[RequiredFileOffset, RequiredFileOffset+RequiredLength)` (clamped
  to the file size) in chunks of at most 4 MiB, each chunk offset 4 KiB
  aligned, the last ending at EOF; for each chunk `Execute`
  (`OperationTypeTransferData`, `TransferDataParameters{Buffer, Offset,
  Length, CompletionStatus: STATUS_SUCCESS}`). On a read error, one `Execute`
  over the remaining range with `CompletionStatus = cfapi.NTStatus(err)`.
  Use a `sync.Pool` of 4 MiB buffers. Track in-flight transfers by
  `info.TransferKey` in a map of `context.CancelFunc`.
- `cancelFetchData`: cancel the transfer for `info.TransferKey`.
- Every `OperationInfo` copies `ConnectionKey`, `TransferKey` and
  `RequestKey` from `info` and sets `StructSize = 48`.
- [ ] **Step 5: Spike tests** (`spike_windows_test.go`)

```go
func testMount(t *testing.T, fsys types.ReadFS) string {
	t.Helper()
	dir := t.TempDir()
	s, err := host.Mount(t.Context(), fsys, dir, host.WithBackend(Name))
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return dir
}

// fileStandardInfo maps FILE_STANDARD_INFO.
type fileStandardInfo struct {
	AllocationSize int64
	EndOfFile      int64
	NumberOfLinks  uint32
	DeletePending  bool
	Directory      bool
}

// allocationSize returns how many bytes of name are stored on the local disk.
func allocationSize(t *testing.T, name string) int64 {
	t.Helper()
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatalf("CreateFile(%s): %v", name, err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // read-only handle
	var info fileStandardInfo
	if err := windows.GetFileInformationByHandleEx(h, windows.FileStandardInfo,
		(*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		t.Fatalf("GetFileInformationByHandleEx(%s): %v", name, err)
	}
	return info.AllocationSize
}

func memFS(t *testing.T, files map[string][]byte) types.WriteFS {
	t.Helper()
	fsys, err := ufs.New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	for name, data := range files {
		if err := ufs.BatchCreate(fsys, ufs.BatchFile{Name: name, Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	return fsys
}

// TestReadStreams reads a 16 MiB file fully through the mount and checks
// that Windows kept no local copy of it (allocation size 0).
func TestReadStreams(t *testing.T) {
	t.Parallel()
	want := bytes.Repeat([]byte("0123456789abcdef"), 1<<20)
	dir := testMount(t, memFS(t, map[string][]byte{"big.bin": want}))
	name := filepath.Join(dir, "big.bin")
	got, err := osutil.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReadFile returned %d bytes that differ from the %d written", len(got), len(want))
	}
	if n := allocationSize(t, name); n != 0 {
		t.Errorf("allocation size after a full read = %d, want 0 (data was stored locally)", n)
	}
}

// TestProviderProcessRead reads through the mount from this process, the
// provider, answering whether Cloud Files serves the provider's own I/O.
func TestProviderProcessRead(t *testing.T) {
	t.Parallel()
	dir := testMount(t, memFS(t, map[string][]byte{"a.txt": []byte("a")}))
	got, err := osutil.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil || string(got) != "a" {
		t.Fatalf("ReadFile from the provider process = %q, %v, want %q", got, err, "a")
	}
}

// TestUnmountEmpties checks the mount directory is empty after Close.
func TestUnmountEmpties(t *testing.T) {
	t.Parallel()
	fsys := memFS(t, map[string][]byte{"a.txt": []byte("a"), "d/b.txt": []byte("b")})
	dir := t.TempDir()
	s, err := host.Mount(t.Context(), fsys, dir, host.WithBackend(Name))
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if _, err := osutil.ReadDir(filepath.Join(dir, "d")); err != nil {
		t.Fatalf("ReadDir through the mount: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir after Close: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("mount directory after Close has %d entries, want 0", len(entries))
	}
}
```

Also add `TestMountConformance` running `hosttest.Run(t, hosttest.Backend{Name: Name, Mount: testMount})`
with zero `Caps` (read tests only until Task 12).

- [ ] **Step 6: CI checkpoint (STOP here)**

Push and read the `Build Windows` and `build-windows-root` results. Record the
answers in this plan under "Spike results" below and in the PR:

1. `TestReadStreams`: does allocation stay 0?
2. `TestProviderProcessRead`: does the provider's own read work? (If not,
   set `Caps.ChildIO` for cloudfiles and use `hosttest` child ops.)
3. `TestUnmountEmpties`: are placeholders removable after unregister?
4. Does `build-windows-root` (Server Core) load cldapi.dll and mount?

If 1 or 3 fails, or 4 fails, stop and bring the result to the user before
Task 10 (the spec says a "no" changes the design). Otherwise continue.

- [ ] **Step 7: Commit**

```bash
git add host
git commit -m "Add the Cloud Files driver read path"
```

#### Spike results

(Fill in after Task 9 Step 6.)

---

### Task 10: Upload on close

**Files:**

- Create: `host/cloudfiles/sync_windows.go`
- Modify: `callbacks_windows.go` (close completion), `server_windows.go`
  (start/stop the watcher and queue)
- Test: `host/cloudfiles/write_windows_test.go`

**Interfaces:**

- Produces: `type syncer struct` with `enqueue(rel string)`, `run(ctx)`,
  `flush(ctx) []string` (returns files still open), owned by `server`.

- [ ] **Step 1: Failing tests** (`write_windows_test.go`)

Each test mounts a `memory:` fs (writable) and works through the mount from
this process (or `hosttest` child ops if Spike result 2 said so), then polls
the backing fs for up to 10 s:

- `TestWriteNewFile`: write `new.txt`, close; backing has the content;
  the local file is an in-sync placeholder
  (`cfapi.GetPlaceholderStateFromFileInfo` has `PlaceholderStateInSync`);
  allocation size returns to 0 (dehydrated).
- `TestOverwritePlaceholder`: backing has `a.txt` = "old"; overwrite through
  the mount with "new"; backing becomes "new".
- `TestAppend`: append to a placeholder; backing has both parts.
- `TestMkdir`: `os.Mkdir` through the mount; backing has the directory; a
  file written inside it uploads too.
- `TestChangedDuringUpload`: use a slow backing fs (wrap memFS so `Create`
  sleeps 500 ms), write, close, reopen and write again during the upload;
  the final backing content is the second write and the file ends in-sync.
- `TestReadOnlyLocalCreate`: on a read-only mount, creating a file locally is
  not uploaded and is logged (check with a `slog` handler capture).
- [ ] **Step 2: Implement the syncer** (`sync_windows.go`)
- Watcher: a goroutine on `windows.ReadDirectoryChanges(dirHandle, buf, true,
  FILE_NOTIFY_CHANGE_FILE_NAME|FILE_NOTIFY_CHANGE_DIR_NAME|FILE_NOTIFY_CHANGE_LAST_WRITE|FILE_NOTIFY_CHANGE_SIZE, ...)`
  on the sync root opened with `FILE_LIST_DIRECTORY` and
  `FILE_FLAG_BACKUP_SEMANTICS|FILE_FLAG_OVERLAPPED`; each `FILE_ACTION_ADDED`,
  `MODIFIED` and `RENAMED_NEW_NAME` record enqueues its relative path.
  `REMOVED` drops it from the queue. Cancel with `windows.CancelIoEx` on
  close.
- `closeCompletion` callback: unless `CallbackCloseCompletionFlagDeleted`,
  enqueue `s.backingPath(info)`.
- Queue: a set of pending relative paths and one worker goroutine.
- For each path, `os.Lstat` the local file:
  - Directory: if not a placeholder, `wfs.MkdirAll(rel)`, then open with
    `FILE_FLAG_BACKUP_SEMANTICS` and
    `cfapi.ConvertToPlaceholder(h, identity(ids.id(rel)), ConvertFlagMarkInSync)`.
  - File: open with `GENERIC_READ`, share `FILE_SHARE_READ` only. On
    `ERROR_SHARING_VIOLATION` leave it (the next close re-queues it). If the
    file is an in-sync placeholder, skip. Read the USN with
    `FSCTL_READ_FILE_USN_DATA` (`windows.DeviceIoControl`), copy the content
    to `ufs.BatchCreate(wfs, ufs.BatchFile{Name: rel, Data: data})` (read
    whole files up to 64 MiB; above that stream with `wfs.Create` +
    `io.Copy`). If it is not a placeholder yet:
    `ConvertToPlaceholder(h, identity(id), ConvertFlagMarkInSync)`; else
    `SetInSyncState(h, &usn)`. If that fails because the USN changed
    (`ERROR_CLOUD_FILE_NOT_IN_SYNC`), re-queue.
  - Then dehydrate: open the file with `FILE_WRITE_ATTRIBUTES` and call
    `cfapi.DehydratePlaceholder(h, 0, cfapi.EOF)`; ignore
    `ERROR_CLOUD_FILE_IN_USE` and `ERROR_CLOUD_FILE_PINNED` (logged at Debug).
- Read-only mount (`s.wfs == nil`): log new paths once at Warn and skip.
- [ ] **Step 3: Verify and commit**

Push; `Build Windows` runs the write tests.

```bash
git add host/cloudfiles
git commit -m "Upload Cloud Files mount writes on close"
```

---

### Task 11: Delete and rename

**Files:**

- Modify: `host/cloudfiles/callbacks_windows.go`
- Test: `host/cloudfiles/write_windows_test.go`
- [ ] **Step 1: Failing tests**
- `TestDeleteFile`, `TestDeleteDir` (with children): backing loses them.
- `TestRenameFile`, `TestRenameDir`: backing has the new names; reading the
  renamed file through the mount returns its content (ID map).
- `TestRenameOutOfMount`: move a file to `t.TempDir()`; backing loses it.
- `TestReadOnlyDeleteRenameDenied`: on a read-only mount both fail with
  access denied and the local placeholder remains.
- `TestDeleteBackingFailure`: a backing fs whose `Remove` fails makes the
  delete fail locally (file still listed).
- [ ] **Step 2: Implement**
- `notifyDelete`: `rel := s.backingPath(info)`. Read-only →
  ack with `STATUS_ACCESS_DENIED`. Directory
  (`CallbackDeleteFlagIsDirectory`) → `wfs.RemoveAll(rel)`, else
  `wfs.Remove(rel)`; `fs.ErrNotExist` counts as success. On success
  `s.ids.remove(rel)`. Ack with
  `Execute(OperationInfo{Type: OperationTypeAckDelete, ...}, &AckParameters{ParamSize: 16, CompletionStatus: int32(cfapi.NTStatus(err))})`.
- `notifyRename`: view as `*cfapi.RenameParameters`; target relative path
  from `TargetPath` (volume-relative, like `NormalizedPath`). Read-only →
  deny. Target outside the sync root
  (`Flags&CallbackRenameFlagTargetInScope == 0`) → treat as delete. Else
  `ops.Rename(wfs, rel, target)` then `s.ids.rename(rel, target)`. Ack with
  `OperationTypeAckRename`.
- [ ] **Step 3: Verify and commit**

```bash
git add host/cloudfiles
git commit -m "Apply Cloud Files deletes and renames to the mounted file system"
```

---

### Task 12: Write conformance tests for every driver

**Files:**

- Create: `host/hosttest/write.go`
- Modify: `host/hosttest/hosttest.go` (register the write tests when
  `b.Caps.Write`), `host/fuse/mount_conformance_linux_test.go`,
  `host/projfs/mount_conformance_windows_test.go`,
  `host/cloudfiles/spike_windows_test.go` (Caps for cloudfiles:
  `Write: true, DirRename: true`, `ChildIO` per Spike result 2)
- [ ] **Step 1: Add the tests** (`write.go`)

Using `WriteFile`, `Rename`, `Remove` from `childop.go` and polling the
backing fs for up to 10 s (writes may be asynchronous):

- `create`: write `c.txt` = "c"; backing has "c".
- `overwrite`: backing `o.txt` = "old"; write "new"; backing "new".
- `appendFile` (in-process `os.OpenFile(O_APPEND)` unless ChildIO, then a
  child "append" op): backing "ab".
- `mkdir`: `os.Mkdir` (child op "mkdir" when ChildIO); backing has the dir.
- `removeFile`, `removeDir` when `Caps.Delete`: backing loses them.
- `renameFile`, and `renameDir` when `Caps.DirRename`.
- `readOnly`: mount a read-only wrapper; delete and rename fail.

Cloud Files Caps: `Write: true, Delete: true, DirRename: true`, and
`ChildIO` from Spike result 2.

- [ ] **Step 2: Verify and commit**

`make test` (FUSE), push for Windows.

```bash
git add host
git commit -m "Run write conformance tests against every host mount driver"
```

---

### Task 13: Unmount cleanup and crashed-mount recovery

**Files:**

- Modify: `host/cloudfiles/server_windows.go`
- Test: `host/cloudfiles/lifecycle_windows_test.go`
- [ ] **Step 1: Failing tests**
- `TestCloseKeepsUnsynced`: on a read-only mount create `local.txt`, Close;
  the directory holds only `local.txt` and Close's log names it.
- `TestCloseFlushes`: write a file and Close immediately; backing has it.
- `TestRemountAfterCrash`: register a sync root on a directory with
  `cfapi.RegisterSyncRoot` (same provider ID) and do not unregister
  (simulates a crash); `Mount` on it succeeds.
- `TestMountNonEmptyFails`: a directory with a regular file fails with an
  error naming the directory.
- [ ] **Step 2: Implement**
- Close: stop the watcher; `flush` the queue (log files still open by name);
  `DisconnectSyncRoot`; `UnregisterSyncRoot`; walk the directory bottom-up
  and remove every entry that is a placeholder
  (`GetPlaceholderStateFromFileInfo & PlaceholderStatePlaceholder`) and in
  sync; log the rest at Warn as unsynced.
- Mount: if the directory is not empty, it is accepted only when the
  directory itself has the `PlaceholderStateSyncRoot` (0x2) bit (add the
  constant to cfapi) and every entry under it is a placeholder. Then
  `UnregisterSyncRoot`, remove the in-sync placeholders as Close does, and
  register as usual. Otherwise fail with an error naming the directory.
- [ ] **Step 3: Verify and commit**

```bash
git add host/cloudfiles
git commit -m "Clean up Cloud Files mounts on close and recover crashed ones"
```

---

### Task 14: CI, docs, presubmit, PR ready

**Files:**

- Modify: `.github/workflows/deploy.yaml` (nothing to add for Cloud Files;
  confirm no env gating), `docs/ufsmount.md`, `CLAUDE.md`, this plan
  (tick boxes, Spike results), the spec if the spike changed anything.

- [ ] **Step 1: Docs**

`docs/ufsmount.md`: platform table (Linux FUSE; Windows Cloud Files default,
ProjFS when installed and local), `-backend`, what each driver supports
(Cloud Files: read streaming, write on close, delete/rename, no
backing-side change propagation; ProjFS: as today), Windows examples with
Cloud Files. `CLAUDE.md`: Host mount section final state.

- [ ] **Step 2: Presubmit**

Run: `make presubmit`. Fix anything it reports.

- [ ] **Step 3: Squash and push**

```bash
git fetch origin
git rebase origin/main
git reset --soft origin/main
git commit   # one commit; message summarizing the whole change + attribution lines
git push --force-with-lease
```

- [ ] **Step 4: PR**

Update the PR description (what changed, spike results, testing), mark it
ready (`gh pr ready`), and wait for every check to pass.
