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
	"net"
	"testing"

	"github.com/cloudfra/ufs/drivers/core"
	"github.com/cloudfra/ufs/drivers/grpcfs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

// serve starts a gRPC server for inner on an in-memory listener and returns a
// client talking to it, so a test exercises the whole path a remote client
// takes — frames, statuses and the mapping between the two — without a port, a
// second process, or a network.
func serve(tb testing.TB, inner types.WriteFS) proto.FSClient {
	tb.Helper()

	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	proto.RegisterFSServer(grpcServer, grpcfs.NewServer(inner))
	go func() {
		if err := grpcServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			tb.Errorf("Serve: %v", err)
		}
	}()
	// GracefulStop rather than Stop: a read handler holds the file it streams
	// open until it unwinds, and Stop returns without waiting for that. A busy
	// file is as hard for Windows to remove as a busy directory, so a cleanup
	// that went ahead while a handler was still sending would fail over the
	// served tree. The client connection is closed before this cleanup runs —
	// t.Cleanup is last-in-first-out — so a handler still sending to a caller
	// that has gone away is canceled rather than awaited.
	tb.Cleanup(grpcServer.GracefulStop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		tb.Fatalf("grpc.NewClient: %v", err)
	}
	tb.Cleanup(func() { mustClose(tb, conn) })

	return proto.NewFSClient(conn)
}

// servedFS returns a memory file system for a server to serve. memFS answers
// every method of types.WriteFS, which is all a Server asks of the file system
// it is handed: the service carries no rpc for anything an optional interface
// would add, so there is nothing for a test fixture to put back the way a wider
// inner used to need.
func servedFS(tb testing.TB) types.WriteFS {
	tb.Helper()
	return core.MakeMemFS("memory://test")
}

func mustClose(tb testing.TB, c interface{ Close() error }) {
	tb.Helper()
	if err := c.Close(); err != nil {
		tb.Errorf("Close: %v", err)
	}
}

// wantCode fails unless err carries the status code the service documents for
// the failure.
func wantCode(tb testing.TB, err error, want codes.Code) {
	tb.Helper()
	if err == nil {
		tb.Fatalf("got nil, want a %s error", want)
	}
	if got := status.Code(err); got != want {
		tb.Fatalf("got %s (%v), want %s", got, err, want)
	}
}

// upload sends a whole file through the client-streaming WriteFile rpc, which is
// how a test changes the served tree the way any client would.
func upload(ctx context.Context, tb testing.TB, client proto.FSClient, name, content string) {
	tb.Helper()
	_, err := writeWhole(ctx, client, name, content)
	if err != nil {
		tb.Fatalf("WriteFile of %q: %v", name, err)
	}
}

