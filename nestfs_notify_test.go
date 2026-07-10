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
	"io/fs"
	"testing"
	"time"
)

func TestNestFSWatchDelegatesToMemFS(t *testing.T) {
	inner := makeMemFS("memory:")
	nfs := makeNestFS(t.Context(), inner)
	defer func() {
		if err := nfs.Close(); err != nil {
			t.Errorf("failed to close nestFS: %v", err)
		}
	}()

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := nfs.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	f, err := inner.Create("test.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "test.txt"
	})
}

func TestNestFSWatchSubdirectory(t *testing.T) {
	inner := makeMemFS("memory:")
	if err := inner.MkdirAll("sub", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	nfs := makeNestFS(t.Context(), inner)
	defer func() {
		if err := nfs.Close(); err != nil {
			t.Errorf("failed to close nestFS: %v", err)
		}
	}()

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := nfs.Watch(ctx, "sub", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	// File inside the watched subdirectory should be delivered.
	f, err := inner.Create("sub/inside.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "sub/inside.txt"
	})

	// File outside should not be delivered.
	f, err = inner.Create("outside.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	time.Sleep(100 * time.Millisecond)

	if ec.hasEvent(func(ev notifyEvent) bool {
		return ev.path == "outside.txt"
	}) {
		t.Error("received event for file outside watched subdirectory")
	}
}

func TestNestFSWatchUnsupportedBackend(t *testing.T) {
	inner, err := newNullFS("null://")
	if err != nil {
		t.Fatal(err)
	}
	nfs := makeNestFS(t.Context(), inner)
	defer nfs.Close()

	_, err = nfs.Watch(t.Context(), ".", func(NotifyOp, string) {})
	if err == nil {
		t.Error("Watch on non-watchable backend should fail")
	}
}
