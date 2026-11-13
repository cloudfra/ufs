// Copyright 2026 Cloudfra
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package testing

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudfra/ufs/drivers/testing/eventtest"
	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// Every case of the [types.Watcher] suite builds a file system fresh, points a
// watcher at watchDir, and then changes that file system through its own write
// API. Building it fresh each time is what keeps a watcher left running by one
// case from reporting another case's changes. Delivered paths are compared
// against the file system root, never against the watched directory, because
// that is the shape the contract promises.
//
// Only operations every implementation agrees on are pinned. When a file system
// reports the same change as a different operation, or reports changes the
// others leave alone, that is left to the driver's own tests and logged here.
//
// Two clauses of the interface go unchecked, and knowing which ones is what
// makes the suite's green result readable. The "must terminate all background
// goroutines" half of closing is not checked at all: nothing in this package
// counts goroutines, so a closer that returns nil and leaves its watcher
// running passes. The serial-delivery clause is checked, through
// [watchProbe.assertSerial] running as cleanup for every watcher this suite
// starts. A file system that will not watch a directory it does hold skips the
// delivery cases in every sub-suite but the two that check refusals, which is
// how a file system whose notifications come from outside its own mutations
// shows up here.
const (
	// watchDir is the directory the watchers watch.
	watchDir = "watch"
	// watchFile is created, rewritten, removed and renamed by the cases.
	watchFile = watchDir + "/file.txt"
	// watchSentinelFile is written after a change that must not be reported:
	// notifications are delivered serially, so once the sentinel has arrived
	// anything the watcher was going to say about the earlier change has been
	// said already.
	watchSentinelFile = watchDir + "/sentinel.txt"
	// watchNestedDir, watchNestedFile, watchDeepDir and watchDeepFile are
	// created only after the watch starts, which is what makes the recursive
	// half of the contract testable.
	watchNestedDir  = watchDir + "/nested"
	watchNestedFile = watchNestedDir + "/file.txt"
	watchDeepDir    = watchNestedDir + "/deep"
	watchDeepFile   = watchDeepDir + "/file.txt"
	// watchRenamedFile is where watchFile goes in the rename case.
	watchRenamedFile = watchDir + "/renamed.txt"
	// watchOtherDir, watchOtherFile and watchOutsideFile sit outside watchDir,
	// so a watcher on watchDir has to stay quiet about them.
	watchOtherDir    = "other"
	watchOtherFile   = watchOtherDir + "/file.txt"
	watchOutsideFile = "outside.txt"
	// watchMissingDir is never created.
	watchMissingDir = "missing"
)

// watchSilenceWindow is how long a watcher that should have stopped delivering
// is given the chance to prove it before the suite accepts the silence.
const watchSilenceWindow = 500 * time.Millisecond

// watchHookHold is how long the probe's hook stays busy handling one
// notification. A hook that returns immediately makes overlapping delivery
// unobservable, so the hold is what lets [watchProbe.assertSerial] see a file
// system that calls the hook from more than one goroutine at a time.
const watchHookHold = 2 * time.Millisecond

// watcherInvalidNames extends invalidPaths with the names only Watch has to
// refuse: the root spelled with a dot element, and paths holding a dot or
// dot-dot element in the middle.
var watcherInvalidNames = append(invalidPaths, "./watch", "watch/./file.txt", "watch/../outside.txt")

// watchMissingNames are the watch targets that no file system holds.
var watchMissingNames = []string{watchMissingDir, watchMissingDir + "/deeper"}

// watchOpNames names each [types.NotifyOp] for failure messages: the type has no
// String method, so a report holding a bare integer cannot be read.
var watchOpNames = [...]string{"Create", "Write", "Remove", "Rename", "Chmod"}

// watcherRefusalFS is the minimum set of interfaces watcherInvalidPaths needs:
// it only asks Watch for names that have to be refused, which every file system
// checks before it looks anything up, so nothing has to be seeded.
type watcherRefusalFS interface {
	types.SimpleFS
	types.Watcher
}

