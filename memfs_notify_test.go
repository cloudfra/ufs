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
	"io/fs"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemWatchCreateWriteRemove(t *testing.T) {
	fsys := makeMemFS("memory:")

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	f, err := fsys.Create("hello.txt")
	if err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "hello.txt"
	})

	if _, err := f.WriteString("updated"); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyWrite && ev.path == "hello.txt"
	})

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if err := fsys.Remove("hello.txt"); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyRemove && ev.path == "hello.txt"
	})
}

func TestMemWatchNestedDir(t *testing.T) {
	fsys := makeMemFS("memory:")

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	if err := fsys.MkdirAll("a/b", fs.ModePerm); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "a"
	})
	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "a/b"
	})

	if _, err := fsys.Create("a/b/deep.txt"); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "a/b/deep.txt"
	})
}

func TestMemWatchSubdirectory(t *testing.T) {
	fsys := makeMemFS("memory:")
	if err := fsys.MkdirAll("watched", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	if err := fsys.MkdirAll("other", fs.ModePerm); err != nil {
		t.Fatal(err)
	}

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, "watched", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	if _, err := fsys.Create("watched/inside.txt"); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "watched/inside.txt"
	})

	// File outside watched directory should not be delivered.
	if _, err := fsys.Create("other/outside.txt"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	if ec.hasEvent(func(ev notifyEvent) bool {
		return ev.path == "other/outside.txt"
	}) {
		t.Error("received event for file outside watched directory")
	}
}

func TestMemWatchCloseStopsDelivery(t *testing.T) {
	fsys := makeMemFS("memory:")

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

	if _, err := fsys.Create("after.txt"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	if ec.hasEvent(func(ev notifyEvent) bool {
		return ev.path == "after.txt"
	}) {
		t.Error("received event after Close()")
	}
}

func TestMemWatchCtxCancellation(t *testing.T) {
	fsys := makeMemFS("memory:")

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	cancel()

	time.Sleep(200 * time.Millisecond)

	if _, err := fsys.Create("post_cancel.txt"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	if ec.hasEvent(func(ev notifyEvent) bool {
		return ev.path == "post_cancel.txt"
	}) {
		t.Error("received event after context cancellation")
	}
}

func TestMemWatchRemoveAll(t *testing.T) {
	fsys := makeMemFS("memory:")

	if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.Create("dir/a.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.Create("dir/b.txt"); err != nil {
		t.Fatal(err)
	}

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	if err := fsys.RemoveAll("dir"); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyRemove && ev.path == "dir"
	})
}

func TestMemWatchCreateOverwrite(t *testing.T) {
	fsys := makeMemFS("memory:")

	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatal(err)
	}

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	// Create again should fire a Write event (overwrite).
	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyWrite && ev.path == "file.txt"
	})
}

func TestMemWatchInvalidPath(t *testing.T) {
	fsys := makeMemFS("memory:")

	ctx := t.Context()

	if _, err := fsys.Watch(ctx, "../escape", func(NotifyOp, string) {}); err == nil {
		t.Error("Watch with invalid path should fail")
	}

	if _, err := fsys.Watch(ctx, "nonexistent", func(NotifyOp, string) {}); err == nil {
		t.Error("Watch on nonexistent directory should fail")
	}

	// Watching a file (not a dir) should fail.
	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.Watch(ctx, "file.txt", func(NotifyOp, string) {}); err == nil {
		t.Error("Watch on a file should fail")
	}
}

func TestMemWatchClosed(t *testing.T) {
	fsys := makeMemFS("memory:")
	fsys.Close()

	if _, err := fsys.Watch(t.Context(), ".", func(NotifyOp, string) {}); err == nil {
		t.Error("Watch on closed FS should fail")
	}
}

func TestMemWatchRaceConcurrentClose(t *testing.T) {
	fsys := makeMemFS("memory:")

	closer, err := fsys.Watch(t.Context(), ".", func(NotifyOp, string) {})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = closer.Close()
		}()
	}
	wg.Wait()
}

func TestMemWatchRaceCloseWhileEventsInFlight(t *testing.T) {
	fsys := makeMemFS("memory:")

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
			fsys.Create(fmt.Sprintf("churn_%d.txt", i))
		}
	}()

	time.Sleep(5 * time.Millisecond)
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}

func TestMemWatchRaceConcurrentFileCreation(t *testing.T) {
	fsys := makeMemFS("memory:")

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	const writers = 5
	const filesPerWriter = 10

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range filesPerWriter {
				name := fmt.Sprintf("w%d_f%d.txt", w, f)
				fsys.Create(name)
			}
		}()
	}
	wg.Wait()

	for w := range writers {
		prefix := fmt.Sprintf("w%d_", w)
		ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
			return ev.op == NotifyCreate && len(ev.path) >= len(prefix) && ev.path[:len(prefix)] == prefix
		})
	}
}

func TestMemWatchRaceCloseAndCancel(t *testing.T) {
	fsys := makeMemFS("memory:")

	ctx, cancel := context.WithCancel(t.Context())
	closer, err := fsys.Watch(ctx, ".", func(NotifyOp, string) {})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		cancel()
	}()
	go func() {
		defer wg.Done()
		_ = closer.Close()
	}()
	wg.Wait()
}

func TestMemWatchFSClose(t *testing.T) {
	fsys := makeMemFS("memory:")

	ec := newEventCollector()

	closer, err := fsys.Watch(t.Context(), ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	_ = closer

	// Closing the FS should cancel all watchers.
	fsys.Close()

	time.Sleep(100 * time.Millisecond)
}
