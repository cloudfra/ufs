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

package core

import (
	"context"
	"fmt"
	"io/fs"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudfra/ufs/drivers/testing/eventtest"
	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func TestMemWatchCreateWriteRemove(t *testing.T) {
	fsys := makeMemFS("memory:")

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, closer)()

	f, err := fsys.Create("hello.txt")
	if err != nil {
		t.Fatal(err)
	}

	ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Op == types.NotifyCreate && ev.Path == "hello.txt"
	})

	if _, err := f.WriteString("updated"); err != nil {
		t.Fatal(err)
	}

	ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Op == types.NotifyWrite && ev.Path == "hello.txt"
	})

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if err := fsys.Remove("hello.txt"); err != nil {
		t.Fatal(err)
	}

	ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Op == types.NotifyRemove && ev.Path == "hello.txt"
	})
}

func TestMemWatchNestedDir(t *testing.T) {
	fsys := makeMemFS("memory:")

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, closer)()

	if err := fsys.MkdirAll("a/b", fs.ModePerm); err != nil {
		t.Fatal(err)
	}

	ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Op == types.NotifyCreate && ev.Path == "a"
	})
	ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Op == types.NotifyCreate && ev.Path == "a/b"
	})

	if _, err := fsys.Create("a/b/deep.txt"); err != nil {
		t.Fatal(err)
	}

	ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Op == types.NotifyCreate && ev.Path == "a/b/deep.txt"
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

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, "watched", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, closer)()

	if _, err := fsys.Create("watched/inside.txt"); err != nil {
		t.Fatal(err)
	}

	ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Op == types.NotifyCreate && ev.Path == "watched/inside.txt"
	})

	// File outside watched directory should not be delivered. Events arrive
	// in order, so once the sentinel inside arrives, an event for the outside
	// file would have arrived before it.
	if _, err := fsys.Create("other/outside.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.Create("watched/sentinel.txt"); err != nil {
		t.Fatal(err)
	}
	ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Path == "watched/sentinel.txt"
	})

	if ec.HasEvent(func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Path == "other/outside.txt"
	}) {
		t.Error("received event for file outside watched directory")
	}
}

func TestMemWatchCloseStopsDelivery(t *testing.T) {
	fsys := makeMemFS("memory:")

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}

	if err := closer.Close(); err != nil {
		t.Fatalf("failed to close watcher: %v", err)
	}
	// Idempotent close.
	if err := closer.Close(); err != nil {
		t.Fatalf("failed to close watcher: %v", err)
	}

	// Close waits for delivery to stop, so nothing can arrive after it.
	if _, err := fsys.Create("after.txt"); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	if ec.HasEvent(func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Path == "after.txt"
	}) {
		t.Error("received event after Close()")
	}
}

func TestMemWatchCtxCancellation(t *testing.T) {
	fsys := makeMemFS("memory:")

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())

	closer, err := fsys.Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}

	cancel()
	if _, err := fsys.Create("post_cancel.txt"); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	// Close waits for the delivery goroutine to exit, so any delivery of the
	// event (a bug) has happened by the time it returns.
	ufsTesting.ValidateClose(t, closer)()

	if ec.HasEvent(func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Path == "post_cancel.txt"
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

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, closer)()

	if err := fsys.RemoveAll("dir"); err != nil {
		t.Fatal(err)
	}

	ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Op == types.NotifyRemove && ev.Path == "dir"
	})
}

func TestMemWatchCreateOverwrite(t *testing.T) {
	fsys := makeMemFS("memory:")

	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := closer.Close(); err != nil {
			t.Errorf("failed to close watcher: %v", err)
		}
	}()

	// Create again should fire a Write event (overwrite).
	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatal(err)
	}

	ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Op == types.NotifyWrite && ev.Path == "file.txt"
	})
}

func TestMemWatchInvalidPath(t *testing.T) {
	fsys := makeMemFS("memory:")

	ctx := t.Context()

	if _, err := fsys.Watch(ctx, "../escape", func(types.NotifyOp, string) {}); err == nil {
		t.Error("Watch with invalid path should fail")
	}

	if _, err := fsys.Watch(ctx, "nonexistent", func(types.NotifyOp, string) {}); err == nil {
		t.Error("Watch on nonexistent directory should fail")
	}

	// Watching a file (not a dir) should fail.
	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.Watch(ctx, "file.txt", func(types.NotifyOp, string) {}); err == nil {
		t.Error("Watch on a file should fail")
	}
}