// watcherTargetFS is the minimum set watcherBadTargets needs: it seeds a file to
// point Watch at, and asks for targets that are missing or are files.
type watcherTargetFS interface {
	types.SimpleFS
	types.Watcher
	types.MkdirAllFS
	types.CreateFileFS
}

// watcherMutateFS is the minimum set the delivery cases need: they start a
// watcher, then change the tree through the file system's own write API.
type watcherMutateFS interface {
	types.SimpleFS
	types.Watcher
	types.MkdirAllFS
	types.CreateFileFS
	types.RemoveFS
}

// watcherRenameFS adds the rename the rename case performs.
type watcherRenameFS interface {
	watcherMutateFS
	types.RenameFileFS
}

// watcherRemoveAllFS adds the subtree remove the RemoveAll case performs.
type watcherRemoveAllFS interface {
	watcherMutateFS
	types.RemoveAllFS
}

// watcherSeedFS is the set of interfaces newWatchFS uses to seed the tree the
// watchers sit in.
type watcherSeedFS interface {
	types.SimpleFS
	types.MkdirAllFS
	types.CreateFileFS
}

// watcher runs the [types.Watcher] conformance suite.
func watcher(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("Watcher", func(t *testing.T) {
		requireType[types.Watcher](t, args)
		t.Parallel()
		watcherInvalidPaths(t, requireFSTypeFunc[watcherRefusalFS](t, createFSFunc))
		watcherBadTargets(t, requireFSTypeFunc[watcherTargetFS](t, createFSFunc))
		watcherCreateRemove(t, requireFSTypeFunc[watcherMutateFS](t, createFSFunc))
		watcherRootDir(t, requireFSTypeFunc[watcherMutateFS](t, createFSFunc))
		watcherRecursion(t, requireFSTypeFunc[watcherMutateFS](t, createFSFunc))
		watcherScoped(t, requireFSTypeFunc[watcherMutateFS](t, createFSFunc))
		watcherOverwrite(t, requireFSTypeFunc[watcherMutateFS](t, createFSFunc))
		watcherTwoWatchers(t, requireFSTypeFunc[watcherMutateFS](t, createFSFunc))
		watcherStopDelivery(t, requireFSTypeFunc[watcherMutateFS](t, createFSFunc))
		watcherRename(t, requireFSTypeFunc[watcherRenameFS](t, createFSFunc))
		watcherRemoveAll(t, requireFSTypeFunc[watcherRemoveAllFS](t, createFSFunc))
	})
}

// watcherInvalidPaths checks that Watch refuses an invalid name before it looks
// anything up. Every file system gets this check from [pathutil.Validate], so
// the Op and Path of the [fs.PathError] are fixed as well as the sentinel.
func watcherInvalidPaths(t *testing.T, createFSFunc func(t *testing.T) watcherRefusalFS) {
	t.Run("WatchInvalidPaths", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		for _, name := range watcherInvalidNames {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				closer, err := fsys.Watch(t.Context(), name, watchNoHook)
				ufsTesting.AssertInvalidPathError(t, name, err, "watch")
				// AssertInvalidPathError checks the shape of the error only, so
				// the sentinel is checked here too: a file system that started
				// watching an invalid name reports the same Op and Path for a
				// name that is simply not there.
				if !errors.Is(err, fs.ErrInvalid) {
					t.Errorf("Watch(%q) = %v, want an error wrapping %v", name, err, fs.ErrInvalid)
				}
				// Whatever a file system that refuses returns, it must not hand
				// back a watcher that goes on running.
				if closer != nil {
					if err := closer.Close(); err != nil {
						t.Errorf("Watch(%q) = %v and Close() = %v, want no error from either: a refused watch must not leave anything running", name, err, closer)
					}
				}
			})
		}
	})
}

