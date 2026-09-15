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

package ufs

import (
	"context"
	"io/fs"
	"testing"
	"time"
)

// TestBoltWatchCreateWriteClose verifies that, unlike memFS, a NotifyWrite
// event is only delivered once the writer is Closed: WriteString alone only
// buffers content in memory and must not touch the bolt database or fire a
// notification.
func TestBoltWatchCreateWriteClose(t *testing.T) {
	fsys := newTestBoltFS(t)

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.(Watcher).Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, closer)()

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

	// WriteString alone must not persist to the database or notify.
	time.Sleep(100 * time.Millisecond)
	if ec.hasEvent(func(ev notifyEvent) bool {
		return ev.op == NotifyWrite && ev.path == "hello.txt"
	}) {
		t.Error("received NotifyWrite before Close(), want it deferred until Close")
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyWrite && ev.path == "hello.txt"
	})

	if err := fsys.Remove("hello.txt"); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyRemove && ev.path == "hello.txt"
	})
}

func TestBoltWatchNestedDir(t *testing.T) {
	fsys := newTestBoltFS(t)

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.(Watcher).Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, closer)()

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

func TestBoltWatchSubdirectory(t *testing.T) {
	fsys := newTestBoltFS(t)
	if err := fsys.MkdirAll("watched", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	if err := fsys.MkdirAll("other", fs.ModePerm); err != nil {
		t.Fatal(err)
	}

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.(Watcher).Watch(ctx, "watched", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, closer)()

	if _, err := fsys.Create("watched/inside.txt"); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "watched/inside.txt"
	})

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

func TestBoltWatchCloseStopsDelivery(t *testing.T) {
	fsys := newTestBoltFS(t)

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.(Watcher).Watch(ctx, ".", ec.hook)
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

	if ec.hasEvent(func(ev notifyEvent) bool {
		return ev.path == "after.txt"
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

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.(Watcher).Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer validateClose(t, closer)()

	if err := fsys.RemoveAll("dir"); err != nil {
		t.Fatal(err)
	}

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyRemove && ev.path == "dir"
	})
}

func TestBoltWatchCreateOverwrite(t *testing.T) {
	fsys := newTestBoltFS(t)

	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.(Watcher).Watch(ctx, ".", ec.hook)
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

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyWrite && ev.path == "file.txt"
	})
}

func TestBoltWatchInvalidPath(t *testing.T) {
	fsys := newTestBoltFS(t)

	ctx := t.Context()

	if _, err := fsys.(Watcher).Watch(ctx, "../escape", func(NotifyOp, string) {}); err == nil {
		t.Error("Watch with invalid path should fail")
	}

	if _, err := fsys.(Watcher).Watch(ctx, "nonexistent", func(NotifyOp, string) {}); err == nil {
		t.Error("Watch on nonexistent directory should fail")
	}

	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.(Watcher).Watch(ctx, "file.txt", func(NotifyOp, string) {}); err == nil {
		t.Error("Watch on a file should fail")
	}
}

func TestBoltWatchClosed(t *testing.T) {
	fsys := newTestBoltFS(t)
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := fsys.(Watcher).Watch(t.Context(), ".", func(NotifyOp, string) {}); err == nil {
		t.Error("Watch on closed FS should fail")
	}
}

func TestBoltWatchFSClose(t *testing.T) {
	fsys := newTestBoltFS(t)

	ec := newEventCollector()

	closer, err := fsys.(Watcher).Watch(t.Context(), ".", ec.hook)
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
