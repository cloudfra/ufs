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

// Package grpcfs serves a ufs file system over the FS service of
// proto/ufs.proto: grpcfs.NewServer answers that service from a file system the
// process already holds, which is how the file system is read and written from
// another process or another machine.
//
// The service maps one rpc onto each method reachable from the types.WriteFS
// interface of drivers/types, so the server is a translation of one interface
// rather than an invention: it calls methods on a types.WriteFS and answers what
// it gets back. That decides what is not on the wire. Close and Release are
// absent because a server cannot decide when every client it serves is done with
// a file system it was handed; IsSame, InnerFS and ExternalPath are absent
// because none of them is a question about a location; and every optional
// interface a file system may or may not carry — Rename, Mkdir, OpenFile,
// BatchCreate, the link and mode-bit setters, Watcher — is absent
// because an rpc for each would make the surface a server offers depend on what
// the file system standing behind it happens to implement. A server therefore
// offers the same rpc surface for every file system it is handed.
package grpcfs

import "google.golang.org/grpc"

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
func ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{grpc.MaxRecvMsgSize(maxMessageSize)}
}