// watcherBadTargets checks the ways a watch target can be wrong that are not
// about its spelling: a directory that is not there, and a name that is a file
// rather than a directory.
func watcherBadTargets(t *testing.T, createFSFunc func(t *testing.T) watcherTargetFS) {
	t.Run("WatchBadTargets", func(t *testing.T) {
		t.Parallel()
		// Every case here is about how a file system answers a lookup, so they
		// only mean something for a file system that gets as far as looking.
		// *gcsfs.gcsFS refuses every watch when its bucket holds no
		// publish/subscribe subscription, before it looks at name at all.
		skipIfNoWatch(t, newWatchFS(t, createFSFunc), watchDir)

		for _, name := range watchMissingNames {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				fsys := newWatchFS(t, createFSFunc)
				_, err := fsys.Watch(t.Context(), name, watchNoHook)
				if !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("Watch(%q) = %v, want an error wrapping %v", name, err, fs.ErrNotExist)
				}
				// The error has to name the target the caller asked for, not
				// something the file system resolved it to on the way down.
				var pathErr *fs.PathError
				if !errors.As(err, &pathErr) {
					t.Errorf("Watch(%q) = %T %v, want an *fs.PathError", name, err, err)
				} else {
					if pathErr.Path != name {
						t.Errorf("Watch(%q) Path = %q, want %q", name, pathErr.Path, name)
					}
					if pathErr.Op != "watch" {
						t.Errorf(`Watch(%q) Op = %q, want "watch"`, name, pathErr.Op)
					}
				}
			})
		}

		t.Run("File", func(t *testing.T) {
			t.Parallel()
			fsys := newWatchFS(t, createFSFunc)
			WriteFile(t, fsys, watchFile, "not a directory")
			_, err := fsys.Watch(t.Context(), watchFile, watchNoHook)
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Watch(%q) = %v, want an error wrapping %v: Watch takes a directory", watchFile, err, fs.ErrInvalid)
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf(`Watch(%q) = %T %v, want an *fs.PathError with Op "watch" and Path %q`, watchFile, err, err, watchFile)
			} else if pathErr.Path != watchFile || pathErr.Op != "watch" {
				t.Errorf(`Watch(%q) = %+v, want Op "watch" and Path %q`, watchFile, pathErr, watchFile)
			}
		})

		t.Run("BelowFile", func(t *testing.T) {
			t.Parallel()
			fsys := newWatchFS(t, createFSFunc)
			WriteFile(t, fsys, watchFile, "not a directory")
			// A name below a file is either a missing directory or not a
			// directory, and the file systems differ about which, so what is
			// pinned here is only that it is refused at all.
			if _, err := fsys.Watch(t.Context(), watchFile+"/below", watchNoHook); err == nil {
				t.Errorf("Watch(%q) = nil, want an error: %q is a file", watchFile+"/below", watchFile)
			}
		})

		t.Run("Root", func(t *testing.T) {
			t.Parallel()
			fsys := newWatchFS(t, createFSFunc)
			closer, err := fsys.Watch(t.Context(), ".", watchNoHook)
			if err != nil {
				t.Fatalf(`Watch(".") = %v, want nil: the root is a directory that can be watched`, err)
			}
			if closer == nil {
				t.Fatalf(`Watch(".") returned a nil io.Closer, want one that can be closed`)
			}
			if err := closer.Close(); err != nil {
				t.Errorf(`Close after Watch(".") = %v, want nil`, err)
			}
		})
	})
}

// watcherCreateRemove checks the two operations every implementation agrees on
// for a plain file: creating it is reported as Create carrying its
// root-relative path, and removing it is reported as Remove.
func watcherCreateRemove(t *testing.T, createFSFunc func(t *testing.T) watcherMutateFS) {
	t.Run("WatchCreateRemove", func(t *testing.T) {
		t.Parallel()
		fsys := newWatchFS(t, createFSFunc)
		probe := newWatchProbe(t.Context(), t, fsys, watchDir)

		WriteFile(t, fsys, watchFile, "hello")
		probe.waitFor(types.NotifyCreate, watchFile)

		if err := fsys.Remove(watchFile); err != nil {
			t.Fatalf("Remove(%q) = %v, want nil", watchFile, err)
		}
		probe.waitFor(types.NotifyRemove, watchFile)

		// The notification names the entry that changed, not the watched
		// directory it happens to sit in.
		if probe.hasEvent(types.NotifyCreate, watchDir) {
			t.Errorf("Watch delivered Create for %q, want one for %q only: the watched directory did not change", watchDir, watchFile)
		}
		probe.assertPaths()
	})
}

