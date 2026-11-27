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
	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestBoltBatchCreateWatch checks that a watcher sees the batch: the parent
// directories it creates first, then every file, with a write rather than a
// create for a file that was already there. The batch notifies only once its
// transaction has committed, so a watcher never sees a path whose content is
// not in the database yet.
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
	// Those four are all the batch reports. bbolt runs the transaction
	// function again when it refuses a shared attempt, so an attempt that
	// never committed must not add events of its own: every event a watcher
	// receives stands for content it can read back.
	if got := ec.Events(); len(got) != 4 {
		t.Errorf("watcher collected %d events (%v), want the 4 the batch makes", len(got), got)
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

// TestBoltBatchCreateOverwrite covers an entry whose name is already taken,
// which is where a batch has a choice to make: a file there is replaced and the
// rest of the batch commits, while a directory there — empty or holding files —
// refuses the whole call. boltFS stores a directory as a bucket and a file as a
// key in the parent bucket, so the entry fails the way Create fails, wrapping
// fs.ErrInvalid, and the transaction that refuses it writes nothing else of the
// batch and reports nothing to a watcher.
func TestBoltBatchCreateOverwrite(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		fsys := newTestBoltFS(t)
		ufsdriversTesting.WriteFile(t, fsys, "there.txt", "old")
		ec := newBatchWatchCollector(t, fsys)
		err := ufs.BatchCreate(fsys,
			types.BatchFile{Name: "there.txt", Data: []byte("new"), Perm: 0o600},
			types.BatchFile{Name: "also.txt", Data: []byte("also")},
		)
		if err != nil {
			t.Fatalf("BatchCreate() = %v, want nil: a batch replaces the file it finds", err)
		}
		ufsTesting.AssertContains(t, fsys, "there.txt", "new")
		ufsTesting.AssertContains(t, fsys, "also.txt", "also")
		// The whole record is written again, so the permissions the entry asks
		// for replace the ones the file had.
		info, err := fsys.Stat("there.txt")
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode() != 0o600 {
			t.Errorf("Stat(there.txt).Mode() = %v, want %v", info.Mode(), fs.FileMode(0o600))
		}
		// A watcher is told a write for the file it already knew, and a create
		// for the one the batch made.
		for _, want := range []eventtest.Event[types.NotifyOp]{
			{Op: types.NotifyWrite, Path: "there.txt"},
			{Op: types.NotifyCreate, Path: "also.txt"},
		} {
			ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
				return ev == want
			})
		}
		if got := ec.Events(); len(got) != 2 {
			t.Errorf("watcher collected %d events (%v), want the 2 the batch makes", len(got), got)
		}
	})
	// The two directory cases differ only in whether the directory holds
	// anything: a batch refuses either name, and what the filled one holds
	// survives the attempt.
	for _, dir := range []struct {
		name   string
		filled bool
	}{
		{name: "empty-dir"},
		{name: "non-empty-dir", filled: true},
	} {
		t.Run(dir.name, func(t *testing.T) {
			fsys := newTestBoltFS(t)
			if err := fsys.MkdirAll(dir.name, fs.ModePerm); err != nil {
				t.Fatal(err)
			}
			if dir.filled {
				ufsdriversTesting.WriteFile(t, fsys, dir.name+"/inside.txt", "inside")
			}
			ec := newBatchWatchCollector(t, fsys)

			err := ufs.BatchCreate(fsys,
				types.BatchFile{Name: "new.txt", Data: []byte("new")},
				types.BatchFile{Name: dir.name, Data: []byte("conflict")},
			)
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) || pathErr.Path != dir.name || pathErr.Op != batchCreateOp {
				t.Fatalf("BatchCreate(%q) = %v, want an *fs.PathError for %s of %s", dir.name, err, dir.name, batchCreateOp)
			}
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("BatchCreate(%q) = %v, want an error wrapping %v as Create reports it", dir.name, err, fs.ErrInvalid)
			}
			if _, err := fsys.Stat("new.txt"); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Stat(new.txt) = %v, want %v: a failed batch writes nothing", err, fs.ErrNotExist)
			}
			// The directory stays a directory holding what it held.
			if info, err := fsys.Stat(dir.name); err != nil || !info.IsDir() {
				t.Errorf("Stat(%q) = %v, %v, want a directory", dir.name, info, err)
			}
			if dir.filled {
				ufsTesting.AssertContains(t, fsys, dir.name+"/inside.txt", "inside")
			}

			// A watcher never hears of a batch that never committed: the
			// sentinel is the only event it gets, and the batch under test ran
			// before it.
			if err := ufs.BatchCreate(fsys, types.BatchFile{Name: "sentinel.txt", Data: []byte("s")}); err != nil {
				t.Fatal(err)
			}
			ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
				return ev == eventtest.Event[types.NotifyOp]{Op: types.NotifyCreate, Path: "sentinel.txt"}
			})
			if got := ec.Events(); len(got) != 1 {
				t.Errorf("watcher collected %v, want only the sentinel", got)
			}
		})
	}
}

