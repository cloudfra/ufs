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
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
)

var _ proto.FSServer = (*Server)(nil)

// Server answers the FS service of proto/ufs.proto from a file system the
// process already holds, which is how that file system is read and written from
// another process or another machine.
//
// The file system is taken as types.WriteFS because the service carries each
// method reachable from that interface exactly once, and anything narrower would
// leave rpcs with nothing to answer them. What types.WriteFS does not reach is the
// optional interfaces; the service asks for none of them, so a server offers the
// same rpc surface whatever the file system standing behind it carries. The one
// thing a handler needs that the interface does not promise is a whole-file write,
// which it asks of drivers/polyfills: polyfills uses the file system's own
// [types.WriteFileFS] when it has one and writes the file through a handle when it
// does not. The handler never learns which of those answered, and the difference
// between them is limited to what the file system's own methods already decide
// about the mode of a new file and the levels above a name.
//
// Close and Release are deliberately not called here. The service has no rpc for
// either — see the service comment — and a server cannot decide when every client
// it serves is done with the file system, so what it was handed stays the caller's
// to close. What a handler does close is a file handle it opened itself, which the
// caller of the rpc never sees.
//
// The handlers are in the files beside this one, one file per group of like rpcs —
// the lookups in grpcfs_server_meta.go, the listing in grpcfs_server_list.go, the
// reads in grpcfs_server_read.go, the upload in grpcfs_server_write.go, the tree
// changes in grpcfs_server_mutation.go — with the mappings between the wire types
// and io/fs in fileinfo.go, deviceinfo.go and errors.go, which the client driver in
// this package shares with these handlers. So a change to one rpc moves one file,
// which is the point: two PRs touching two rpcs do not touch the same file.
type Server struct {
	inner types.WriteFS

	// proto.UnimplementedFSServer is embedded by value so that an rpc added to
	// the .proto before this file catches up answers codes.Unimplemented instead
	// of silently doing nothing.
	proto.UnimplementedFSServer
}

// NewServer returns a Server serving inner. Register it on a grpc.Server with
// proto.RegisterFSServer, and put [ServerOptions] on that server as well: its
// message ceiling is the one this package's handlers assume.
func NewServer(inner types.WriteFS) *Server {
	return &Server{inner: inner}
}