// watcherRootDir checks that watching the root covers the whole tree and still
// reports paths from the root: neither the empty prefix nor "./" is stripped or
// added on the way out.
func watcherRootDir(t *testing.T, createFSFunc func(t *testing.T) watcherMutateFS) {
	t.Run("WatchRootDir", func(t *testing.T) {
		t.Parallel()
		fsys := newWatchFS(t, createFSFunc)
		probe := newWatchProbe(t.Context(), t, fsys, ".")

		WriteFile(t, fsys, watchFile, "hello")
		probe.waitFor(types.NotifyCreate, watchFile)
		probe.assertPaths()
	})
}

// watcherRecursion checks the recursive half of the contract: a directory
// created after the watch started is watched itself, at every level. Each level
// is created and waited for on its own, because a file system that walks its
// watched tree looking for new directories can be handed a file under a
// directory it has not added to the watch yet.
//
// A new directory has to be reported at all: it is a change at or below the
// watched name.
func watcherRecursion(t *testing.T, createFSFunc func(t *testing.T) watcherMutateFS) {
	t.Run("WatchRecursion", func(t *testing.T) {
		t.Parallel()
		fsys := newWatchFS(t, createFSFunc)
		probe := newWatchProbe(t.Context(), t, fsys, watchDir)

		for _, dir := range []string{watchNestedDir, watchDeepDir} {
			mkdirForTest(t, fsys, dir)
			probe.waitPath(dir)
		}

		WriteFile(t, fsys, watchDeepFile, "deep")
		probe.waitFor(types.NotifyCreate, watchDeepFile)
		WriteFile(t, fsys, watchNestedFile, "nested")
		probe.waitFor(types.NotifyCreate, watchNestedFile)

		probe.assertPaths()
	})
}

// watcherScoped checks that a watcher reports only what sits at or below the
// name it was given. The file written inside the watched directory is the
// sentinel that bounds the check: notifications are delivered serially, so by
// the time the sentinel has arrived the watcher has said everything it is going
// to say about the changes made outside.
func watcherScoped(t *testing.T, createFSFunc func(t *testing.T) watcherMutateFS) {
	t.Run("WatchScoped", func(t *testing.T) {
		t.Parallel()
		fsys := newWatchFS(t, createFSFunc)
		probe := newWatchProbe(t.Context(), t, fsys, watchDir)

		WriteFile(t, fsys, watchOtherFile, "in another directory")
		WriteFile(t, fsys, watchOutsideFile, "at the root")
		WriteFile(t, fsys, watchSentinelFile, "sentinel")
		probe.waitPath(watchSentinelFile)

		for _, name := range []string{watchOtherFile, watchOutsideFile} {
			probe.assertQuietPath(name, "changes at the root and in another directory")
		}
		probe.assertPaths()
	})
}

// watcherOverwrite checks the two halves of what rewriting a file is allowed to
// look like: the change is reported, and a file that already existed is not
// reported as created a second time.
func watcherOverwrite(t *testing.T, createFSFunc func(t *testing.T) watcherMutateFS) {
	t.Run("WatchOverwrite", func(t *testing.T) {
		t.Parallel()
		fsys := newWatchFS(t, createFSFunc)
		WriteFile(t, fsys, watchFile, "first")
		// The watch starts after the file exists, so any Create for it can only
		// come from the rewrite.
		probe := newWatchProbe(t.Context(), t, fsys, watchDir)

		WriteFile(t, fsys, watchFile, "second")
		probe.waitFor(types.NotifyWrite, watchFile)
		probe.settle()
		probe.assertQuietOp(types.NotifyCreate, watchFile, "an existing file was written again")
		probe.assertPaths()
	})
}

// watcherTwoWatchers checks that two watchers over one directory each get their
// own copy of a notification, so a second Watch cannot take over or steal the
// delivery of the first.
func watcherTwoWatchers(t *testing.T, createFSFunc func(t *testing.T) watcherMutateFS) {
	t.Run("WatchTwoWatchers", func(t *testing.T) {
		t.Parallel()
		fsys := newWatchFS(t, createFSFunc)
		first := newWatchProbe(t.Context(), t, fsys, watchDir)
		second := newWatchProbe(t.Context(), t, fsys, watchDir)

		WriteFile(t, fsys, watchFile, "both")
		first.waitFor(types.NotifyCreate, watchFile)
		second.waitFor(types.NotifyCreate, watchFile)

		first.assertPaths()
		second.assertPaths()
	})
}