// TestBoltBatchCreateDescendantName covers two entries of one batch where one
// name is a directory the other needs, in both orders: whichever entry comes
// first, the pair is refused and none of the batch survives, not even the
// directories the batch made on the way to the entry that failed. The two
// orders report the conflict as different sentinels — an entry that finds a
// file where it needs a directory fails the way MkdirAll does, wrapping
// fs.ErrExist, and an entry that finds a directory where it needs a file
// fails the way Create does, wrapping fs.ErrInvalid — so the assertions name
// the sentinels rather than the messages.
func TestBoltBatchCreateDescendantName(t *testing.T) {
	cases := []struct {
		name    string
		first   string
		second  string
		wantErr error
	}{
		{name: "ancestor-first", first: "a/b", second: "a/b/c", wantErr: fs.ErrExist},
		{name: "descendant-first", first: "a/b/c", second: "a/b", wantErr: fs.ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := newTestBoltFS(t)
			ec := newBatchWatchCollector(t, fsys)
			err := ufs.BatchCreate(fsys,
				types.BatchFile{Name: "new.txt", Data: []byte("new")},
				types.BatchFile{Name: tc.first, Data: []byte("first")},
				types.BatchFile{Name: tc.second, Data: []byte("second")},
			)
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) || pathErr.Path != tc.second || pathErr.Op != batchCreateOp {
				t.Fatalf("BatchCreate(%q, %q) = %v, want an *fs.PathError for %s of %s", tc.first, tc.second, err, tc.second, batchCreateOp)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("BatchCreate(%q, %q) = %v, want an error wrapping %v", tc.first, tc.second, err, tc.wantErr)
			}
			for _, name := range []string{"new.txt", "a", tc.first, tc.second} {
				if _, err := fsys.Stat(name); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("Stat(%q) = %v, want %v: a failed batch writes nothing", name, err, fs.ErrNotExist)
				}
			}
			if err := ufs.BatchCreate(fsys, types.BatchFile{Name: "sentinel.txt", Data: []byte("s")}); err != nil {
				t.Fatal(err)
			}
			ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
				return ev == eventtest.Event[types.NotifyOp]{Op: types.NotifyCreate, Path: "sentinel.txt"}
			})
			if got := ec.Events(); len(got) != 1 {
				t.Errorf("watcher collected %v, want only the sentinel", got)
			}
		})
	}
}

// newBatchWatchCollector watches the root of fsys for the calling test and
// returns the collector that records what its watchers are told.
func newBatchWatchCollector(t *testing.T, fsys types.WriteFS) *eventtest.EventCollector[types.NotifyOp] {
	t.Helper()
	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	closer, err := fsys.(types.Watcher).Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ufsTesting.ValidateClose(t, closer))
	return ec
}

