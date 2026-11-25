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
	"errors"
	"net"
	"testing"

	"github.com/cloudfra/ufs/drivers/grpcfs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc"
)

// TestGrpcFSDriver runs the shared conformance suite against the driver the way a
// caller of ufs.New meets it: a Server answering the FS service on a port of its
// own, and a file system built from the grpc:// URI of that address. Every fixture
// the suite asks for is a server of its own on a port of its own, which is what
// lets the suite compare two fixtures by address and find them different: the
// driver answers IsSame by comparing addresses, so one address shared by two
// fixtures would be a claim that they hold one tree.
//
// What this covers that the served-client run does not is the front door of the
// driver rather than its internals — parseGrpcURI deciding that the address is a
// usable one, the scheme choosing a plaintext connection, and the URI the file
// system answers with being an address that opens the same server again. The
// translation underneath is the one TestClientFSConformance runs over an in-memory
// listener, where none of that is in the path.
func TestGrpcFSDriver(t *testing.T) {
	ufsdriversTesting.FS(t, ufsdriversTesting.Args{}, func(t *testing.T) types.SimpleFS {
		return driverFS(t)
	})
}

// driverFS returns the driver's file system for a served address, built by New
// from a URI rather than handed a client that is already dialed. New dials
// nothing, so a failure to reach the server shows up as the first call the suite
// makes rather than here.
//
// Nothing here closes what it returns: every suite in drivers/testing closes the
// file system it is handed, and a second Close on a driver that dialed its own
// connection answers with grpc-go taking that connection down (codes.Canceled,
// "the client connection is closing") instead of with anything the file system
// did wrong.
func driverFS(tb testing.TB) types.WriteFS {
	tb.Helper()

	fsys, err := grpcfs.New(tb.Context(), "grpc://"+serveAddress(tb, servedFS(tb)))
	if err != nil {
		tb.Fatalf("New: %v", err)
	}
	return fsys
}

// serveAddress starts a gRPC server for inner on a port of its own and answers the
// address to dial it at, for the tests that have to name a served file system the
// way a caller does. 127.0.0.1 with no port chosen says both what is reached and
// that nothing outside this process can reach it.
//
// The server carries grpcfs.ServerOptions, which is what a host embedding a Server
// is told to put on its own: without it the transport enforces grpc-go's 4 MiB
// default rather than the ceiling the service documents, and a suite that writes a
// large file would be testing a server no host runs.
func serveAddress(tb testing.TB, inner types.WriteFS) string {
	tb.Helper()

	var lc net.ListenConfig
	lis, err := lc.Listen(tb.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("Listen: %v", err)
	}
	grpcServer := grpc.NewServer(grpcfs.ServerOptions()...)
	proto.RegisterFSServer(grpcServer, grpcfs.NewServer(inner))
	go func() {
		if err := grpcServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			tb.Errorf("Serve: %v", err)
		}
	}()
	// GracefulStop for the reason serve gives: the client built on this address is
	// closed before this cleanup runs, so its handlers have been canceled and only
	// need to unwind.
	tb.Cleanup(grpcServer.GracefulStop)
	return lis.Addr().String()
}
