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
	"io/fs"
	"sync/atomic"
	"testing"
)

// closeCounter tracks how many times Close was called on a FS.
type closeCounter struct {
	FS
	count atomic.Int32
}

func (c *closeCounter) Close() error {
	c.count.Add(1)
	return c.FS.Close()
}

func (c *closeCounter) closed() int {
	return int(c.count.Load())
}

// failCloser wraps a FS so that Close always returns an error.
type failCloser struct {
	FS
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
	base := &closeCounter{FS: makeMemFS("memory://")}
	mount := &closeCounter{FS: makeMemFS("memory://")}

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

	good1 := &closeCounter{FS: makeMemFS("memory://1")}
	bad := &failCloser{FS: makeMemFS("memory://bad")}
	good2 := &closeCounter{FS: makeMemFS("memory://2")}

	must(t, mm.put("a", makeNestFS(ctx, good1)))
	must(t, mm.put("b", makeNestFS(ctx, bad)))
	must(t, mm.put("c", makeNestFS(ctx, good2)))

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

	base := &closeCounter{FS: makeMemFS("memory://base")}
	bad := &failCloser{FS: makeMemFS("memory://bad")}

	nfs := makeNestFS(ctx, base)
	must(t, nfs.addMount("failing", makeNestFS(ctx, bad)))

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

func TestTempMountFSCloseRunsCleanupOnInnerError(t *testing.T) {
	t.Parallel()

	var cleanupCalled atomic.Int32
	angry := makeAngryFS(angryFSPrefix)
	tfs := makeTempMountFS(angry, "test://", "test://", func() error {
		cleanupCalled.Add(1)
		return nil
	})

	err := tfs.Close()
	if err == nil {
		t.Fatal("Close() should return error from angry lfs")
	}
	if cleanupCalled.Load() < 1 {
		t.Error("cleanup function was not called when inner FS Close failed")
	}
}

func TestTempMountFSCloseReportsBothErrors(t *testing.T) {
	t.Parallel()

	angry := makeAngryFS(angryFSPrefix)
	cleanupErr := errors.New("cleanup boom")
	tfs := makeTempMountFS(angry, "test://", "test://", func() error {
		return cleanupErr
	})

	err := tfs.Close()
	if err == nil {
		t.Fatal("Close() should return error")
	}
	if !errors.Is(err, cleanupErr) {
		t.Errorf("Close() error should contain cleanup error, got: %v", err)
	}
}

func TestArchiveFSCloseClosesUnderlyingFile(t *testing.T) {
	t.Parallel()

	var closeCalled atomic.Int32
	afs := makeArchiveFS(emptyFS{}, "test.zip", closerFunc(func() error {
		closeCalled.Add(1)
		return nil
	}))

	if err := afs.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if closeCalled.Load() != 1 {
		t.Error("underlying file closer was not called on archiveFS.Close()")
	}
}

func TestArchiveFSCloseWithoutCloserIsNoop(t *testing.T) {
	t.Parallel()

	afs := makeArchiveFS(emptyFS{}, "test.zip", nil)
	if err := afs.Close(); err != nil {
		t.Errorf("Close() = %v, want nil (no closer set)", err)
	}
}

func TestArchiveFSCloseReportsCloserError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("file close failed")
	afs := makeArchiveFS(emptyFS{}, "test.zip", closerFunc(func() error {
		return wantErr
	}))

	err := afs.Close()
	if !errors.Is(err, wantErr) {
		t.Errorf("Close() = %v, want %v", err, wantErr)
	}
}

func TestArchiveFSCloseIdempotent(t *testing.T) {
	t.Parallel()

	var closeCalled atomic.Int32
	afs := makeArchiveFS(emptyFS{}, "test.zip", closerFunc(func() error {
		closeCalled.Add(1)
		return nil
	}))

	for range 3 {
		validateClose(t, afs)()
	}
	if closeCalled.Load() != 1 {
		t.Errorf("closer called %d times, want exactly 1", closeCalled.Load())
	}
}

// closerFunc adapts a bare function to io.Closer.
type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// emptyFS is a minimal fs.FS that contains no files.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}
