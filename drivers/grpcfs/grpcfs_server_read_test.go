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
	"strings"
	"sync/atomic"
	"testing"

	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
)

// readChunkSize mirrors the unexported constant of the same name in package
// grpcfs: the handler cuts its frames at it, and a test in this package cannot
// see it. A change to that constant is a change to this one.
const readChunkSize = 64 << 10

// maxReadAhead and maxListPageSize mirror the unexported constants of the
// same names in package grpcfs.
const (
	maxReadAhead    = 4 << 20
	maxListPageSize = 10000
)

// TestServerReadFileFrames covers how a streamed read is cut into frames, which
// is the part of ReadFile that a single-frame test cannot see: the frames come
// back in order, each of them as full as the ceiling allows, and a range asks for
// exactly the bytes it names rather than the frame edges it happens to cross.
func TestServerReadFileFrames(t *testing.T) {
	inner := servedFS(t)
	client := serve(t, inner)

	// Not a whole multiple of the frame size, so the last frame is short rather
	// than empty, and the number of frames says something.
	content := bytes.Repeat([]byte("0123456789abcdef"), 3*readChunkSize/16+7)
	ufsdriversTesting.WriteFile(t, inner, "big.bin", string(content))

	t.Run("a_whole_file_arrives_in_frames_of_the_handler_size", func(t *testing.T) {
		frames := readFrames(t, client, &proto.ReadFileRequest{Name: "big.bin"})
		wantFrames := len(content)/readChunkSize + 1
		if len(frames) != wantFrames {
			t.Errorf("%d frames for a %d byte file, want %d at most %d bytes each", len(frames), len(content), wantFrames, readChunkSize)
		}
		for i, frame := range frames[:len(frames)-1] {
			if len(frame) != readChunkSize {
				t.Errorf("frame %d holds %d bytes, want the full %d", i, len(frame), readChunkSize)
			}
		}
		if got := bytes.Join(frames, nil); !bytes.Equal(got, content) {
			t.Errorf("%d bytes came back, want the %d bytes that were written to be them in order", len(got), len(content))
		}
	})

	t.Run("an_offset_and_a_limit_take_the_bytes_they_name", func(t *testing.T) {
		// Deliberately neither aligned to a frame nor a whole number of them, so
		// an off-by-one in either bound shows up in the content rather than in a
		// frame count.
		const offset = readChunkSize - 10
		const limit = readChunkSize + 20
		got := bytes.Join(readFrames(t, client, &proto.ReadFileRequest{Name: "big.bin", Offset: offset, Limit: limit}), nil)
		if want := content[offset : offset+limit]; !bytes.Equal(got, want) {
			t.Errorf("%d bytes at offset %d, want the %d bytes the request names", len(got), offset, limit)
		}
	})

	t.Run("a_limit_longer_than_the_file_stops_at_the_file", func(t *testing.T) {
		got := bytes.Join(readFrames(t, client, &proto.ReadFileRequest{Name: "big.bin", Offset: int64(len(content)) - 5, Limit: 1 << 20}), nil)
		if string(got) != string(content[len(content)-5:]) {
			t.Errorf("the tail read gave %q, want the last five bytes", got)
		}
	})
}

// TestServerWholeFileReadIsGatedBySize says which way a request for a whole file
// is answered: the served file system's own whole-file read while the file is
// under the ceiling the WriteFile rpc comment puts between a file worth sending
// whole and one worth streaming, and a handle above it. Without that gate a
// client asking for a whole file makes the server hold the whole file in one
// slice before the first frame moves, which is what a streamed rpc is supposed to
// avoid.
func TestServerWholeFileReadIsGatedBySize(t *testing.T) {
	inner := &countWriteFS{WriteFS: servedFS(t)}
	client := serve(t, inner)

	const oneMiB = 1 << 20
	// The .proto names 10 MiB as the line, so the two sizes here sit on either
	// side of it with room to spare on both sides.
	ufsdriversTesting.WriteFile(t, inner, "small.bin", strings.Repeat("s", oneMiB))
	if got := readFrames(t, client, &proto.ReadFileRequest{Name: "small.bin"}); len(got) == 0 {
		t.Fatal("no frames")
	}
	if n := inner.wholeReads.Load(); n != 1 {
		t.Errorf("the served file system answered %d whole-file reads, want 1: a file under the ceiling should still be read the way the file system reads a file", n)
	}

	const large = 12 * oneMiB
	ufsdriversTesting.WriteFile(t, inner, "large.bin", strings.Repeat("l", large))
	got := bytes.Join(readFrames(t, client, &proto.ReadFileRequest{Name: "large.bin"}), nil)
	if len(got) != large {
		t.Fatalf("%d bytes came back for a %d byte file", len(got), large)
	}
	if n := inner.wholeReads.Load(); n != 1 {
		t.Errorf("the served file system answered %d whole-file reads, want the small file alone: above the ceiling the content has to move through a handle, a frame at a time", n)
	}
}

// countWriteFS is a served file system that keeps count of the whole-file reads
// its handler takes, which is the only way a test in this package can say which
// of the two ways a read is answered actually answered it.
type countWriteFS struct {
	types.WriteFS
	wholeReads atomic.Int64
}

func (f *countWriteFS) ReadFile(name string) ([]byte, error) {
	f.wholeReads.Add(1)
	return f.WriteFS.ReadFile(name)
}

// readFrames asks for one read and returns the frames the stream gave back.
func readFrames(tb testing.TB, client proto.FSClient, req *proto.ReadFileRequest) [][]byte {
	tb.Helper()

	stream, err := client.ReadFile(tb.Context(), req)
	if err != nil {
		tb.Fatalf("ReadFile: %v", err)
	}
	var frames [][]byte
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return frames
		}
		if err != nil {
			tb.Fatalf("Recv: %v", err)
		}
		frames = append(frames, resp.GetData())
	}
}
