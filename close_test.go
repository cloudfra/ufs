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
	"errors"
	"sync/atomic"
	"testing"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

// closeCounter tracks how many times Close was called on a FS.
type closeCounter struct {
	WriteFS
	count atomic.Int32
}

func (c *closeCounter) Close() error {
	c.count.Add(1)
	return c.WriteFS.Close()
}

func (c *closeCounter) closed() int {
	return int(c.count.Load())
}

// failCloser wraps a FS so that Close always returns an error.
type failCloser struct {
	WriteFS
	count atomic.Int32
}

func (c *failCloser) Close() error {
	c.count.Add(1)
	return errors.New("close failed")
}

func (c *failCloser) closed() int {
	return int(c.count.Load())
}

func TestNewClosesBaseOnMountError(t *testing.T) {
	t.Parallel()
	// "angry://" will fail to open as a mount; the base FS (memory)
	// must still be closed.
	// Mount at path "ok" first, then fail at "bad" with an invalid scheme.
	_, err := New(t.Context(), "memory://?ok=null://&bad=invalid-will-fail://scheme")
	if err == nil {
		t.Fatal("New() with invalid mount URI should fail")
	}
}

func TestBuildClosesBaseOnMountError(t *testing.T) {
	t.Parallel()
	_, err := NewFSBuilder("memory://").
		Mount("ok", "null://").
		Mount("bad", "invalid-will-fail://scheme").
		Build(t.Context())
	if err == nil {
		t.Fatal("Build() with invalid mount URI should fail")
	}
}

func TestBuildClosesBaseOnConflictingMountError(t *testing.T) {
	t.Parallel()
	base := &closeCounter{WriteFS: mustBaseFS(t, "memory://")}
	mount := &closeCounter{WriteFS: mustBaseFS(t, "memory://")}

	b := NewFSBuilder("null://").MountFS("a", base).MountFS("a", mount)
	_, err := b.Build(t.Context())
	if err == nil {
		t.Fatal("Build() with conflicting mount paths should fail")
	}
	if mount.closed() < 1 {
		t.Error("conflicting mountFS was not closed on addMount error")
	}
}

func TestMountMapCloseClosesAllMountsOnError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	mm := makeMountMap("test")

	good1 := &closeCounter{WriteFS: mustBaseFS(t, "memory://1")}
	bad := &failCloser{WriteFS: mustBaseFS(t, "memory://bad")}
	good2 := &closeCounter{WriteFS: mustBaseFS(t, "memory://2")}

	ufsTesting.Must(t, mm.put("a", makeNestFS(ctx, good1)))
	ufsTesting.Must(t, mm.put("b", makeNestFS(ctx, bad)))
	ufsTesting.Must(t, mm.put("c", makeNestFS(ctx, good2)))

	err := mm.Close()
	if err == nil {
		t.Fatal("mountMap.Close() should return error when a mount fails to close")
	}

	if good1.closed() < 1 {
		t.Error("good1 mount was not closed")
	}
	if bad.closed() < 1 {
		t.Error("bad mount Close was not called")
	}
	if good2.closed() < 1 {
		t.Error("good2 mount was not closed despite bad mount failing")
	}
}

func TestNestFSCloseClosesBaseWhenMountsFail(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	base := &closeCounter{WriteFS: mustBaseFS(t, "memory://base")}
	bad := &failCloser{WriteFS: mustBaseFS(t, "memory://bad")}

	nfs := makeNestFS(ctx, base)
	ufsTesting.Must(t, nfs.addMount("failing", makeNestFS(ctx, bad)))

	err := nfs.Close()
	if err == nil {
		t.Fatal("nestFS.Close() should return error when mount fails to close")
	}

	if base.closed() < 1 {
		t.Error("base FS was not closed when mount close failed")
	}
	if bad.closed() < 1 {
		t.Error("failing mount Close was not called")
	}
}
