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

package grpcfs_test

// These tests hold the two halves of the client's lifetime guard to what they
// claim. A flag that says a file system is closed by the time it is read is not
// the same thing as a call never working through a connection that Close has
// given up on, so what is asked here is whether a call that had already started
// can be cut off in the middle, and that is decided by the order of recorded
// events rather than by any wait.
//
// The whole path is inside the test: a caller in this package, a client built by
// [grpcfs.New] from an address it dials, a server on a port of its own, and a file
// system of this repository behind it. The address matters because only a client
// the driver dialed for itself has a connection to give up, which is the teardown
// an operation can be waiting behind.

import (
	"errors"
	"io/fs"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudfra/ufs/drivers/core"
	"github.com/cloudfra/ufs/drivers/grpcfs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
)

// listedChildren is how many children the served directory holds. It is one more
// than the page size the service answers a listing with, so the listing a caller
// asks for is two rpcs on one connection rather than one. The tests check that the
// walk really did take two pages, so if the page size ever changes the test says
// so instead of quietly checking less.
const listedChildren = 1001

// parkedListingFS is the file system behind the server for these tests. It wraps a
// memFS and stops the first listing inside the rpc, so that an operation can be
// held on the wire while Close is called, and it writes down the order the two
// ends of that stop happen in alongside the order Close returned in. Nothing here
// waits on the clock: the parked listing is let go by the test, and the guard is
// what settles the order of the records — a lock decides that, a scheduler does
// not.
type parkedListingFS struct {
	types.WriteFS

	// entered is closed when a listing is parked inside the rpc, and release is
	// what lets it answer.
	entered chan struct{}
	release chan struct{}

	// Only the first page is parked, so the rest of the walk runs on its own and
	// the records show where in a walk of several rpcs a connection given up
	// would have landed.
	once sync.Once

	// pages counts how many times the served file system was asked for the
	// listing, which is how a test knows the walk it waited for was more than one
	// rpc rather than one rpc that happened to be slow.
	pages atomic.Int64

	// logs records what happened in the order it happened; reled releases a
	// parked listing at most once.
	mu    sync.Mutex
	logs  []string
	reled sync.Once
}

// log records what happened, in the order it happened, for a test to read back.
func (p *parkedListingFS) log(what string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logs = append(p.logs, what)
}

// recorded answers where what happened sits in the record, or -1 when it never
// happened.
func (p *parkedListingFS) recorded(what string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, logged := range p.logs {
		if logged == what {
			return i
		}
	}
	return -1
}

// letGo releases a parked listing. It is safe to call more than once, because a
// test that fails partway still has to let the served handler go before the server
// is stopped.
func (p *parkedListingFS) letGo() {
	p.reled.Do(func() { close(p.release) })
}

// ReadDir is the rpc a listing is served from, so this is where a walk can be held
// open. The first page waits; the pages after it answer as the memFS underneath
// does.
func (p *parkedListingFS) ReadDir(name string) ([]fs.DirEntry, error) {
	p.once.Do(func() {
		p.log("page-1-entered")
		close(p.entered)
		<-p.release
		p.log("page-1-answered")
	})
	if page := p.pages.Add(1); page > 1 {
		p.log("page-" + strconv.FormatInt(page, 10) + "-entered")
	}
	return p.WriteFS.ReadDir(name)
}

