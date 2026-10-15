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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudfra/ufs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/testing/eventtest"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func TestBoltBatchCreateWatch(t *testing.T) {
	fsys := newTestBoltFS(t)
	ufsdriversTesting.WriteFile(t, fsys, "old.txt", "old")

	ec := eventtest.NewEventCollector[ufs.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	closer, err := fsys.(ufs.Watcher).Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, closer)()

	err = ufs.BatchCreate(fsys,
		ufs.BatchFile{Name: "a/b/new.txt", Data: []byte("new")},
		ufs.BatchFile{Name: "old.txt", Data: []byte("replaced")},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []eventtest.Event[ufs.NotifyOp]{
		{Op: ufs.NotifyCreate, Path: "a"},
		{Op: ufs.NotifyCreate, Path: "a/b"},
		{Op: ufs.NotifyCreate, Path: "a/b/new.txt"},
		{Op: ufs.NotifyWrite, Path: "old.txt"},
	} {
		ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[ufs.NotifyOp]) bool {
			return ev == want
		})
	}
}

func TestBoltBatchCreatePerm(t *testing.T) {
	fsys := newTestBoltFS(t)
	err := ufs.BatchCreate(fsys,
		ufs.BatchFile{Name: "default.txt"},
		ufs.BatchFile{Name: "private.txt", Perm: 0o600},
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
			files := []ufs.BatchFile{{Name: fmt.Sprintf("w/%d/a.txt", i), Data: []byte("a")}}
			if i%4 == 0 {
				files = append(files, ufs.BatchFile{Name: "dir", Data: []byte("conflict")})
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

func TestBoltBatchCreateClosed(t *testing.T) {
	fsys, err := makeBoltFS(testBoltFSURI(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}
	err = fsys.BatchCreate([]ufs.BatchFile{{Name: "a.txt"}})
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
	write := map[string]func(fsys ufs.WriteFS, name string) error{
		"Create": func(fsys ufs.WriteFS, name string) error {
			if err := fsys.MkdirAll(path.Dir(name), fs.ModePerm); err != nil {
				return err
			}
			f, err := fsys.Create(name)
			if err != nil {
				return err
			}
			if _, err := f.Write(data); err != nil {
				return err
			}
			return f.Close()
		},
		"BatchCreate": func(fsys ufs.WriteFS, name string) error {
			return ufs.BatchCreate(fsys, ufs.BatchFile{Name: name, Data: data})
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

	err = ufs.BatchCreate(fsys, ufs.BatchFile{Name: "a/b.txt", Data: []byte("b")})
	var pathErr *fs.PathError
	if !errors.Is(err, fs.ErrPermission) || !errors.As(err, &pathErr) || pathErr.Path != "a/b.txt" {
		t.Errorf("BatchCreate() on a read-only FS = %v, want an *fs.PathError for a/b.txt wrapping %v", err, fs.ErrPermission)
	}
	if _, err := fsys.Stat("a"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(a) = %v, want %v", err, fs.ErrNotExist)
	}
}