func TestMemWatchClosed(t *testing.T) {
	fsys := makeMemFS("memory:")
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := fsys.Watch(t.Context(), ".", func(types.NotifyOp, string) {}); err == nil {
		t.Error("Watch on closed FS should fail")
	}
}

func TestMemWatchRaceConcurrentClose(t *testing.T) {
	fsys := makeMemFS("memory:")

	closer, err := fsys.Watch(t.Context(), ".", func(types.NotifyOp, string) {})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if err := closer.Close(); err != nil {
				t.Errorf("failed to close watcher: %v", err)
			}
		})
	}
	wg.Wait()
}

func TestMemWatchRaceCloseWhileEventsInFlight(t *testing.T) {
	fsys := makeMemFS("memory:")

	var hookCalls atomic.Int64
	firstEvent := make(chan struct{})
	var firstOnce sync.Once
	hook := func(types.NotifyOp, string) {
		hookCalls.Add(1)
		firstOnce.Do(func() { close(firstEvent) })
	}

	closer, err := fsys.Watch(t.Context(), ".", hook)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 50 {
			if _, err := fsys.Create(fmt.Sprintf("churn_%d.txt", i)); err != nil {
				t.Fatalf("failed to create file: %v", err)
			}
		}
	})

	// Close once events are flowing.
	select {
	case <-firstEvent:
	case <-time.After(eventtest.EventDeadline):
		t.Fatal("no event delivered before Close")
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}

func TestMemWatchRaceConcurrentFileCreation(t *testing.T) {
	fsys := makeMemFS("memory:")

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := closer.Close(); err != nil {
			t.Errorf("failed to close watcher: %v", err)
		}
	}()

	const writers = 5
	const filesPerWriter = 10

	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			for f := range filesPerWriter {
				name := fmt.Sprintf("w%d_f%d.txt", w, f)
				if _, err := fsys.Create(name); err != nil {
					t.Fatalf("failed to create file: %v", err)
				}
			}
		})
	}
	wg.Wait()

	for w := range writers {
		prefix := fmt.Sprintf("w%d_", w)
		ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
			return ev.Op == types.NotifyCreate && len(ev.Path) >= len(prefix) && ev.Path[:len(prefix)] == prefix
		})
	}
}

func TestMemWatchRaceCloseAndCancel(t *testing.T) {
	fsys := makeMemFS("memory:")

	ctx, cancel := context.WithCancel(t.Context())
	closer, err := fsys.Watch(ctx, ".", func(types.NotifyOp, string) {})
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
		ufsTesting.ValidateClose(t, closer)
	}()
	wg.Wait()
}

func TestMemWatchFSClose(t *testing.T) {
	fsys := makeMemFS("memory:")

	ec := eventtest.NewEventCollector[types.NotifyOp]()

	closer, err := fsys.Watch(t.Context(), ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}

	// Closing the FS cancels every watcher: the watcher's own Close, which
	// waits for its delivery goroutine to exit, must then return.
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}
	ufsTesting.ValidateClose(t, closer)()
}

func TestMemWatchRename(t *testing.T) {
	fsys := makeMemFS("memory:")
	if err := fsys.MkdirAll("a", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Create("a/f.txt")
	if err != nil {
		t.Fatal(err)
	}
	ufsTesting.Must(t, f.Close())

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, closer)()

	if err := fsys.Rename("a", "b"); err != nil {
		t.Fatal(err)
	}

	for _, want := range []eventtest.Event[types.NotifyOp]{
		{Op: types.NotifyRename, Path: "a"},
		{Op: types.NotifyRename, Path: "a/f.txt"},
		{Op: types.NotifyCreate, Path: "b"},
		{Op: types.NotifyCreate, Path: "b/f.txt"},
	} {
		ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
			return ev == want
		})
	}
}
