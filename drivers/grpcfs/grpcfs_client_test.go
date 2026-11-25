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

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/cloudfra/ufs/drivers/grpcfs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc"
)

// servedClientURI returns the address a served client says it is at. The service
// puts no part of a file system in the URL, so a scheme and a host is the whole
// address, and the host here names the in-memory listener the client was dialed
// through — which the checks on URI accept, because they ask that an address be
// absolute, stable and re-parseable rather than reachable. An address a dialer
// can connect to is covered by TestClientReopensThroughUfsNew.
//
// Every served client gets a host of its own because the driver answers IsSame by
// comparing addresses, and each client here is served by a server of its own
// holding a tree of its own: one address for all of them would be a claim
// that they hold one set of data, which the shared suite checks against what they
// actually hold. TestClientsOnOneAddressReportOneFileSystem covers the two
// clients that do share an address.
var servedAddresses atomic.Uint64

func servedClientURI() string {
	return "grpc://bufconn-" + strconv.FormatUint(servedAddresses.Add(1), 10)
}

// servedClient returns a driver file system talking to a served file system. The
// file system behind the server is the same memFS the server tests serve, so the
// whole path is inside the stack: a caller in this package, an rpc, and a file
// system of this repository at the other end, with nothing of the machine running
// the test in between.
func servedClient(tb testing.TB) types.WriteFS {
	tb.Helper()

	fsys, err := grpcfs.NewWithClient(tb.Context(), serve(tb, servedFS(tb)), servedClientURI())
	if err != nil {
		tb.Fatalf("NewWithClient: %v", err)
	}
	tb.Cleanup(func() { mustClose(tb, fsys) })
	return fsys
}

// TestClientFSConformance runs the shared suite over a client whose server is a
// memFS, which is the whole translation under test: the paths the driver
// validates before it dials, the status a call answers with coming back as the Go
// error the file system behind the server returned, and the handles and listings
// a caller gets from rpcs alone. The parts of the suite that ask for an optional
// interface are skipped by the suite itself, because a driver that answers
// types.WriteFS is what the service can back.
func TestClientFSConformance(t *testing.T) {
	ufsdriversTesting.FS(t, ufsdriversTesting.Args{}, func(t *testing.T) types.SimpleFS {
		return servedClient(t)
	})
}

// serveTCP starts the same server as serve on a port of its own and answers with
// the address to connect to, for a test that has to name the file system the way
// a caller of ufs.New would.
func serveTCP(tb testing.TB, inner types.WriteFS) string {
	tb.Helper()

	var lc net.ListenConfig
	lis, err := lc.Listen(tb.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("Listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	proto.RegisterFSServer(grpcServer, grpcfs.NewServer(inner))
	go func() {
		if err := grpcServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			tb.Errorf("Serve: %v", err)
		}
	}()
	// GracefulStop for the reason serve gives: the clients built on this address
	// are closed before this cleanup runs, so the handlers have been canceled and
	// only need to unwind.
	tb.Cleanup(grpcServer.GracefulStop)
	return lis.Addr().String()
}

// TestClientReopensThroughUfsNew opens a served file system from its address
// alone, which is what shows the driver is wired into ufs for both schemes the
// service needs and that an address reaches the server it names: the client in
// every other test here is handed one that is already dialed.
func TestClientReopensThroughUfsNew(t *testing.T) {
	inner := servedFS(t)
	// The tree is seeded through the file system behind the server rather than
	// through a client, because that is the state a caller finds a served address
	// in: a tree that is already there, with its levels already made.
	if err := inner.MkdirAll("dir", 0o777); err != nil {
		t.Fatal(err)
	}
	ufsdriversTesting.WriteFile(t, inner, "dir/file.txt", "served over a port")

	ufsdriversTesting.OpenWithNew(t, "grpc://"+serveTCP(t, inner), map[string]string{
		"dir/file.txt": "served over a port",
	})
}

// oneAddressFS is what comparing two clients of one server needs: the file system
// the driver returns, plus the comparison and the closer that belong to the full
// file system rather than to the types.WriteFS New answers with.
type oneAddressFS interface {
	types.ComparableFS
	types.CreateFileFS
	io.Closer
}

// clientAtAddress returns the driver's file system for an address, as a caller of
// ufs.New would get it from a grpc:// URI.
func clientAtAddress(tb testing.TB, address string) oneAddressFS {
	tb.Helper()

	fsys, err := grpcfs.New(tb.Context(), "grpc://"+address)
	if err != nil {
		tb.Fatalf("New(%q): %v", address, err)
	}
	client, ok := fsys.(oneAddressFS)
	if !ok {
		tb.Fatal("a driver file system is not comparable and closable")
	}
	tb.Cleanup(func() { mustClose(tb, client) })
	return client
}

// TestClientsOnOneAddressReportOneFileSystem covers the half of IsSame that
// isSameIndependent cannot: two file systems dialed at the same address are two
// views of one served tree, so what one writes is what the other reads back.
func TestClientsOnOneAddressReportOneFileSystem(t *testing.T) {
	address := serveTCP(t, servedFS(t))

	first := clientAtAddress(t, address)
	second := clientAtAddress(t, address)

	if !first.IsSame(second) {
		t.Errorf("%T.IsSame(%T) = false, want true because both are dialed at %q and hold one tree", first, second, address)
	}
	if !second.IsSame(first) {
		t.Errorf("%T.IsSame(%T) = false, want the same answer the other way round", second, first)
	}

	const marker = "comparable-marker.txt"
	ufsdriversTesting.WriteFile(t, first, marker, "written through the first client")
	got, err := fs.ReadFile(second, marker)
	if err != nil {
		t.Fatalf("ReadFile(%q) through the second client = %v, want the write seen because the two hold one tree", marker, err)
	}
	if string(got) != "written through the first client" {
		t.Errorf("ReadFile(%q) through the second client = %q, want what the first wrote", marker, got)
	}
}

// TestNewRefusesAddressesItCannotName covers the names that are not addresses
// this driver can use. Each is answered before anything is dialed, and by the
// driver rather than the nest that would wrap it, because a name of another
// scheme is not this driver's to open and a path inside an address asks for a
// part of a file system the service has no way to name.
func TestNewRefusesAddressesItCannotName(t *testing.T) {
	for _, name := range []string{
		"http://example.com:1",
		"grpc://",
		"grpcs://",
		"grpc://host:1/some/dir",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := grpcfs.New(t.Context(), name)
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Fatalf("New(%q) = %v, want an *fs.PathError", name, err)
			}
			if pathErr.Op != "init" || pathErr.Path != name {
				t.Errorf("New(%q) = %+v, want Op %q and Path %q", name, pathErr, "init", name)
			}
		})
	}
}

