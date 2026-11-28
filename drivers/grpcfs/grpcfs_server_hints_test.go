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
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"

	"github.com/cloudfra/ufs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc/codes"
)

// TestServerReadFileAnnouncesSize checks that the first frame of a read says
// how many bytes the whole stream carries, for a whole file and for a range,
// and that the frames after it leave the field unset.
func TestServerReadFileAnnouncesSize(t *testing.T) {
	t.Parallel()
	inner := servedFS(t)
	client := serve(t, inner)
	content := bytes.Repeat([]byte("x"), 3*readChunkSize+5)
	ufsdriversTesting.WriteFile(t, inner, "big.bin", string(content))

	for _, tc := range []struct {
		name string
		req  *proto.ReadFileRequest
		want int64
	}{
		{"whole", &proto.ReadFileRequest{Name: "big.bin"}, int64(len(content))},
		{"from_an_offset", &proto.ReadFileRequest{Name: "big.bin", Offset: 10}, int64(len(content) - 10)},
		{"limited", &proto.ReadFileRequest{Name: "big.bin", Offset: 10, Limit: readChunkSize + 1}, readChunkSize + 1},
		{"past_the_end", &proto.ReadFileRequest{Name: "big.bin", Offset: int64(len(content) - 3), Limit: 100}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream, err := client.ReadFile(t.Context(), tc.req)
			if err != nil {
				t.Fatalf("ReadFile = %v", err)
			}
			for i := 0; ; i++ {
				frame, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					return
				}
				if err != nil {
					t.Fatalf("Recv = %v", err)
				}
				want := int64(0)
				if i == 0 {
					want = tc.want
				}
				if frame.GetSize() != want {
					t.Errorf("frame %d announces %d bytes, want %d", i, frame.GetSize(), want)
				}
			}
		})
	}
}

// TestServerRefusesAnnouncedOversizeUpload checks that an upload whose first
// frame announces more than the server accepts is refused on that frame, with
// the code the transport would give, rather than after the rest arrives.
func TestServerRefusesAnnouncedOversizeUpload(t *testing.T) {
	t.Parallel()
	client := serve(t, servedFS(t))
	stream, err := client.WriteFile(t.Context())
	if err != nil {
		t.Fatalf("WriteFile = %v", err)
	}
	if err := stream.Send(&proto.WriteFileRequest{Name: "big.bin", Data: []byte("x"), Size: 33 << 20}); err != nil {
		t.Fatalf("Send = %v", err)
	}
	_, err = stream.CloseAndRecv()
	wantCode(t, err, codes.ResourceExhausted)
}

// TestServerListFilesCapsThePage asks for more children than one page may
// carry and checks that the page stops at the cap and says where to go on.
func TestServerListFilesCapsThePage(t *testing.T) {
	t.Parallel()
	inner := servedFS(t)
	client := serve(t, inner)
	const children = maxListPageSize + 5
	for i := range children {
		ufsdriversTesting.WriteFile(t, inner, "f"+strconv.Itoa(i), "")
	}
	resp, err := client.ListFiles(t.Context(), &proto.ListFilesRequest{Parent: ".", PageSize: 1 << 30})
	if err != nil {
		t.Fatalf("ListFiles = %v", err)
	}
	if len(resp.GetFiles()) != maxListPageSize || resp.GetNextPageToken() == "" {
		t.Errorf("a page asking for 2^30 children holds %d with next token %q, want %d and a token",
			len(resp.GetFiles()), resp.GetNextPageToken(), maxListPageSize)
	}
	entries, err := clientOver(t, inner).ReadDir(".")
	if err != nil || len(entries) != children {
		t.Errorf("ReadDir(.) through the client = %d entries, %v, want %d", len(entries), err, children)
	}
}

// TestServerRewriteIsNeverEmpty rewrites one file through the client from
// several goroutines while others read it, over the nestFS-on-memFS stack
// ufs.New builds. The WriteFile rpc promises a reader never sees the empty
// file that creating it and then writing it would show, so every read must
// return a whole version.
func TestServerRewriteIsNeverEmpty(t *testing.T) {
	t.Parallel()
	inner, err := ufs.New(t.Context(), "memory://")
	if err != nil {
		t.Fatalf("ufs.New(memory://): %v", err)
	}
	t.Cleanup(func() { mustClose(t, inner) })
	client := clientOver(t, inner)
	const version = "a whole version"
	ufsdriversTesting.WriteFile(t, client, "shared.txt", version)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 200 {
				ufsdriversTesting.WriteFile(t, client, "shared.txt", version)
			}
		})
	}
	for range 2 {
		wg.Go(func() {
			buf := make([]byte, 4096)
			for range 400 {
				f, err := client.Open("shared.txt")
				if err != nil {
					t.Errorf("Open(shared.txt) while it is rewritten = %v", err)
					return
				}
				n, err := f.(io.ReaderAt).ReadAt(buf, 0)
				mustClose(t, f)
				if string(buf[:n]) != version || (err != nil && !errors.Is(err, io.EOF)) {
					t.Errorf("read shared.txt while it is rewritten = %q, %v, want %q", buf[:n], err, version)
					return
				}
			}
		})
	}
	wg.Wait()
}
