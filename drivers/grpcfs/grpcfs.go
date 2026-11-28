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

// Package grpcfs carries a ufs file system over the FS service of
// proto/ufs.proto, in both directions: a Server that answers the service from a
// file system the process already holds, and a driver that turns an address
// running the service into a file system of its own.
//
// The service maps one rpc onto each method of types.FS and of the optional
// interfaces of drivers/types that can cross a wire — rename, copy, mkdir,
// chmod, chown and lchown, chtimes, symlink, link, closest parent and watch —
// so both directions are a translation of the interfaces rather than an
// invention. The server answers each optional rpc through drivers/polyfills:
// the served file system's own method, an emulation on the server that keeps
// the method's contract, or errors.ErrUnsupported, which a client receives
// back as that same sentinel. What stays off the wire is what a client cannot
// hold or cannot be promised: Close and Release, which act on the server's
// file system for every client at once; IsSame, InnerFS and ExternalPath,
// which are not questions about locations; and BatchCreate, whose all-or-none
// promise a client cannot know the served file system keeps when it is built.
// OpenFile has no rpc because the service holds no handle open: the client
// answers it from Open and Create.
//
// The two directions share the constants here, because they are the numbers the
// service documents rather than the numbers one side chose: the ceiling a frame
// may reach is the ceiling a client may send and the ceiling a server may accept.
package grpcfs

