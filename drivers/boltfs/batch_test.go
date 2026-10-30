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
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/cloudfra/ufs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/testing/eventtest"
	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func TestBoltBatchCreateWatch(t *testing.T) {
	fsys := newTestBoltFS(t)
	ufsdriversTesting.WriteFile(t, fsys, "old.txt", "old")

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	closer, err := fsys.(types.Watcher).Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, closer)()

	err = ufs.BatchCreate(fsys,
		types.BatchFile{Name: "a/b/new.txt", Data: []byte("new")},
		types.BatchFile{Name: "old.txt", Data: []byte("replaced")},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []eventtest.Event[types.NotifyOp]{
		{Op: types.NotifyCreate, Path: "a"},
		{Op: types.NotifyCreate, Path: "a/b"},
		{Op: types.NotifyCreate, Path: "a/b/new.txt"},
		{Op: types.NotifyWrite, Path: "old.txt"},
	} {
		ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
			return ev == want
		})
	}
}

func TestBoltBatchCreatePerm(t *testing.T) {
	fsys := newTestBoltFS(t)
	err := ufs.BatchCreate(fsys,
		types.BatchFile{Name: "default.txt"},
		types.BatchFile{Name: "private.txt", Perm: 0o600},
	)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]fs.FileMode{"default.txt": fs.ModePerm, "private.txt": 0o600} {
		info, err := fsys.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode() != want {
			t.Errorf("Stat(%q).Mode() = %v, want %v", name, info.Mode(), want)
		}
	}
}

// TestBoltBatchCreateRollback checks that a batch refused halfway through
// writes none of its files and leaves none of the directories it made on the
// way, which is what the one transaction is for.
func TestBoltBatchCreateRollback(t *testing.T) {
	fsys := newTestBoltFS(t)
	if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	err := ufs.BatchCreate(fsys,
		types.BatchFile{Name: "new/a.txt", Data: []byte("a")},
		types.BatchFile{Name: "dir", Data: []byte("conflict")},
		types.BatchFile{Name: "new/b.txt", Data: []byte("b")},
	)
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || pathErr.Path != "dir" || pathErr.Op != batchCreateOp {
		t.Fatalf("BatchCreate() = %v, want an *fs.PathError for dir of %s", err, batchCreateOp)
	}
	for _, name := range []string{"new/a.txt", "new/b.txt"} {
		if _, err := fsys.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) = %v, want %v: a failed batch writes nothing", name, err, fs.ErrNotExist)
		}
	}
	if _, err := fsys.Stat("new"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(new) = %v, want %v: a failed batch leaves no directories behind", err, fs.ErrNotExist)
	}
}

// TestBoltBatchCreateRepeatedName pins how a name repeated within one call is
// handled: the last entry wins, as the shared conformance requires today. The
// check belongs to the driver rather than only to the suite, because a batch
// enters the commit queue before it is applied. When types.ValidateBatchFiles
// lands and makes names unique within a batch, BatchCreate must call it and
// this test must expect an *fs.PathError wrapping fs.ErrInvalid instead.
func TestBoltBatchCreateRepeatedName(t *testing.T) {
	fsys := newTestBoltFS(t)
	err := ufs.BatchCreate(fsys,
		types.BatchFile{Name: "bc/x.txt", Data: []byte("first")},
		types.BatchFile{Name: "bc/x.txt", Data: []byte("second")},
	)
	if err != nil {
		t.Fatalf("BatchCreate() = %v, want nil", err)
	}
	ufsTesting.AssertContains(t, fsys, "bc/x.txt", "second")
}

// TestBoltBatchCreateConcurrentFailure runs many writers at once, some of
// whose batches fail. Batches committed together must not fail each other:
// each good batch is written and each bad one writes nothing.
func TestBoltBatchCreateConcurrentFailure(t *testing.T) {
	fsys := newTestBoltFS(t)
	if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	const writers = 32
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			files := []types.BatchFile{{Name: fmt.Sprintf("w/%d/a.txt", i), Data: []byte("a")}}
			if i%4 == 0 {
				files = append(files, types.BatchFile{Name: "dir", Data: []byte("conflict")})
			}
			errs[i] = ufs.BatchCreate(fsys, files...)
		})
	}
	wg.Wait()
	for i, err := range errs {
		name := fmt.Sprintf("w/%d/a.txt", i)
		if i%4 == 0 {
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) || pathErr.Path != "dir" || pathErr.Op != batchCreateOp {
				t.Errorf("writer %d: BatchCreate() = %v, want an *fs.PathError for %s of dir", i, err, batchCreateOp)
			}
			if _, err := fsys.Stat(path.Dir(name)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("writer %d: Stat(%q) = %v, want %v", i, path.Dir(name), err, fs.ErrNotExist)
			}
			continue
		}
		if err != nil {
			t.Errorf("writer %d: BatchCreate() = %v, want nil", i, err)
		}
		ufsTesting.AssertContains(t, fsys, name, "a")
	}
}

