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

package grpcfs

import (
	"context"
	"crypto/tls"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// types.FS and the optional interfaces in grpcfs_client_optional.go are what
// this file system answers, each method one rpc of the service (OpenFile is
// built from Open and Create).
var _ types.WriteFS = (*grpcFS)(nil)

const (
	// grpcScheme is the scheme of a file system reached through the FS service
	// over a plaintext connection, grpcsScheme the same service over TLS. Both
	// name the same rpcs, so one driver answers both and the scheme says only
	// how the connection is protected.
	grpcScheme  = "grpc"
	grpcsScheme = "grpcs"

	// frameBytes is the largest payload this client puts into one frame of a
	// streaming rpc, in either direction. 4 MB is the largest message gRPC lets
	// a peer receive unless a side raises the limit of its own, so a frame well
	// under it moves a large file without either end having to be configured for
	// it. This is the client's policy for the same reason readChunkSize is the
	// server's: the wire format leaves the size of a frame to whoever is sending
	// one.
	frameBytes = 1 << 20
)

func init() {
	ufs.Register(ufs.NewDriver(grpcScheme, New, isGrpcFSURI, 1, true, true))
	ufs.Register(ufs.NewDriver(grpcsScheme, New, isGrpcsFSURI, 1, true, true))
}

// isGrpcFSURI reports whether name is a grpc:// URI, and isGrpcFSURI the grpcs://
// form of the same address. A grpcs:// address does not begin with grpc:// — the
// s comes before the separator — so the two tests cannot both match one name.
func isGrpcFSURI(name string) bool {
	return strings.HasPrefix(name, grpcScheme+"://")
}

func isGrpcsFSURI(name string) bool {
	return strings.HasPrefix(name, grpcsScheme+"://")
}

// New returns a file system that reads and writes the file system a server
// exposes through the FS service at the address named by a grpc:// or grpcs://
// URI, such as grpc://fs.example.com:9000. The address is all a grpc:// URI
// names: the service names no part of the file system in its URL — every rpc
// carries the path it acts on — so a subtree of a served file system is chosen
// by mounting it, which is what the nestFS layer the base package puts over
// every driver is for, and a URI that tries to name one is an error rather than
// a mount that is not there.
//
// Nothing is dialed here: a gRPC connection starts on the first call, so a file
// system for an address that is down at the moment builds fine and fails on the
// first read.
//
// The address decides nothing but the address. uri.IsServingAddressString — true
// only of localhost, 127.0.0.1 and 0.0.0.0 — is not consulted here, for none of
// the three jobs it might look like it does:
//
//   - not to choose between serving and connecting, which are not two spellings
//     of one operation. A constructor that turned into a server for the addresses
//     it recognized would give grpc://localhost:9000 one meaning on a machine
//     that holds the port and another on a machine that does not, so that the URI
//     this file system answers with would not open the same thing back and IsSame
//     would compare a server against a client. Serving is what [NewServer] is
//     for: the process that runs a server says so, and the caller that reaches an
//     address never decides it.
//   - not to choose the transport, because the scheme already says it. A
//     connection made plaintext for a host that looked local would protect a file
//     system according to its hostname rather than according to the URI that
//     named it, and grpcs:// would stop meaning TLS.
//   - not to validate the address, because what that helper records is which
//     hosts a process has chosen to answer on, which says nothing about whether
//     an address can be dialed. Every host of these two schemes is a client; what
//     [parseGrpcURI] refuses is a name that is not such an address.
func New(ctx context.Context, name string) (types.WriteFS, error) {
	address, scheme, err := parseGrpcURI(name)
	if err != nil {
		return nil, err
	}
	var creds credentials.TransportCredentials
	if scheme == grpcsScheme {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	} else {
		creds = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient(address, append(DialOptions(), grpc.WithTransportCredentials(creds))...)
	if err != nil {
		return nil, ufserrors.NewPathError("init", name, err)
	}
	return newGrpcFS(ctx, proto.NewFSClient(conn), conn, address, scheme), nil
}

// NewWithClient returns a file system that spends its calls on an existing
// proto.FSClient, for a caller that dials its own grpc.ClientConn or talks to a
// server in its own process. name is the URI the file system reports for itself
// — it need not be dialable, but it has to be a grpc:// or grpcs:// address so
// that the URI the file system answers with opens the same thing back.
//
// Close on the returned file system stops it working, as it does on one built by
// [New], but leaves the connection alone: that is the caller's to close, which is
// the one way the two differ.
func NewWithClient(ctx context.Context, client proto.FSClient, name string) (types.WriteFS, error) {
	address, scheme, err := parseGrpcURI(name)
	if err != nil {
		return nil, err
	}
	return newGrpcFS(ctx, client, nil, address, scheme), nil
}

// parseGrpcURI reads the address and scheme out of a grpc:// or grpcs:// URI.
func parseGrpcURI(name string) (address, scheme string, err error) {
	uri, err := url.Parse(name)
	if err != nil {
		return "", "", ufserrors.NewPathError("init", name, err)
	}
	if uri.Scheme != grpcScheme && uri.Scheme != grpcsScheme {
		return "", "", ufserrors.NewPathError("init", name,
			fmt.Errorf("%q is not a %s:// or %s:// address", name, grpcScheme, grpcsScheme))
	}
	if uri.Host == "" {
		return "", "", ufserrors.NewPathError("init", name, fmt.Errorf("%q names no host to connect to", name))
	}
	if uri.Path != "" && uri.Path != "/" {
		return "", "", ufserrors.NewPathError("init", name,
			fmt.Errorf("%q names the path %q inside the file system, which a %s:// address does not do: mount the subtree instead",
				name, uri.Path, uri.Scheme))
	}
	return uri.Host, uri.Scheme, nil
}

// grpcFS is a file system whose every method is one rpc of the FS service.
//
// Nothing here is emulated but OpenFile: every other method is one rpc, and an
// emulation the served file system needs runs on the server, beside the data,
// so a call that comes back refused carries the server's own reason rather
// than a standin built on this side of the connection.
type grpcFS struct {
	client  proto.FSClient
	conn    *grpc.ClientConn
	address string
	scheme  string

	// ctx bounds every rpc this file system makes. The methods of types.WriteFS
	// take no context of their own, so the one the file system was built with is
	// the only one there is to bound them with — the same gap gcsfs answers the
	// same way.
	ctx context.Context

	// closed says Close has been called, after which this file system refuses
	// everything it would have put on the wire.
	closed atomic.Bool
}

func newGrpcFS(ctx context.Context, client proto.FSClient, conn *grpc.ClientConn, address, scheme string) *grpcFS {
	return &grpcFS{client: client, conn: conn, address: address, scheme: scheme, ctx: ctx}
}

// closedErr answers why a call cannot be made because the file system has been
// closed, or nil when it can. A file system that gave its connection up has no
// way to answer anything else, so every method that reaches the server asks here
// rather than letting the transport report a connection that is gone.
func (fsys *grpcFS) closedErr(op, name string) error {
	if fsys.closed.Load() {
		return ufserrors.NewPathError(op, name, fs.ErrClosed)
	}
	return nil
}

// Close makes this file system unusable and gives up the connection that came
// with [New]. Closing twice is not an error, and a file system that is closed
// reports [fs.ErrClosed] for every call afterwards, whether it had a connection
// of its own or not: what Close answers for is the file system, and a file system
// built by [NewWithClient] leaves its connection to the caller that made it.
func (fsys *grpcFS) Close() error {
	if !fsys.closed.CompareAndSwap(false, true) || fsys.conn == nil {
		return nil
	}
	return fsys.conn.Close()
}

// Release does nothing. The service has no rpc for it: what it lets go of is
// what the server holds for every client of it, which is why the decision is
// left to the process that runs the server.
func (fsys *grpcFS) Release() error {
	return nil
}
