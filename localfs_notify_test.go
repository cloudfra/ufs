// Copyright 2026 Jeremy Edwards
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

package ufs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudfra/ufs/internal/osutil"
)

type notifyEvent struct {
	op   NotifyOp
	path string
}

type eventCollector struct {
	mu     sync.Mutex
	events []notifyEvent
	ch     chan struct{}
}

func newEventCollector() *eventCollector {
	return &eventCollector{ch: make(chan struct{}, 1024)}
}

func (c *eventCollector) hook(op NotifyOp, path string) {
	c.mu.Lock()
	c.events = append(c.events, notifyEvent{op: op, path: path})
	c.mu.Unlock()
	select {
	case c.ch <- struct{}{}:
	default:
	}
}

func (c *eventCollector) waitFor(t *testing.T, deadline time.Duration, match func(notifyEvent) bool) {
	t.Helper()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		c.mu.Lock()
		found := slices.ContainsFunc(c.events, match)
		c.mu.Unlock()
		if found {
			return
		}
		select {
		case <-timer.C:
			c.mu.Lock()
			events := c.events
			c.mu.Unlock()
			t.Fatalf("timed out waiting for matching event; collected: %v", events)
			return
		case <-c.ch:
		}
	}
}

func (c *eventCollector) hasEvent(match func(notifyEvent) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.ContainsFunc(c.events, match)
}

const eventDeadline = 5 * time.Second

func skipIfUnsupported(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "darwin", "windows", "freebsd", "openbsd", "netbsd", "dragonfly":
	default:
		t.Skipf("fsnotify not supported on %s", runtime.GOOS)
	}
}

func TestWatchCreateWriteRemove(t *testing.T) {
	skipIfUnsupported(t)
	dir := t.TempDir()
	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, closer)()

	if err := osutil.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi")); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "hello.txt"
	})

	if err := osutil.WriteFile(filepath.Join(dir, "hello.txt"), []byte("updated")); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyWrite && ev.path == "hello.txt"
	})

	if err := osutil.Remove(filepath.Join(dir, "hello.txt")); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyRemove && ev.path == "hello.txt"
	})
}

func TestWatchNestedPreExisting(t *testing.T) {
	skipIfUnsupported(t)
	dir := t.TempDir()

	if err := osutil.MkdirAll(filepath.Join(dir, "a", "b")); err != nil {
		t.Fatal(err)
	}

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, closer)()

	if err := osutil.WriteFile(filepath.Join(dir, "a", "b", "deep.txt"), []byte("data")); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "a/b/deep.txt"
	})
}

// TestWatchNewDirRecursion verifies that directories created after the watch
// starts are themselves watched. The hook fires only after the watcher has
// installed the watch on the reported directory, so each step below waits for
// the event that guarantees the next write cannot race the watch installation.
func TestWatchNewDirRecursion(t *testing.T) {
	skipIfUnsupported(t)

	// startWatch watches a fresh directory and returns it with its collector.
	startWatch := func(t *testing.T) (string, *eventCollector) {
		t.Helper()
		dir := t.TempDir()
		fsys, err := makeLocalFS(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(validateClose(t, fsys))

		ec := newEventCollector()
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		closer, err := fsys.Watch(ctx, ".", ec.hook)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(validateClose(t, closer))
		return dir, ec
	}

	// Create each level separately and wait for its event before descending.
	// Creating both levels at once (MkdirAll) lets the watcher walk "new"
	// before "new/sub" exists; a file written into "new/sub" as soon as "new"
	// is reported can then land before the watch on "new/sub" is installed and
	// is never reported.
	t.Run("created incrementally", func(t *testing.T) {
		dir, ec := startWatch(t)

		if err := osutil.Mkdir(filepath.Join(dir, "new")); err != nil {
			t.Fatal(err)
		}
		ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
			return ev.op == NotifyCreate && ev.path == "new"
		})

		if err := osutil.Mkdir(filepath.Join(dir, "new", "sub")); err != nil {
			t.Fatal(err)
		}
		ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
			return ev.op == NotifyCreate && ev.path == "new/sub"
		})

		if err := osutil.WriteFile(filepath.Join(dir, "new", "sub", "file.txt"), []byte("x")); err != nil {
			t.Fatal(err)
		}
		ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
			return ev.op == NotifyCreate && ev.path == "new/sub/file.txt"
		})
	})

	// A tree that already exists when it appears in the watched directory must
	// be walked so its nested directories are watched too. Building it outside
	// the watched directory and moving it in makes the walk deterministic: the
	// whole tree exists before the watcher sees the "new" event.
	t.Run("moved in with existing children", func(t *testing.T) {
		dir, ec := startWatch(t)

		staging := t.TempDir()
		if err := osutil.MkdirAll(filepath.Join(staging, "new", "sub")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(staging, "new"), filepath.Join(dir, "new")); err != nil {
			t.Fatal(err)
		}
		ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
			return ev.op == NotifyCreate && ev.path == "new"
		})

		if err := osutil.WriteFile(filepath.Join(dir, "new", "sub", "file.txt"), []byte("x")); err != nil {
			t.Fatal(err)
		}
		ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
			return ev.op == NotifyCreate && ev.path == "new/sub/file.txt"
		})
	})
}

