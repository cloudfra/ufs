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

package gcsfs

// gcsFS.Close gives up the *storage.Client that every operation reaches the
// bucket through, so the closed flag is only worth having if the decision to use
// that client and the calls made through it are one critical section with
// respect to Close. These two tests pin that decision from the inside:
// TestGCSCloseWaitsForInFlightRequest parks a request inside the client's own
// transport and shows Close could not return until that request came back, and
// TestGCSOperationsRefusedAfterClose shows everything started afterwards is
// refused. Neither sleeps, polls or waits on a timeout — the ordering comes from
// the transport and the lock, so they cannot pass by being lucky.

import (
	"errors"
	"io/fs"
	"net/http"
	"sync/atomic"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/fsouza/fake-gcs-server/fakestorage"
	"google.golang.org/api/option"
)

// stalledTransport is an http.RoundTripper that parks the first request it sees
// inside the transport until the test releases it, and records both when that
// request entered and when it came back. The storage client cannot be replaced
// with a stub — it is a concrete struct — so its own HTTP round trip is the
// narrowest thing a test can watch to know that a call into the client is still
// under way. Every later request passes straight through.
type stalledTransport struct {
	base    http.RoundTripper
	entered chan struct{}
	served  chan struct{}
	release chan struct{}
	stalled atomic.Bool
}

func (t *stalledTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.stalled.CompareAndSwap(false, true) {
		return t.base.RoundTrip(req)
	}
	t.entered <- struct{}{}
	<-t.release
	resp, err := t.base.RoundTrip(req)
	t.served <- struct{}{}
	return resp, err
}

// newStalledGcsFS returns a file system over a one-object fake backend whose
// client talks to that backend through st.
func newStalledGcsFS(t *testing.T, st *stalledTransport) *gcsFS {
	t.Helper()

	server := fakestorage.NewServer([]fakestorage.Object{
		{
			ObjectAttrs: fakestorage.ObjectAttrs{BucketName: "first", Name: "a"},
			Content:     []byte("content: first/a"),
		},
	})
	t.Cleanup(server.Stop)
	st.base = server.HTTPClient().Transport

	// The fake backend's own transport ignores the host, so the client keeps the
	// default endpoint; WithoutAuthentication keeps the test off the network.
	httpClient := &http.Client{Transport: st}
	client, err := storage.NewClient(t.Context(), option.WithHTTPClient(httpClient), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("storage.NewClient(ctx) = %v, want nil", err)
	}
	fsys, err := makeGCSFSWithClient(t.Context(), client, "gs://first")
	if err != nil {
		t.Fatalf("makeGCSFSWithClient(ctx, client, %q) = %v, want nil", "gs://first", err)
	}
	return fsys
}

// TestGCSCloseWaitsForInFlightRequest closes a file system while one of its
// operations is parked inside the client. The operation is known to be inside
// because the transport says so; the release happens in the closing goroutine
// itself, so an operation that has already decided to use the client is the only
// thing that can let Close through. That is the barrier the atomic flag alone
// does not give: with only the flag, Close returns while the request is still in
// the transport, and the served check below reports it.
func TestGCSCloseWaitsForInFlightRequest(t *testing.T) {
	t.Parallel()

	const object = "a"

	st := &stalledTransport{
		entered: make(chan struct{}, 1),
		served:  make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	fsys := newStalledGcsFS(t, st)

	statErr := make(chan error, 1)
	go func() {
		_, err := fsys.Stat(object)
		statErr <- err
	}()

	// The request is now inside the client, past the point where the operation
	// decided this file system was open.
	<-st.entered

	closeErr := make(chan error, 1)
	go func() {
		close(st.release)
		closeErr <- fsys.Close()
	}()

	if err := <-closeErr; err != nil {
		t.Fatalf("Close() while an operation was in flight = %v, want nil", err)
	}
	// Close may only have taken the write lock after the read section that
	// covers this request ended, which is after the request came back.
	select {
	case <-st.served:
	default:
		t.Errorf("Close() returned while the request it raced had not come back from the transport: the closed check and the calls into the client have to be one critical section")
	}
	if err := <-statErr; err != nil {
		t.Errorf("Stat(%q) racing Close = %v, want nil: an operation that decided before Close is owed a finished request", object, err)
	}
	if _, err := fsys.Stat(object); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Stat(%q) after Close = %v, want an error matching fs.ErrClosed", object, err)
	}
}

// TestGCSOperationsRefusedAfterClose checks the decision every entry point makes
// once the client is gone. Each of these would put something on the wire, and
// each of them refuses with an error matching [fs.ErrClosed] rather than
// answering from a client that has been given up — including the names, like the
// root, that a method might have answered without a request.
func TestGCSOperationsRefusedAfterClose(t *testing.T) {
	t.Parallel()

	client := CreateFakeGcsBackend(t)
	fsys, err := makeGCSFSWithClient(t.Context(), client, "gs://first")
	if err != nil {
		t.Fatalf("makeGCSFSWithClient(ctx, client, %q) = %v, want nil", "gs://first", err)
	}
	if err := fsys.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if err := fsys.Close(); err != nil {
		t.Errorf("Close() the second time = %v, want nil: closing twice is not an error", err)
	}

	operations := []struct {
		name string
		call func() error
	}{
		{"Open", func() error { _, err := fsys.Open("a"); return err }},
		{"Open root", func() error { _, err := fsys.Open(pathutil.CwdPath); return err }},
		{"Stat", func() error { _, err := fsys.Stat("a"); return err }},
		{"Stat root", func() error { _, err := fsys.Stat(pathutil.CwdPath); return err }},
		{"Lstat", func() error { _, err := fsys.Lstat("a"); return err }},
		{"Create", func() error { _, err := fsys.Create("a"); return err }},
		{"ReadFile", func() error { _, err := fsys.ReadFile("a"); return err }},
		{"ReadDir", func() error { _, err := fsys.ReadDir("dir"); return err }},
		{"ReadDir root", func() error { _, err := fsys.ReadDir(pathutil.CwdPath); return err }},
		{"Remove", func() error { return fsys.Remove("a") }},
		{"RemoveAll", func() error { return fsys.RemoveAll("dir") }},
		{"RemoveAll root", func() error { return fsys.RemoveAll(pathutil.CwdPath) }},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.call(); !errors.Is(err, fs.ErrClosed) {
				t.Errorf("%s on a closed file system = %v, want an error matching fs.ErrClosed", operation.name, err)
			}
		})
	}
}