import (
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

const (
	// readChunkSize is how much file content one ReadFileResponse carries. How
	// big a whole file has to be before it is worth streaming rather than
	// sending whole is server policy rather than part of the wire format, so the
	// server picks it: small enough that a large file arrives in frames instead
	// of one buffered message, large enough that a modest file is one frame.
	//
	// A frame is not free, and that is the reason the chunk stops here rather than
	// lower: the same 8 MiB of content costs 29.8 MB and 290 allocations over the
	// wire in one frame, and 43.8 MB and 2433 allocations in 128 frames of this
	// size (BenchmarkUploadFrameSize). On the read side this is also the only
	// buffer a streamed read holds, whatever size the file turns out to be.
	readChunkSize = 64 << 10

	// defaultListPageSize is how many children ListFiles answers when the
	// request asks for no maximum of its own.
	defaultListPageSize = 1000

	// maxListPageSize is the most children one ListFiles page carries, however
	// many the request asks for, and what this package's client asks for. A
	// child is about a hundred bytes on the wire, so a full page is about a
	// megabyte: well inside [maxMessageSize], and a tenth of the round trips
	// the default page would cost for a large directory. The server re-reads
	// the directory for every page (see listOffset), so fewer pages is also
	// less work there.
	maxListPageSize = 10000

	// maxReadAhead is the largest window a run of ranged reads walking forward
	// through a file grows to. Each window is one round trip, so on a link with
	// latency the window bounds throughput: 4 MiB a round trip is 80 MB/s at
	// 50 ms. It is also what one open handle may hold, which is why it stops
	// here rather than growing with the file.
	maxReadAhead = 4 << 20

	// maxPreallocate is the most a client allocates up front on the strength
	// of a size the server announced, so a size that turns out to be wrong
	// costs at most this much before the content itself decides.
	maxPreallocate = 256 << 20

	// keepaliveTime is how long a client lets a connection with an rpc in
	// progress sit silent before it pings the server, and keepaliveTimeout how
	// long it waits for the answer before treating the connection as dead.
	// A Watch stream can sit silent for as long as nothing changes, and a
	// peer that disappeared without closing the connection would otherwise
	// leave it waiting forever. The server's enforcement policy in
	// [ServerOptions] permits pings this often.
	keepaliveTime    = time.Minute
	keepaliveTimeout = 20 * time.Second

	// maxMessageSize is the largest single message the service carries in either
	// direction. The number is the one the rpc comment already chooses: the
	// WriteFile comment in proto/ufs.proto puts 10 MB at the point above which
	// content is worth splitting across frames rather than sending in one, so a
	// message below it is what one frame is for and a message above it does not
	// occur.
	//
	// The transport enforces it, which is what [ServerOptions] is for, and the
	// read handler uses the same number for the largest file it is willing to hold
	// in memory in order to answer a request for a whole file.
	//
	// The number is worth what it costs, and the cost is linear in the message: a
	// single-frame 8 MiB upload measures 29.8 MB of allocation and 6.3 ms over the
	// in-process path, a 1 MiB one 3.5 MB and 0.8 ms (BenchmarkUploadFrameSize,
	// BenchmarkUploadSize), so a message is a fixed multiple of itself and the
	// ceiling is the whole of the policy. It sits a little above twice
	// grpc-go's own 4 MiB default — defaultServerMaxReceiveMessageSize, and
	// MaxCallRecvMsgSize says the same for the client side — because that default
	// refuses the single-frame upload the rpc documents, and it stops there
	// because one message at the ceiling is already worth some tens of megabytes.
	maxMessageSize = 10 << 20

	// maxUploadSize is the largest whole content a WriteFile request may carry,
	// counted over all of its frames rather than per frame. It is a handler
	// ceiling rather than a transport one because the handler holds the content it
	// is given: the write is handed to the file system whole, so an upload costs
	// the server its size in memory for as long as the request runs, and without a
	// ceiling the number of frames a client sends bounds nothing.
	//
	// It sits above [maxMessageSize] on purpose: the reason the upload is a stream
	// at all is that a file larger than one message still has to fit. It stops
	// there because a file much larger than that should not be buffered in the
	// first place, and this service has no rpc that writes an upload as it arrives.
	//
	// The measurement behind where "much larger" starts is BenchmarkUploadSize:
	// 1 MiB costs 3.5 MB, 8 MiB costs 42 MB, 30 MiB costs 163 MB, so a buffered
	// upload is worth about five times itself per request, and 32 MiB is where one
	// request's peak reaches the hundreds of megabytes a host should be able to
	// refuse. Refusal is not free — a 33 MiB upload measures 170 MB, because the
	// frames are received before the handler can answer — so what this ceiling
	// bounds is what the handler accumulates and writes, not what a client can put
	// on the wire.
	maxUploadSize = 32 << 20
)

// ServerOptions returns the grpc.ServerOption values a host needs on the
// grpc.Server it registers a [Server] on, so that the message ceiling the service
// documents is also the ceiling the transport enforces.
//
// grpc-go refuses a received message larger than 4 MiB unless told otherwise
// (defaultServerMaxReceiveMessageSize), while the WriteFile rpc documents an
// upload that puts its whole content in one frame up to the 10 MB ceiling the rpc
// comment names: without this option a host cannot accept the single-frame upload
// the service describes, and the refusal it gets is the transport's, at a number
// nothing in this package chose.
//
// The other direction needs no option: a read response carries at most
// [readChunkSize] of content, well under any default.
//
// It also permits the keepalive pings this package's client sends (grpc-go's
// default policy refuses a ping more often than every five minutes and ends
// the connection of a client that sends them), and pings idle clients itself so
// a connection to a client that vanished is let go.
func ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.MaxRecvMsgSize(maxMessageSize),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime: keepaliveTime / 2,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    2 * keepaliveTime,
			Timeout: keepaliveTimeout,
		}),
	}
}

// DialOptions returns the grpc.DialOption values [New] dials with, for a
// caller of [NewWithClient] that dials its own connection: keepalive pings
// while an rpc is in progress, so a Watch stream to a server that vanished
// ends rather than waiting forever, and a receive ceiling matching the
// server's own, so a full listing page is accepted. Pair it
// with a server built with [ServerOptions], whose keepalive policy permits the
// pings.
func DialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    keepaliveTime,
			Timeout: keepaliveTimeout,
		}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMessageSize)),
	}
}