// watcherStopDelivery checks the two ways watching is supposed to end: closing
// the returned io.Closer, which has to be idempotent, and canceling the context
// the watch was started with.
func watcherStopDelivery(t *testing.T, createFSFunc func(t *testing.T) watcherMutateFS) {
	t.Run("WatchStopDelivery", func(t *testing.T) {
		t.Parallel()

		t.Run("Closed", func(t *testing.T) {
			t.Parallel()
			fsys := newWatchFS(t, createFSFunc)
			probe := newWatchProbe(t.Context(), t, fsys, watchDir)
			probe.close()

			WriteFile(t, fsys, watchFile, "after close")
			if err := fsys.Remove(watchFile); err != nil {
				t.Errorf("Remove(%q) with a closed watcher = %v, want nil: closing a watcher does not stop the file system working", watchFile, err)
			}
			probe.settle()
			probe.assertQuietOp(types.NotifyCreate, watchFile, "the watcher was closed")
			probe.assertQuietOp(types.NotifyWrite, watchFile, "the watcher was closed")
			probe.assertQuietOp(types.NotifyRemove, watchFile, "the watcher was closed")
		})

		t.Run("CanceledContext", func(t *testing.T) {
			t.Parallel()
			fsys := newWatchFS(t, createFSFunc)
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			probe := newWatchProbe(ctx, t, fsys, watchDir)
			cancel()

			WriteFile(t, fsys, watchFile, "after cancel")
			probe.settle()
			probe.assertQuietOp(types.NotifyCreate, watchFile, "the context was canceled")
			probe.assertQuietOp(types.NotifyWrite, watchFile, "the context was canceled")
		})
	})
}

// watcherRename checks that a rename inside the watched directory reports the
// name the entry has now: a watcher that is only told the name it had before is
// left with a stale tree.
//
// Which operation a rename is reported as, and whether the old name is reported
// too, is left to each driver's own tests: *core.memFS and *boltfs.boltFS
// report the old name as Rename and the new one as Create, while
// *core.localFS reports one event whose path is the new name.
func watcherRename(t *testing.T, createFSFunc func(t *testing.T) watcherRenameFS) {
	t.Run("WatchRename", func(t *testing.T) {
		t.Parallel()
		fsys := newWatchFS(t, createFSFunc)
		WriteFile(t, fsys, watchFile, "moving")
		probe := newWatchProbe(t.Context(), t, fsys, watchDir)

		if err := fsys.Rename(watchFile, watchRenamedFile); err != nil {
			t.Fatalf("Rename(%q, %q) = %v, want nil", watchFile, watchRenamedFile, err)
		}
		probe.waitPath(watchRenamedFile)
		probe.assertPaths()
		t.Logf("Rename(%q, %q) delivered: %v", watchFile, watchRenamedFile, probe.describe())
	})
}

// watcherRemoveAll checks that removing a subtree reports the file that went
// with it, by the path it had.
//
// Whether the directories are reported as well, and what other operations come
// with a subtree remove, is left to each driver's own tests: *core.memFS and
// *boltfs.boltFS report every path below the name, while *core.localFS leaves
// it to the operating system, which reports Chmod changes too.
func watcherRemoveAll(t *testing.T, createFSFunc func(t *testing.T) watcherRemoveAllFS) {
	t.Run("WatchRemoveAll", func(t *testing.T) {
		t.Parallel()
		fsys := newWatchFS(t, createFSFunc)
		mkdirForTest(t, fsys, watchNestedDir)
		WriteFile(t, fsys, watchNestedFile, "going away")
		WriteFile(t, fsys, watchFile, "staying")
		probe := newWatchProbe(t.Context(), t, fsys, watchDir)

		if err := fsys.RemoveAll(watchNestedDir); err != nil {
			t.Fatalf("RemoveAll(%q) = %v, want nil", watchNestedDir, err)
		}
		probe.waitFor(types.NotifyRemove, watchNestedFile)
		probe.assertPaths()
		t.Logf("RemoveAll(%q) delivered: %v", watchNestedDir, probe.describe())
	})
}