func TestWatchCloseStopsDelivery(t *testing.T) {
	skipIfUnsupported(t)
	dir := t.TempDir()

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}

	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	// Idempotent close.
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}

	if err := osutil.WriteFile(filepath.Join(dir, "after.txt"), []byte("x")); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	if ec.hasEvent(func(ev notifyEvent) bool {
		return ev.path == "after.txt"
	}) {
		t.Error("received event after Close()")
	}
}

func TestWatchCtxCancellation(t *testing.T) {
	skipIfUnsupported(t)
	dir := t.TempDir()

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, closer)()

	cancel()

	// Give the goroutine time to observe cancellation.
	time.Sleep(200 * time.Millisecond)

	if err := osutil.WriteFile(filepath.Join(dir, "post_cancel.txt"), []byte("x")); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	if ec.hasEvent(func(ev notifyEvent) bool {
		return ev.path == "post_cancel.txt"
	}) {
		t.Error("received event after context cancellation")
	}
}

func TestWatchSubdirectory(t *testing.T) {
	skipIfUnsupported(t)
	dir := t.TempDir()

	if err := osutil.MkdirAll(filepath.Join(dir, "watched")); err != nil {
		t.Fatal(err)
	}

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, "watched", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, closer)()

	if err := osutil.WriteFile(filepath.Join(dir, "watched", "inside.txt"), []byte("y")); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "watched/inside.txt"
	})
}

func TestWatchInvalidPath(t *testing.T) {
	skipIfUnsupported(t)
	dir := t.TempDir()

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	ctx := t.Context()

	if _, err := fsys.Watch(ctx, "../escape", func(NotifyOp, string) {}); err == nil {
		t.Error("Watch with invalid path should fail")
	}

	if _, err := fsys.Watch(ctx, "nonexistent", func(NotifyOp, string) {}); err == nil {
		t.Error("Watch on nonexistent directory should fail")
	}
}

func TestWatchRaceConcurrentClose(t *testing.T) {
	skipIfUnsupported(t)
	dir := t.TempDir()

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	closer, err := fsys.Watch(t.Context(), ".", func(NotifyOp, string) {})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			_ = closer.Close()
		})
	}
	wg.Wait()
}

func TestWatchRaceCloseWhileEventsInFlight(t *testing.T) {
	skipIfUnsupported(t)
	dir := t.TempDir()

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	var hookCalls atomic.Int64
	hook := func(NotifyOp, string) {
		hookCalls.Add(1)
	}

	closer, err := fsys.Watch(t.Context(), ".", hook)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 50 {
			_ = osutil.WriteFile(filepath.Join(dir, fmt.Sprintf("churn_%d.txt", i)), []byte("x"))
		}
	}()

	// Let some events start flowing, then close mid-stream.
	time.Sleep(5 * time.Millisecond)
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}