// parkedServed returns the parked file system with its directory already seeded,
// and the address it is served at. The seeding goes through the file system behind
// the server rather than through a client, because that is the state a caller finds
// a served address in.
func parkedServed(tb testing.TB) (*parkedListingFS, string) {
	tb.Helper()

	inner := core.MakeMemFS("memory://test")
	if err := inner.MkdirAll("dir", 0o777); err != nil {
		tb.Fatalf("MkdirAll: %v", err)
	}
	for n := range listedChildren {
		ufsdriversTesting.WriteFile(tb, inner, "dir/file-"+strconv.Itoa(n)+".txt", "served over a port")
	}
	parked := &parkedListingFS{
		WriteFS: inner,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	return parked, serveTCP(tb, parked)
}

// TestClientCloseWaitsForAnInFlightListing is the reason the guard is a lock and
// not only a flag. A listing the service answers in two pages is two rpcs on one
// connection: the first is parked inside the served handler, Close is called while
// it is there, and the test then reads back what happened. The guard holds if and
// only if the second page went out on a connection that was still alive and Close
// answered only once the whole walk had answered, and that is an order a lock
// settles for every schedule rather than one a test can hope for.
func TestClientCloseWaitsForAnInFlightListing(t *testing.T) {
	parked, address := parkedServed(t)
	fsys, err := grpcfs.New(t.Context(), "grpc://"+address)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Registered after serveTCP so that it runs before the server is stopped,
	// which is what keeps a failing test from waiting on a parked handler.
	t.Cleanup(parked.letGo)

	listed := make(chan []fs.DirEntry, 1)
	failed := make(chan error, 1)
	go func() {
		entries, err := fsys.ReadDir("dir")
		if err != nil {
			failed <- err
			return
		}
		listed <- entries
	}()

	// The walk is on the wire: the served handler for the first page is inside
	// the file system behind it, and the client is waiting for that answer.
	<-parked.entered

	closed := make(chan error, 1)
	go func() {
		err := fsys.Close()
		parked.log("close-returned")
		closed <- err
	}()

	// Nothing in the wait that follows is a guess about how long Close takes. The
	// parked page is let go because the test says so, and the guard is what
	// decides whether the walk or the teardown finishes first.
	parked.letGo()

	var entries []fs.DirEntry
	select {
	case entries = <-listed:
	case err := <-failed:
		// The only way a walk that had already started can fail is a connection
		// given up underneath it, which is exactly what the guard is for.
		t.Errorf("ReadDir with Close called while it was on the wire = %v, want the listing to finish", err)
	}
	if len(entries) != listedChildren {
		t.Errorf("ReadDir answered %d children, want %d: the walk stopped partway", len(entries), listedChildren)
	}
	if err := <-closed; err != nil {
		t.Errorf("Close = %v, want nil", err)
	}

	// The record, not the clock, is the proof: the second page was asked for after
	// the first had been answered and before the teardown ran, so the whole walk
	// and the closed check were one section, and Close waited behind that section.
	firstAnswered, closeReturned := parked.recorded("page-1-answered"), parked.recorded("close-returned")
	if firstAnswered < 0 || closeReturned < 0 {
		t.Fatalf("the record is %v, want both the parked page answering and Close returning", parked.logs)
	}
	if firstAnswered > closeReturned {
		t.Errorf("the record is %v, want the listing answered before Close returned", parked.logs)
	}
	secondPage := parked.recorded("page-2-entered")
	if secondPage < 0 {
		t.Fatalf("the served file system was asked for the listing %d times, want two pages, so that the walk under test is a paged one: the record is %v",
			parked.pages.Load(), parked.logs)
	}
	if secondPage > closeReturned {
		t.Errorf("the record is %v, want the second page asked for before Close returned", parked.logs)
	}

	// And from here on nothing is accepted at all, which is the other half of the
	// same section: the check is made under the lock the teardown waits on.
	if _, err := fsys.ReadDir("dir"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("ReadDir after Close = %v, want an error wrapping %v", err, fs.ErrClosed)
	}
	if err := fsys.Close(); err != nil {
		t.Errorf("the second Close = %v, want nil: the first Close claimed the teardown", err)
	}
}

// TestClientRefusesEveryCallAfterClose covers the surface the guard answers for:
// every entry point that reaches the server, including the ones that only delegate
// to another method of the same file system, the two verbs that share a helper, and
// the one that hands back a handle. The names are all well formed, because a bad
// name is refused for being a bad name whether the file system is closed or not.
func TestClientRefusesEveryCallAfterClose(t *testing.T) {
	fsys, err := grpcfs.New(t.Context(), "grpc://"+serveTCP(t, servedFS(t)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	writer, ok := fsys.(types.WriteFileFS)
	if !ok {
		t.Fatalf("%T does not answer types.WriteFileFS, so the streaming entry point is not being covered", fsys)
	}
	if err := fsys.Close(); err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}

	calls := []struct {
		// name is the method, op the verb the driver names an error with, and path
		// the name it was handed.
		name string
		op   string
		path string
		call func() error
	}{
		{"Open", "open", "dir/file.txt", func() error { _, err := fsys.Open("dir/file.txt"); return err }},
		{"ReadFile", "readfile", "dir/file.txt", func() error { _, err := fsys.ReadFile("dir/file.txt"); return err }},
		{"ReadDir", "readdir", "dir", func() error { _, err := fsys.ReadDir("dir"); return err }},
		{"ReadLink", "readlink", "dir/file.txt", func() error { _, err := fsys.ReadLink("dir/file.txt"); return err }},
		{"Stat", "stat", "dir/file.txt", func() error { _, err := fsys.Stat("dir/file.txt"); return err }},
		{"Lstat", "lstat", "dir/file.txt", func() error { _, err := fsys.Lstat("dir/file.txt"); return err }},
		{"Create", "create", "dir/new.txt", func() error { _, err := fsys.Create("dir/new.txt"); return err }},
		{"WriteFile", "writefile", "dir/new.txt", func() error { return writer.WriteFile("dir/new.txt", []byte("closed"), 0o644) }},
		{"MkdirAll", "mkdir", "dir/new", func() error { return fsys.MkdirAll("dir/new", 0o777) }},
		{"Remove", "remove", "dir/file.txt", func() error { return fsys.Remove("dir/file.txt") }},
		{"RemoveAll", "removeall", "dir", func() error { return fsys.RemoveAll("dir") }},
	}
	for _, call := range calls {
		err = call.call()
		if !errors.Is(err, fs.ErrClosed) {
			t.Errorf("%s on a closed file system = %v, want an error wrapping %v", call.name, err, fs.ErrClosed)
			continue
		}
		var pathErr *fs.PathError
		if !errors.As(err, &pathErr) {
			t.Errorf("%s on a closed file system = %v, want an *fs.PathError naming the call", call.name, err)
			continue
		}
		if pathErr.Op != call.op || pathErr.Path != call.path {
			t.Errorf("%s on a closed file system = %+v, want Op %q and Path %q", call.name, pathErr, call.op, call.path)
		}
	}

	// A file system the driver dialed gave its connection up, so a second Close has
	// nothing left to do and says so rather than claiming the teardown again.
	if err := fsys.Close(); err != nil {
		t.Errorf("the second Close = %v, want nil", err)
	}
}

// TestClientCallsMadeWhileCloseWaitsAreRefusedNotCutOff is the deadlock audit run
// as a test. Several operations are asked for while a listing is parked on the
// wire and a Close is waiting behind it: some of them only delegate to another
// method of the same file system, some make several rpcs of their own, and none of
// them may take the lifetime lock and then reach a method that takes it again,
// which is the one way this shape could wedge. A method that did would not fail
// this test, it would hang it, which is what the go test timeout is for. What is
// checked here is the other half: a call that does get an answer gets either its
// own answer or the refusal, and never a failure from a connection that was given
// up underneath it.
//
// Each call works on a subtree of its own, and the subtrees are disjoint from one
// another, so that a call that runs before the teardown cannot undo what another
// is asking about — a RemoveAll over a name a sibling call opened is a failure of
// the test, not of the guard. ReadLink is left out: a served memFS holds no links,
// so its answer about a name that is not a link would look like a failure here
// without being one.
func TestClientCallsMadeWhileCloseWaitsAreRefusedNotCutOff(t *testing.T) {
	parked, address := parkedServed(t)

	// Every name a call below works on, seeded through the served file system
	// before any of it starts.
	for _, name := range []string{"open", "readfile", "stat", "lstat", "create", "writefile", "mkdirall", "remove", "removeall"} {
		if err := parked.MkdirAll(name, 0o777); err != nil {
			t.Fatalf("MkdirAll(%q): %v", name, err)
		}
		ufsdriversTesting.WriteFile(t, parked, name+"/file.txt", "seeded for the call that waits")
	}

	fsys, err := grpcfs.New(t.Context(), "grpc://"+address)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	writer, ok := fsys.(types.WriteFileFS)
	if !ok {
		t.Fatalf("%T does not answer types.WriteFileFS, so the streaming entry point is not being covered", fsys)
	}
	t.Cleanup(parked.letGo)

	walked := make(chan error, 1)
	go func() {
		_, err := fsys.ReadDir("dir")
		walked <- err
	}()
	<-parked.entered

	closed := make(chan error, 1)
	go func() { closed <- fsys.Close() }()

	type call struct {
		name string
		call func() error
	}
	calls := []call{
		{"Open", func() error { _, err := fsys.Open("open/file.txt"); return err }},
		{"ReadFile", func() error { _, err := fsys.ReadFile("readfile/file.txt"); return err }},
		{"Stat", func() error { _, err := fsys.Stat("stat/file.txt"); return err }},
		{"Lstat", func() error { _, err := fsys.Lstat("lstat/file.txt"); return err }},
		// Create looks the name up and then walks the levels above it, which is
		// more than one rpc and so is more than one round trip that could be cut
		// in the middle.
		{"Create", func() error { _, err := fsys.Create("create/new.txt"); return err }},
		{"WriteFile", func() error { return writer.WriteFile("writefile/new.txt", []byte("one stream"), 0o644) }},
		{"MkdirAll", func() error { return fsys.MkdirAll("mkdirall/new", 0o777) }},
		{"Remove", func() error { return fsys.Remove("remove/file.txt") }},
		{"RemoveAll", func() error { return fsys.RemoveAll("removeall") }},
	}
	// The answers come back in no particular order, so each carries the name of
	// the call it came from.
	type answer struct {
		name string
		err  error
	}
	answers := make(chan answer, len(calls))
	for _, call := range calls {
		go func() { answers <- answer{name: call.name, err: call.call()} }()
	}

	parked.letGo()

	if err := <-walked; err != nil {
		t.Errorf("the listing that was on the wire = %v, want it to finish", err)
	}
	if err := <-closed; err != nil {
		t.Errorf("Close = %v, want nil", err)
	}
	for range calls {
		got := <-answers
		if got.err != nil && !errors.Is(got.err, fs.ErrClosed) {
			t.Errorf("%s made while Close was waiting = %v, want its own answer or an error wrapping %v", got.name, got.err, fs.ErrClosed)
		}
	}
}