// TestBoltBatchCreateRepeatedName pins that a name written twice in one call is
// refused before the transaction opens, so none of the call is written. The
// rule is the shared one in types.ValidateBatchFiles, which the conformance
// suite's DuplicateName case holds every BatchCreateFS to; the driver checks it
// itself because a batch is one transaction here, and a refused name must not
// cost the writer a partial commit.
func TestBoltBatchCreateRepeatedName(t *testing.T) {
	fsys := newTestBoltFS(t)
	err := ufs.BatchCreate(fsys,
		types.BatchFile{Name: "bc/x.txt", Data: []byte("first")},
		types.BatchFile{Name: "bc/x.txt", Data: []byte("second")},
	)
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || pathErr.Path != "bc/x.txt" || pathErr.Op != batchCreateOp {
		t.Fatalf("BatchCreate() = %v, want an *fs.PathError for bc/x.txt of %s", err, batchCreateOp)
	}
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("BatchCreate() = %v, want an error wrapping %v", err, fs.ErrInvalid)
	}
	for _, name := range []string{"bc", "bc/x.txt"} {
		if _, err := fsys.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) = %v, want %v: a refused batch writes nothing", name, err, fs.ErrNotExist)
		}
	}
}

// TestBoltBatchCreateConcurrentFailure runs many writers at once, some of
// whose batches fail. Batches committed together must not fail each other:
// bbolt takes a call whose shared transaction is refused out of that
// transaction and runs it again on its own, so each good batch is written and
// each bad one writes nothing.
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
				b.ReportAllocs()
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

// BenchmarkBoltBatchCreateFailure measures the retry path of bolt.DB.Batch: a
// call whose shared transaction is refused is run again on its own, so N
// concurrent failing writers cost one failed shared transaction and N
// single-call transactions on top of it.
//
// Every call here is refused, so none of the solo transactions commits:
// BenchmarkBoltBatchCreateGroupCommit measures the case where the retries
// write, which is the expensive one.
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
			b.ReportAllocs()
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

// BenchmarkBoltBatchCreateGroupCommit measures what bolt.DB.Batch does for
// concurrent calls: each iteration runs one BatchCreate in each of `requests`
// goroutines, and bbolt commits the calls that arrive together in one
// transaction rather than one per call.
//
// The conflict case makes the last call write a file where a directory already
// is. bbolt drops that call from the shared transaction and runs it again on
// its own, so the pair bounds what one bad batch costs the writers it was
// grouped with.
func BenchmarkBoltBatchCreateGroupCommit(b *testing.B) {
	for _, requests := range []int{1, 16} {
		for _, conflict := range []bool{false, true} {
			caseName := fmt.Sprintf("requests=%d", requests)
			if conflict {
				caseName += "/conflict"
			}
			b.Run(caseName, func(b *testing.B) {
				fsys, err := makeBoltFS(boltFSPrefix + filepath.Join(b.TempDir(), "bench.db"))
				if err != nil {
					b.Fatal(err)
				}
				defer ufsTesting.ValidateClose(b, fsys)()
				if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					var wg sync.WaitGroup
					for i := range requests {
						name := fmt.Sprintf("g%d/f%d.txt", requests, i)
						wantErr := conflict && i == requests-1
						if wantErr {
							// A file where a directory already is, refused the way
							// Create refuses it: the batch is valid, the key is not.
							name = "dir"
						}
						wg.Go(func() {
							err := ufs.BatchCreate(fsys, types.BatchFile{Name: name, Data: []byte("a")})
							if (err != nil) != wantErr {
								b.Errorf("BatchCreate(%q) = %v, wantErr %v", name, err, wantErr)
							}
						})
					}
					wg.Wait()
				}
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

	err = ufs.BatchCreate(fsys, types.BatchFile{Name: "a/b.txt", Data: []byte("b")})
	var pathErr *fs.PathError
	if !errors.Is(err, fs.ErrPermission) || !errors.As(err, &pathErr) || pathErr.Path != "a/b.txt" {
		t.Errorf("BatchCreate() on a read-only FS = %v, want an *fs.PathError for a/b.txt wrapping %v", err, fs.ErrPermission)
	}
	if _, err := fsys.Stat("a"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(a) = %v, want %v", err, fs.ErrNotExist)
	}
}