func TestWatchRaceConcurrentFileCreation(t *testing.T) {
	skipIfUnsupported(t)
	dir := t.TempDir()

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, closer)()

	const writers = 5
	const filesPerWriter = 10

	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			for f := range filesPerWriter {
				name := fmt.Sprintf("w%d_f%d.txt", w, f)
				if err := osutil.WriteFile(filepath.Join(dir, name), []byte("data")); err != nil {
					t.Errorf("osutil.WriteFile() returned an error, %s", err)
				}
			}
		})
	}
	wg.Wait()

	// Wait for at least one create event per writer to confirm delivery under
	// concurrent writes. We don't assert an exact count because the OS may
	// coalesce events.
	for w := range writers {
		prefix := fmt.Sprintf("w%d_", w)
		ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
			return ev.op == NotifyCreate && len(ev.path) >= len(prefix) && ev.path[:len(prefix)] == prefix
		})
	}
}

func TestWatchRaceRapidCreateDelete(t *testing.T) {
	skipIfUnsupported(t)
	dir := t.TempDir()

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, closer)()

	for i := range 30 {
		p := filepath.Join(dir, fmt.Sprintf("ephemeral_%d.txt", i))
		if err := osutil.WriteFile(p, []byte("x")); err != nil {
			t.Error(err)
		}
		if err := osutil.Remove(filepath.Clean(p)); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	}

	// Verify the watcher is still alive and functional after the churn.
	if err := osutil.WriteFile(filepath.Clean(filepath.Join(dir, "survivor.txt")), []byte("ok")); err != nil {
		t.Fatal(err)
	}
	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.path == "survivor.txt"
	})
}

func TestWatchRaceRapidDirNesting(t *testing.T) {
	skipIfUnsupported(t)
	dir := t.TempDir()

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, closer)()

	// Rapidly create nested directory trees to race addRecursive with new
	// events arriving for the child directories.
	for i := range 10 {
		nested := filepath.Join(dir, fmt.Sprintf("d%d", i), "a", "b")
		if err := osutil.MkdirAll(filepath.Clean(nested)); err != nil {
			t.Fatal(err)
		}
		if err := osutil.WriteFile(filepath.Clean(filepath.Join(nested, "leaf.txt")), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	// The watcher must survive the rapid nesting. Verify by writing a file
	// into one of the deep directories after a short settle and confirming
	// the watcher still delivers events.
	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate
	})

	if err := osutil.WriteFile(filepath.Clean(filepath.Join(dir, "still_alive.txt")), []byte("ok")); err != nil {
		t.Fatal(err)
	}
	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.path == "still_alive.txt"
	})
}

func TestWatchRaceCloseAndCancel(t *testing.T) {
	skipIfUnsupported(t)
	dir := t.TempDir()

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	ctx, cancel := context.WithCancel(t.Context())
	closer, err := fsys.Watch(ctx, ".", func(NotifyOp, string) {})
	if err != nil {
		t.Fatal(err)
	}

	// Fire cancel and Close simultaneously from separate goroutines.
	var wg sync.WaitGroup
	closeErr := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		cancel()
	}()
	go func() {
		defer wg.Done()
		closeErr <- closer.Close()
	}()
	wg.Wait()
	if err := <-closeErr; err != nil {
		t.Fatalf("Close() returned an error, %s", err)
	}
}

func TestWatchRaceDirRemoveDuringWatch(t *testing.T) {
	skipIfUnsupported(t)
	dir := t.TempDir()

	// Pre-create several directories.
	for i := range 5 {
		if err := osutil.MkdirAll(filepath.Clean(filepath.Join(dir, fmt.Sprintf("rmdir%d", i)))); err != nil {
			t.Fatal(err)
		}
	}

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, fsys)()

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, closer)()

	// Remove all watched directories at once.
	for i := range 5 {
		if err := osutil.RemoveAll(filepath.Clean(filepath.Join(dir, fmt.Sprintf("rmdir%d", i)))); err != nil {
			t.Errorf("osutil.RemoveAll() returned an error, %s", err)
		}
	}

	// Watcher must still be alive — confirm by creating a new file.
	if err := osutil.WriteFile(filepath.Clean(filepath.Join(dir, "after_rmdir.txt")), []byte("ok")); err != nil {
		t.Fatal(err)
	}
	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.path == "after_rmdir.txt"
	})
}