// writeWhole uploads one file and returns what the rpc answered, for the cases
// that are about the answer rather than the file.
func writeWhole(ctx context.Context, client proto.FSClient, name, content string) (*emptypb.Empty, error) {
	stream, err := client.WriteFile(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(&proto.WriteFileRequest{Name: name, Mode: uint32(0o644), Data: []byte(content)}); err != nil {
		return nil, err
	}
	return stream.CloseAndRecv()
}

// TestServerGetFile covers the metadata lookup, including the one field of the
// File resource that is not a method call: the name the caller asked for.
func TestServerGetFile(t *testing.T) {
	ctx := t.Context()
	inner := servedFS(t)
	client := serve(t, inner)

	if err := inner.MkdirAll("dir", 0o755); err != nil {
		t.Fatal(err)
	}
	ufsdriversTesting.WriteFile(t, inner, "dir/file.txt", "hello world")

	t.Run("a_file_answers_with_the_path_it_was_asked_for", func(t *testing.T) {
		got, err := client.GetFile(ctx, &proto.GetFileRequest{Name: "dir/file.txt"})
		if err != nil {
			t.Fatal(err)
		}
		if got.GetName() != "dir/file.txt" {
			t.Errorf("Name = %q, want the whole path the request named rather than %q", got.GetName(), "file.txt")
		}
		if got.GetSize() != int64(len("hello world")) {
			t.Errorf("Size = %d, want %d", got.GetSize(), len("hello world"))
		}
		if got.GetIsDir() {
			t.Error("IsDir = true, want false for a file")
		}
		if got.GetModTime() == nil {
			t.Error("ModTime is unset, want a time")
		}
	})

	t.Run("the_root_is_a_directory_named_dot", func(t *testing.T) {
		got, err := client.GetFile(ctx, &proto.GetFileRequest{Name: "."})
		if err != nil {
			t.Fatal(err)
		}
		if !got.GetIsDir() {
			t.Error("IsDir = false, want true for the root")
		}
	})

	t.Run("a_missing_path_reports_NotFound", func(t *testing.T) {
		_, err := client.GetFile(ctx, &proto.GetFileRequest{Name: "dir/nope.txt"})
		wantCode(t, err, codes.NotFound)
	})
}

// TestServerListFiles covers the listing and the pagination the request asks
// for, which the service leaves to the server to shape.
func TestServerListFiles(t *testing.T) {
	ctx := t.Context()
	inner := servedFS(t)
	client := serve(t, inner)

	for _, name := range []string{"one", "two", "three", "four", "five"} {
		ufsdriversTesting.WriteFile(t, inner, name, name)
	}

	t.Run("pages_walk_the_whole_directory", func(t *testing.T) {
		var names, token []string
		next := ""
		for pages := 0; ; pages++ {
			if pages == 10 {
				t.Fatal("ListFiles kept handing out a page token")
			}
			resp, err := client.ListFiles(ctx, &proto.ListFilesRequest{Parent: ".", PageSize: 2, PageToken: next})
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range resp.GetFiles() {
				names = append(names, f.GetName())
			}
			token = append(token, resp.GetNextPageToken())
			if resp.GetNextPageToken() == "" {
				break
			}
			next = resp.GetNextPageToken()
		}
		want := []string{"five", "four", "one", "three", "two"}
		if len(names) != len(want) {
			t.Fatalf("listed %v, want every child once: %v", names, want)
		}
		for i := range want {
			if names[i] != want[i] {
				t.Fatalf("listed %v, want %v in ReadDir's order", names, want)
			}
		}
		if token[len(token)-1] != "" {
			t.Errorf("the last page's token = %q, want empty to end the walk", token[len(token)-1])
		}
	})

	t.Run("no_page_size_answers_the_whole_directory", func(t *testing.T) {
		resp, err := client.ListFiles(ctx, &proto.ListFilesRequest{Parent: "."})
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.GetFiles()) != 5 {
			t.Errorf("listed %d children, want all 5 at once", len(resp.GetFiles()))
		}
		if resp.GetNextPageToken() != "" {
			t.Errorf("NextPageToken = %q, want empty when nothing is left", resp.GetNextPageToken())
		}
	})

	t.Run("a_page_token_that_is_not_an_offset_is_refused", func(t *testing.T) {
		_, err := client.ListFiles(ctx, &proto.ListFilesRequest{Parent: ".", PageToken: "not-a-number"})
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("a_missing_directory_reports_NotFound", func(t *testing.T) {
		_, err := client.ListFiles(ctx, &proto.ListFilesRequest{Parent: "nope"})
		wantCode(t, err, codes.NotFound)
	})
}

// TestServerReadFile covers the whole-file read and the range the request can
// ask for, both of which arrive as a stream of frames.
func TestServerReadFile(t *testing.T) {
	ctx := t.Context()
	inner := servedFS(t)
	client := serve(t, inner)

	const content = "the quick brown fox jumps over the lazy dog"
	ufsdriversTesting.WriteFile(t, inner, "fox.txt", content)

	read := func(t *testing.T, req *proto.ReadFileRequest) string {
		t.Helper()
		stream, err := client.ReadFile(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		var out []byte
		for {
			frame, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return string(out)
			}
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, frame.GetData()...)
		}
	}

	t.Run("the_whole_file_arrives_whole", func(t *testing.T) {
		if got := read(t, &proto.ReadFileRequest{Name: "fox.txt"}); got != content {
			t.Errorf("read %q, want the whole content", got)
		}
	})

	t.Run("an_offset_starts_partway_in", func(t *testing.T) {
		if got := read(t, &proto.ReadFileRequest{Name: "fox.txt", Offset: 10}); got != content[10:] {
			t.Errorf("read %q, want the content from byte 10", got)
		}
	})

	t.Run("a_limit_stops_short_of_the_end", func(t *testing.T) {
		if got := read(t, &proto.ReadFileRequest{Name: "fox.txt", Limit: 9}); got != content[:9] {
			t.Errorf("read %q, want the first 9 bytes", got)
		}
	})

	t.Run("an_offset_and_a_limit_together_name_one_span", func(t *testing.T) {
		got := read(t, &proto.ReadFileRequest{Name: "fox.txt", Offset: 10, Limit: 5})
		if got != content[10:15] {
			t.Errorf("read %q, want the 5 bytes from 10", got)
		}
	})

	t.Run("a_range_past_the_end_answers_nothing", func(t *testing.T) {
		if got := read(t, &proto.ReadFileRequest{Name: "fox.txt", Offset: int64(len(content))}); got != "" {
			t.Errorf("read %q, want nothing", got)
		}
	})

	t.Run("a_missing_file_reports_NotFound", func(t *testing.T) {
		stream, err := client.ReadFile(ctx, &proto.ReadFileRequest{Name: "nope.txt"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = stream.Recv()
		wantCode(t, err, codes.NotFound)
	})
}

// TestServerGetMetadata covers the one rpc that answers three Go methods, which
// is the whole point of the response existing.
func TestServerGetMetadata(t *testing.T) {
	ctx := t.Context()
	inner := servedFS(t)
	client := serve(t, inner)

	got, err := client.GetMetadata(ctx, &proto.GetMetadataRequest{})
	if err != nil {
		t.Fatal(err)
	}

	uri, err := inner.URI()
	if err != nil {
		t.Fatal(err)
	}
	if got.GetUri() != uri.String() {
		t.Errorf("Uri = %q, want what URI reports: %q", got.GetUri(), uri.String())
	}
	if got.GetDescription() != inner.String() {
		t.Errorf("Description = %q, want what String reports: %q", got.GetDescription(), inner.String())
	}
	devices := inner.DeviceInfo()
	if len(got.GetDevices()) != len(devices) {
		t.Fatalf("devices = %v, want the map DeviceInfo reports: %v", got.GetDevices(), devices)
	}
	for name, info := range devices {
		gotInfo := got.GetDevices()[name]
		if gotInfo == nil {
			t.Fatalf("devices is missing %q", name)
		}
		if gotInfo.GetName() != info.Name() || gotInfo.GetDeviceType() != info.DeviceType() ||
			int(gotInfo.GetThreadCount()) != info.ThreadCount() {
			t.Errorf("devices[%q] = %v, want name %q, type %q and %d threads",
				name, gotInfo, info.Name(), info.DeviceType(), info.ThreadCount())
		}
	}
}