// watchProbe is a watcher started by the suite, together with the notifications
// it has been handed.
type watchProbe struct {
	*eventtest.EventCollector[types.NotifyOp]
	t        *testing.T
	root     string
	closer   io.Closer
	inHook   atomic.Int32
	overlaps atomic.Int32
}

// newWatchProbe starts a watcher over root on fsys using ctx, records what it
// delivers, and registers cleanup that closes it.
func newWatchProbe(ctx context.Context, t *testing.T, fsys types.Watcher, root string) *watchProbe {
	t.Helper()
	probe := &watchProbe{t: t, root: root}
	probe.EventCollector = eventtest.NewEventCollector[types.NotifyOp]()

	closer, err := fsys.Watch(ctx, root, probe.hook)
	if err != nil {
		// A file system that refuses to watch a valid directory that exists
		// cannot have its delivery checked at all, so the case is skipped
		// rather than failed. *gcsfs.gcsFS is one: it implements
		// [types.Watcher] but takes its notifications from a
		// publish/subscribe feed, and the bucket its conformance test builds
		// holds no subscription to read one from.
		t.Skipf("Watch(%q) = %v, want nil: the file system will not watch a directory it holds, so delivery cannot be checked", root, err)
	}
	probe.closer = closer
	// Registered before the close so that it runs after it: a file system
	// that calls the hook from more than one goroutine while shutting down is
	// caught as well as one that does it while delivering.
	t.Cleanup(probe.assertSerial)
	t.Cleanup(ufsTesting.ValidateClose(t, closer))
	return probe
}

// hook holds each notification briefly so that [watchProbe.assertSerial] can
// actually see a file system that delivers concurrently: two overlapping calls
// lasting only a few instructions each are not observable, so the handler has
// to occupy some time for the count of concurrent handlers to exceed one.
func (p *watchProbe) hook(op types.NotifyOp, path string) {
	if p.inHook.Add(1) > 1 {
		p.overlaps.Add(1)
	}
	p.Hook(op, path)
	time.Sleep(watchHookHold)
	p.inHook.Add(-1)
}

// waitFor fails the test unless op for name is delivered within
// [eventtest.EventDeadline].
func (p *watchProbe) waitFor(op types.NotifyOp, name string) {
	p.t.Helper()
	p.WaitFor(p.t, eventtest.EventDeadline, func(event eventtest.Event[types.NotifyOp]) bool {
		return event.Op == op && event.Path == name
	})
}

// waitPath fails the test unless a notification for name arrives within
// [eventtest.EventDeadline], whichever operation it is reported as.
func (p *watchProbe) waitPath(name string) {
	p.t.Helper()
	p.WaitFor(p.t, eventtest.EventDeadline, func(event eventtest.Event[types.NotifyOp]) bool {
		return event.Path == name
	})
}

// hasEvent reports whether op for name has been delivered.
func (p *watchProbe) hasEvent(op types.NotifyOp, name string) bool {
	return p.HasEvent(func(event eventtest.Event[types.NotifyOp]) bool {
		return event.Op == op && event.Path == name
	})
}

// settle gives a notification that should never have been delivered the chance
// to turn up anyway. The wait is bounded because there is no later notification
// to order against: a watcher that has stopped is not going to deliver one.
func (p *watchProbe) settle() {
	p.t.Helper()
	time.Sleep(watchSilenceWindow)
}

// assertQuietOp fails if op for name has been delivered, after which it was.
func (p *watchProbe) assertQuietOp(op types.NotifyOp, name, after string) {
	p.t.Helper()
	if p.hasEvent(op, name) {
		p.t.Errorf("Watch delivered %s for %q after %s, want nothing delivered: %v",
			watchOpName(op), name, after, p.describe())
	}
}

// assertQuietPath fails if any notification for name has been delivered, after
// which it was.
func (p *watchProbe) assertQuietPath(name, after string) {
	p.t.Helper()
	if p.HasEvent(func(event eventtest.Event[types.NotifyOp]) bool {
		return event.Path == name
	}) {
		p.t.Errorf("Watch delivered something for %q after %s, want nothing delivered about it: %v",
			name, after, p.describe())
	}
}