// TestClosedClientLeavesTheConnectionToItsCaller covers the one difference
// between a file system the driver dialed for itself and one it was handed:
// closing the file system stops it working either way, but only the first has a
// connection to give up.
func TestClosedClientLeavesTheConnectionToItsCaller(t *testing.T) {
	client := serve(t, servedFS(t))
	fsys, err := grpcfs.NewWithClient(t.Context(), client, servedClientURI())
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := client.GetFile(t.Context(), &proto.GetFileRequest{Name: "."}); err != nil {
		t.Errorf("GetFile on the caller's client after Close = %v, want the connection left for its owner", err)
	}
	if _, err := fsys.Stat("."); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Stat after Close = %v, want an error wrapping %v", err, fs.ErrClosed)
	}
}

// errUploadGone stands for the failure a frame of an upload meets when the
// connection gives up on it partway through, which is what the Send of a
// client-streaming call reports.
var errUploadGone = errors.New("the connection went away mid-upload")

// brokenUploadClient hands out a WriteFile stream that stops taking frames, so
// that an upload can be cut in the middle without a real connection having to
// fail. Every other rpc goes to the real client, which is what keeps the check
// Create makes before it answers, and the read back afterwards, real as well.
type brokenUploadClient struct {
	proto.FSClient

	// frames is how many frames the upload takes before it breaks.
	frames int
}

func (c *brokenUploadClient) WriteFile(ctx context.Context, opts ...grpc.CallOption) (proto.FS_WriteFileClient, error) {
	stream, err := c.FSClient.WriteFile(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &brokenUploadStream{FS_WriteFileClient: stream, frames: c.frames}, nil
}

// brokenUploadStream is the upload that breaks once its frames are used up.
type brokenUploadStream struct {
	proto.FS_WriteFileClient

	frames int
}

func (s *brokenUploadStream) Send(req *proto.WriteFileRequest) error {
	if s.frames <= 0 {
		return errUploadGone
	}
	s.frames--
	return s.FS_WriteFileClient.Send(req)
}

// TestUploadCloseReportsABrokenUpload covers what Close answers for an upload the
// connection gave up on partway through. The server makes the file only once the
// whole stream is in, so a cut upload leaves nothing behind, and Close is the
// answer a caller that copied into the handle and checked only Close reads: it
// has to report the failure rather than say that the file was written.
func TestUploadCloseReportsABrokenUpload(t *testing.T) {
	const name = "upload-cut-midway.txt"

	client := &brokenUploadClient{FSClient: serve(t, servedFS(t)), frames: 1}
	fsys, err := grpcfs.NewWithClient(t.Context(), client, servedClientURI())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mustClose(t, fsys) })

	f, err := fsys.Create(name)
	if err != nil {
		t.Fatalf("Create(%q) = %v, want nil: the name is free", name, err)
	}

	// The first frame carries the name and the mode along with its bytes, so the
	// frame that breaks is the one carrying the second write.
	if _, err := f.Write([]byte("the part that made it")); err != nil {
		t.Fatalf("the first Write = %v, want it taken", err)
	}
	if _, err := f.Write([]byte(" and the part that did not")); !errors.Is(err, errUploadGone) {
		t.Errorf("Write after the stream broke = %v, want an error wrapping %v", err, errUploadGone)
	}

	err = f.Close()
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("Close after a broken upload = %v, want an *fs.PathError", err)
	}
	if pathErr.Op != "close" || pathErr.Path != name {
		t.Errorf("Close after a broken upload = %+v, want Op %q and Path %q", pathErr, "close", name)
	}
	if !errors.Is(err, errUploadGone) {
		t.Errorf("Close after a broken upload = %v, want the failure that ended the upload", err)
	}
	if again := f.Close(); !errors.Is(again, errUploadGone) {
		t.Errorf("the second Close = %v, want the same answer as the first", again)
	}
	if info, statErr := fsys.Stat(name); statErr == nil {
		t.Errorf("Stat(%q) after a broken upload = %v, want no file: the server writes only once the whole stream is in", name, info.Name())
	}
}
