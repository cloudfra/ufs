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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/proto"
)

// The ceilings this package sets and the one content copy its upload handler
// keeps are chosen from the measurements below rather than from grpc's defaults.
// Run them with:
//
//	go test -run '^$' -bench 'BenchmarkUpload|BenchmarkRead' -benchmem ./drivers/grpcfs/
//
// They go over the in-process bufconn listener the tests use, with no port and no
// network, so what they report is the price of the handlers' own allocations and
// framing rather than the price of moving bytes between machines. The allocation
// numbers are the ones the ceilings are set from: they are what one request costs
// a server, and so what one client asking for something large can make it pay.

// benchUploadCeiling mirrors the unexported ceiling of the same size in package
// grpcfs, which a test in this package cannot see. A change to that one is a
// change to this one.
const benchUploadCeiling = 32 << 20

// BenchmarkUploadFrameSize measures what the frame size a client picks costs the
// server for the same content. One frame is written out of the frame it arrived
// in; several frames have to be joined before the file system is handed one
// slice, and that join is the only content-sized allocation the upload handler
// makes. So the copy the scan of that handler leaves in place has a measured
// price, and the way to pay nothing for it is to upload in one frame.
func BenchmarkUploadFrameSize(b *testing.B) {
	ctx := b.Context()

	// Far enough above the frame size that the difference between one frame and
	// many is visible, and well inside the ceiling the handler puts on an upload.
	const total = 8 << 20

	for _, frameSize := range []int{total, 1 << 20, 64 << 10} {
		b.Run(fmt.Sprintf("%dKiBFrame", frameSize>>10), func(b *testing.B) {
			client := serve(b, servedFS(b))
			payload := bytes.Repeat([]byte("x"), frameSize)
			b.ReportAllocs()
			b.SetBytes(total)
			for b.Loop() {
				stream, err := client.WriteFile(ctx)
				if err != nil {
					b.Fatal(err)
				}
				// Every frame of one upload names the same file; the handler reads
				// the name and the mode from the first frame only.
				req := &proto.WriteFileRequest{Name: "bench.bin", Mode: uint32(0o644)}
				for sent := 0; sent < total; sent += frameSize {
					req.Data = payload[:min(frameSize, total-sent)]
					if err := stream.Send(req); err != nil {
						b.Fatalf("frame %d of %d: %v", sent/frameSize, total/frameSize, err)
					}
				}
				if _, err := stream.CloseAndRecv(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkUploadSize measures uploads from the size of a small file to the size
// that sits over the ceiling the handler puts on one, each framed at one MiB or
// the file itself when it is smaller. An upload is held whole before the file
// system sees it, so its cost grows with the file, and the ceiling is what bounds
// what one request can make a server hold. The size over the ceiling is measured
// too, because refusing partway through is meant to cost less than writing the
// whole thing.
func BenchmarkUploadSize(b *testing.B) {
	ctx := b.Context()

	for _, size := range []int{4 << 10, 64 << 10, 1 << 20, 8 << 20, 30 << 20, 33 << 20} {
		b.Run(benchSizeName(size), func(b *testing.B) {
			client := serve(b, servedFS(b))
			frameSize := min(size, 1<<20)
			payload := bytes.Repeat([]byte("x"), frameSize)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				stream, err := client.WriteFile(ctx)
				if err != nil {
					b.Fatal(err)
				}
				req := &proto.WriteFileRequest{Name: "bench.bin", Mode: uint32(0o644)}
				for sent := 0; sent < size; sent += frameSize {
					req.Data = payload[:min(frameSize, size-sent)]
					if err := stream.Send(req); err != nil {
						b.Fatalf("frame %d of %d: %v", sent/frameSize, size/frameSize, err)
					}
				}
				if _, err := stream.CloseAndRecv(); err != nil {
					if size > benchUploadCeiling {
						// Over the ceiling the request is refused, which is the
						// answer being measured here rather than a failure.
						continue
					}
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkReadWholeVersusStreamed measures a whole-file read on either side of
// the size gate the read handler applies: below it the handler asks the served
// file system for the whole content in one slice, above it the content moves
// through a handle a chunk at a time. That gate is what the ceiling between the
// two is for, and the difference in allocation between them is what puts the
// ceiling where it is instead of at grpc's own default message size.
func BenchmarkReadWholeVersusStreamed(b *testing.B) {
	ctx := b.Context()

	// On either side of the 10 MiB line the WriteFile rpc comment draws, with a
	// small file as the control.
	for _, size := range []int{1 << 20, 9 << 20, 11 << 20, 32 << 20} {
		b.Run(benchSizeName(size), func(b *testing.B) {
			inner := servedFS(b)
			ufsdriversTesting.WriteFile(b, inner, "bench.bin", strings.Repeat("x", size))
			client := serve(b, inner)
			req := &proto.ReadFileRequest{Name: "bench.bin"}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				stream, err := client.ReadFile(ctx, req)
				if err != nil {
					b.Fatal(err)
				}
				var read int
				for {
					resp, err := stream.Recv()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						b.Fatal(err)
					}
					read += len(resp.GetData())
				}
				if read != size {
					b.Fatalf("the read gave %d bytes, want the %d that were written", read, size)
				}
			}
		})
	}
}

// BenchmarkReadFirstFrame measures how long a client waits for the first frame of
// a whole-file read, which is where the size gate shows up in time rather than in
// bytes: below the ceiling the server reads all of the file before it has anything
// to send, above it the first frame is one chunk behind opening the handle. A
// client that reads a large file and acts on what arrives first pays that wait
// whether or not it ever looks at the rest, so the ceiling is also the point where
// the wait stops growing with the file.
func BenchmarkReadFirstFrame(b *testing.B) {
	for _, size := range []int{1 << 20, 9 << 20, 11 << 20, 32 << 20} {
		b.Run(benchSizeName(size), func(b *testing.B) {
			inner := servedFS(b)
			ufsdriversTesting.WriteFile(b, inner, "bench.bin", strings.Repeat("x", size))
			client := serve(b, inner)
			req := &proto.ReadFileRequest{Name: "bench.bin"}
			for b.Loop() {
				// Canceled as soon as the first frame is in hand, so the rest of
				// the file never moves and the measurement stays about the wait.
				ctx, cancel := context.WithCancel(b.Context())
				stream, err := client.ReadFile(ctx, req)
				if err != nil {
					cancel()
					b.Fatal(err)
				}
				resp, err := stream.Recv()
				cancel()
				if err != nil {
					b.Fatal(err)
				}
				if len(resp.GetData()) == 0 {
					b.Fatal("an empty first frame")
				}
			}
		})
	}
}

// benchSizeName names a size for a sub-benchmark, in KiB below a MiB and in MiB
// above, so a table of sizes reads in the output without a wall of digits.
func benchSizeName(size int) string {
	if size < 1<<20 {
		return fmt.Sprintf("%dKiB", size>>10)
	}
	return fmt.Sprintf("%dMiB", size>>20)
}

// BenchmarkClientReadFile measures a whole-file read through the client,
// which collects every frame of the stream into one slice.
func BenchmarkClientReadFile(b *testing.B) {
	for _, size := range []int{64 << 10, 1 << 20, 8 << 20} {
		b.Run(benchSizeName(size), func(b *testing.B) {
			inner := servedFS(b)
			ufsdriversTesting.WriteFile(b, inner, "f.bin", string(bytes.Repeat([]byte("x"), size)))
			client := clientOver(b, inner)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := client.ReadFile("f.bin"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