// assertPaths checks that every delivered path has the shape the contract
// promises: root-relative, forward-slash separated, accepted by [fs.ValidPath],
// and at or below the watched name. A path relative to the watched directory
// rather than to the file system root is the natural mistake to make here, and
// an operating system path is the one a local file system has to avoid.
func (p *watchProbe) assertPaths() {
	p.t.Helper()
	for _, event := range p.Events() {
		if !fs.ValidPath(event.Path) {
			p.t.Errorf("Watch delivered %q, want a path fs.ValidPath accepts: %v", event.Path, p.describe())
		}
		if strings.Contains(event.Path, `\`) {
			p.t.Errorf("Watch delivered %q, want a path using forward slashes: %v", event.Path, p.describe())
		}
		if p.root != "." && event.Path != p.root && !strings.HasPrefix(event.Path, p.root+"/") {
			p.t.Errorf("Watch delivered %q for a watch on %q, want the path of something at or below %q counted from the root: %v",
				event.Path, p.root, p.root, p.describe())
		}
	}
}

// assertSerial checks the contract's promise that hook is called serially from a
// single background goroutine. [newWatchProbe] runs it as cleanup for every
// watcher the suite starts, so no delivery case has to remember to ask.
func (p *watchProbe) assertSerial() {
	p.t.Helper()
	if n := p.overlaps.Load(); n != 0 {
		p.t.Errorf("Watch called the hook while an earlier call was still running, %d time(s), want the calls serial: %v",
			n, p.describe())
	}
}

// close closes the watcher and checks that closing it twice is not an error,
// which is what the contract means by closing being idempotent. The cleanup
// [newWatchProbe] registers closes it a third time.
func (p *watchProbe) close() {
	p.t.Helper()
	if err := p.closer.Close(); err != nil {
		p.t.Errorf("Close() of the Watch closer = %v, want nil", err)
	}
	if err := p.closer.Close(); err != nil {
		p.t.Errorf("Close() of the Watch closer a second time = %v, want nil: closing is idempotent", err)
	}
}

// describe renders the delivered notifications with the operations named, which
// is what a failure report needs to be readable.
func (p *watchProbe) describe() []string {
	events := p.Events()
	described := make([]string, len(events))
	for i, event := range events {
		described[i] = watchOpName(event.Op) + " " + event.Path
	}
	return described
}

// watchOpName names a notification for a failure message.
func watchOpName(op types.NotifyOp) string {
	if op >= 0 && op < types.NotifyOp(len(watchOpNames)) {
		return watchOpNames[op]
	}
	return "NotifyOp(" + strconv.Itoa(int(op)) + ")"
}

// watchNoHook is the hook for the cases that only check whether Watch is
// refused, where there is nothing to record.
func watchNoHook(types.NotifyOp, string) {}

// newWatchFS returns a file system from createFS holding the tree the watchers
// sit in: watchDir and watchOtherDir, and watchOutsideFile at the root.
func newWatchFS[T watcherSeedFS](t *testing.T, createFS func(*testing.T) T) T {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))

	for _, dir := range []string{watchDir, watchOtherDir} {
		mkdirForTest(t, fsys, dir)
	}
	WriteFile(t, fsys, watchOutsideFile, "at the root")
	return fsys
}

// skipIfNoWatch skips the calling test when fsys will not watch root at all,
// which is how a file system whose notifications come from somewhere other
// than its own mutations shows up before any assertion is made about names.
func skipIfNoWatch(t *testing.T, fsys types.Watcher, root string) {
	t.Helper()
	closer, err := fsys.Watch(t.Context(), root, watchNoHook)
	if err != nil {
		t.Skipf("Watch(%q) = %v, want nil: the file system refuses every watch, so what it reports about other names cannot be checked", root, err)
	}
	if closer == nil {
		return
	}
	if err := closer.Close(); err != nil {
		t.Errorf("Close after Watch(%q) = %v, want nil", root, err)
	}
}
