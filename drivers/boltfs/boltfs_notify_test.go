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

//go:build !wasm

package boltfs

import (
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/cloudfra/ufs/drivers/testing/eventtest"
	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestBoltWatchCreateWriteClose verifies that, unlike memFS, a types.NotifyWrite
// event is only delivered once the writer is Closed: WriteString alone only
// buffers content in memory and must not touch the bolt database or fire a
// notification.
func TestBoltWatchCreateWriteClose(t *testing.T) {
	fsys := newTestBoltFS(t)

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.(types.Watcher).Watch(ctx, ".", ec.Hook)
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

	// WriteString alone must not persist to the database or notify.
	time.Sleep(100 * time.Millisecond)
	if ec.HasEvent(func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Op == types.NotifyWrite && ev.Path == "hello.txt"
	}) {
		t.Error("received NotifyWrite before Close(), want it deferred until Close")
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Op == types.NotifyWrite && ev.Path == "hello.txt"
	})

	if err := fsys.Remove("hello.txt"); err != nil {
		t.Fatal(err)
	}

	ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Op == types.NotifyRemove && ev.Path == "hello.txt"
	})
}

func TestBoltWatchNestedDir(t *testing.T) {
	fsys := newTestBoltFS(t)

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.(types.Watcher).Watch(ctx, ".", ec.Hook)
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

func TestBoltWatchSubdirectory(t *testing.T) {
	fsys := newTestBoltFS(t)
	if err := fsys.MkdirAll("watched", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	if err := fsys.MkdirAll("other", fs.ModePerm); err != nil {
		t.Fatal(err)
	}

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.(types.Watcher).Watch(ctx, "watched", ec.Hook)
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

	if _, err := fsys.Create("other/outside.txt"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	if ec.HasEvent(func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Path == "other/outside.txt"
	}) {
		t.Error("received event for file outside watched directory")
	}
}

func TestBoltWatchCloseStopsDelivery(t *testing.T) {
	fsys := newTestBoltFS(t)

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.(types.Watcher).Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}

	if err := closer.Close(); err != nil {
		t.Fatalf("failed to close watcher: %v", err)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("failed to close watcher: %v", err)
	}

	if _, err := fsys.Create("after.txt"); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	if ec.HasEvent(func(ev eventtest.Event[types.NotifyOp]) bool {
		return ev.Path == "after.txt"
	}) {
		t.Error("received event after Close()")
	}
}

func TestBoltWatchRemoveAll(t *testing.T) {
	fsys := newTestBoltFS(t)

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

	closer, err := fsys.(types.Watcher).Watch(ctx, ".", ec.Hook)
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

func TestBoltWatchCreateOverwrite(t *testing.T) {
	fsys := newTestBoltFS(t)

	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.(types.Watcher).Watch(ctx, ".", ec.Hook)
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

func TestBoltWatchInvalidPath(t *testing.T) {
	fsys := newTestBoltFS(t)

	ctx := t.Context()

	if _, err := fsys.(types.Watcher).Watch(ctx, "../escape", func(types.NotifyOp, string) {}); err == nil {
		t.Error("Watch with invalid path should fail")
	}

	if _, err := fsys.(types.Watcher).Watch(ctx, "nonexistent", func(types.NotifyOp, string) {}); err == nil {
		t.Error("Watch on nonexistent directory should fail")
	}

	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.(types.Watcher).Watch(ctx, "file.txt", func(types.NotifyOp, string) {}); err == nil {
		t.Error("Watch on a file should fail")
	}
}

func TestBoltWatchClosed(t *testing.T) {
	fsys := newTestBoltFS(t)
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := fsys.(types.Watcher).Watch(t.Context(), ".", func(types.NotifyOp, string) {}); err == nil {
		t.Error("Watch on closed FS should fail")
	}
}

func TestBoltWatchFSClose(t *testing.T) {
	fsys := newTestBoltFS(t)

	ec := eventtest.NewEventCollector[types.NotifyOp]()

	closer, err := fsys.(types.Watcher).Watch(t.Context(), ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	_ = closer

	// Closing the FS should cancel all watchers.
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
}