// TestBoltBatchCreateCommitPanic checks that a panic inside the commit is
// reported to every request waiting on it instead of leaving its caller
// blocked on lead, and that the committer still runs later batches.
func TestBoltBatchCreateCommitPanic(t *testing.T) {
	var c batchCommitter
	panicky := func(func(*bolt.Tx) error) error { panic("commit panic") }

	errs := make(chan error, 2)
	for range 2 {
		req := &batchRequest{
			files: []types.BatchFile{{Name: "a.txt"}},
			lead:  make(chan bool, 1),
		}
		go func() { errs <- c.submit(panicky, req) }()
	}
	for range 2 {
		select {
		case err := <-errs:
			if err == nil || !strings.Contains(err.Error(), "commit panic") {
				t.Errorf("submit() = %v, want the commit panic", err)
			}
		case <-time.After(time.Minute):
			t.Fatal("submit() did not return after the commit panicked")
		}
	}

	next := &batchRequest{
		files: []types.BatchFile{{Name: "b.txt"}},
		lead:  make(chan bool, 1),
	}
	noop := func(func(*bolt.Tx) error) error { return nil }
	if err := c.submit(noop, next); err != nil {
		t.Errorf("submit() after the panic = %v, want nil", err)
	}
}

func TestBoltBatchCreateClosed(t *testing.T) {
	fsys, err := makeBoltFS(testBoltFSURI(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}
	err = fsys.BatchCreate([]types.BatchFile{{Name: "a.txt"}})
	var pathErr *fs.PathError
	if !errors.Is(err, fs.ErrClosed) || !errors.As(err, &pathErr) || pathErr.Path != "a.txt" {
		t.Errorf("BatchCreate() on a closed FS = %v, want an *fs.PathError for a.txt wrapping %v", err, fs.ErrClosed)
	}
}

// BenchmarkBoltWriteFile compares writing one small file per call with
// MkdirAll, Create and Close (three transactions) and with BatchCreate (one
// transaction, shared by concurrent writers).
func BenchmarkBoltWriteFile(b *testing.B) {
	data := make([]byte, 4096)
	write := map[string]func(fsys types.WriteFS, name string) error{
		"Create": func(fsys types.WriteFS, name string) error {
			if err := fsys.MkdirAll(path.Dir(name), fs.ModePerm); err != nil {
				return err
			}
			f, err := fsys.Create(name)
			if err != nil {
				return err
			}
			_, werr := f.Write(data)
			if cerr := f.Close(); cerr != nil && werr == nil {
				werr = cerr
			}
			return werr
		},
		"BatchCreate": func(fsys types.WriteFS, name string) error {
			return ufs.BatchCreate(fsys, types.BatchFile{Name: name, Data: data})
		},
	}
	for _, method := range []string{"Create", "BatchCreate"} {
		for _, writers := range []int{1, 16} {
			b.Run(fmt.Sprintf("%s/writers=%d", method, writers), func(b *testing.B) {
				fsys, err := makeBoltFS(boltFSPrefix + filepath.Join(b.TempDir(), "bench.db"))
				if err != nil {
					b.Fatal(err)
				}
				defer ufsTesting.ValidateClose(b, fsys)()
				var next atomic.Int64
				var wg sync.WaitGroup
				b.ResetTimer()
				for range writers {
					wg.Go(func() {
						for {
							n := next.Add(1) - 1
							if n >= int64(b.N) {
								return
							}
							if err := write[method](fsys, fmt.Sprintf("d%d/f%d.txt", n%64, n)); err != nil {
								b.Error(err)
								return
							}
						}
					})
				}
				wg.Wait()
			})
		}
	}
}

// BenchmarkBoltBatchCreateFailure measures the retry path of group commit: a
// shared transaction that fails is committed again one request at a time, so
// N concurrent failing writers cost one failed leader transaction and N
// single-request transactions on top of it.
func BenchmarkBoltBatchCreateFailure(b *testing.B) {
	for _, writers := range []int{1, 16} {
		b.Run(fmt.Sprintf("writers=%d", writers), func(b *testing.B) {
			fsys, err := makeBoltFS(boltFSPrefix + filepath.Join(b.TempDir(), "bench.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer ufsTesting.ValidateClose(b, fsys)()
			if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
				b.Fatal(err)
			}
			var next atomic.Int64
			var wg sync.WaitGroup
			b.ResetTimer()
			for range writers {
				wg.Go(func() {
					for {
						n := next.Add(1) - 1
						if n >= int64(b.N) {
							return
						}
						err := ufs.BatchCreate(fsys,
							types.BatchFile{Name: fmt.Sprintf("d%d/f%d.txt", n%64, n), Data: []byte("a")},
							types.BatchFile{Name: "dir", Data: []byte("conflict")},
						)
						if err == nil {
							b.Error("BatchCreate() = nil, want an error")
							return
						}
					}
				})
			}
			wg.Wait()
		})
	}
}

func TestBoltBatchCreateReadOnly(t *testing.T) {
	uri := testBoltFSURI(t)
	writer, err := makeBoltFS(uri)
	if err != nil {
		t.Fatal(err)
	}
	ufsTesting.ValidateClose(t, writer)()
	fsys, err := makeBoltFS(uri + "?" + readOnlyParam + "=true")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	err = ufs.BatchCreate(fsys, types.BatchFile{Name: "a/b.txt", Data: []byte("b")})
	var pathErr *fs.PathError
	if !errors.Is(err, fs.ErrPermission) || !errors.As(err, &pathErr) || pathErr.Path != "a/b.txt" {
		t.Errorf("BatchCreate() on a read-only FS = %v, want an *fs.PathError for a/b.txt wrapping %v", err, fs.ErrPermission)
	}
	if _, err := fsys.Stat("a"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(a) = %v, want %v", err, fs.ErrNotExist)
	}
}
