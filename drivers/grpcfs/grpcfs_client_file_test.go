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
	"io"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs/drivers/grpcfs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
)

// rangeRecorder wraps a service client and writes down every ranged read the
// driver asks for, so that a test can say how much of a read was an rpc and how
// much was the handle answering from a range it fetched already.
type rangeRecorder struct {
	proto.FSClient
	ranges *[]rangeAsk
}

// rangeAsk is a ranged read as a plain value, which is what lets a test name the
// reads it expects and compare them: the request a read travels in is a protobuf
// message, and those are not values to copy or compare.
type rangeAsk struct {
	Name   string
	Offset int64
	Limit  int64
}

func (r rangeRecorder) ReadFile(ctx context.Context, in *proto.ReadFileRequest, opts ...grpc.CallOption) (proto.FS_ReadFileClient, error) {
	*r.ranges = append(*r.ranges, rangeAsk{Name: in.GetName(), Offset: in.GetOffset(), Limit: in.GetLimit()})
	return r.FSClient.ReadFile(ctx, in, opts...)
}

// recordingClient is the served client of [servedClient] with the range recorder
// of [rangeRecorder] in between, giving a test both the file system to call and
// the list of ranged reads its handles asked the service for.
func recordingClient(tb testing.TB) (types.WriteFS, *[]rangeAsk) {
	tb.Helper()

	var ranges []rangeAsk
	client := rangeRecorder{FSClient: serve(tb, servedFS(tb)), ranges: &ranges}
	fsys, err := grpcfs.NewWithClient(tb.Context(), client, servedClientURI())
	if err != nil {
		tb.Fatalf("NewWithClient: %v", err)
	}
	tb.Cleanup(func() { mustClose(tb, fsys) })
	return fsys, &ranges
}

// readHandleAt opens name on fsys and answers the read side of the handle, which
// every test here aims at with [io.ReaderAt] rather than at a position.
func readHandleAt(tb testing.TB, fsys types.ReadFS, name string) io.ReaderAt {
	tb.Helper()

	f, err := fsys.Open(name)
	if err != nil {
		tb.Fatalf("Open(%q): %v", name, err)
	}
	tb.Cleanup(func() { mustClose(tb, f) })
	at, ok := f.(io.ReaderAt)
	if !ok {
		tb.Fatalf("Open(%q) answered a %T, which is not an io.ReaderAt", name, f)
	}
	return at
}

// TestClientFileReadAtRunSharesOneRange is the reason a ranged read asks for more
// than it is asked for: the shared conformance suite walks a file a byte at a
// time with [io.ReaderAt], which is one round trip per byte if every call asks
// the service for its own bytes. A run of them costs one read for the first byte,
// which nothing has said will continue, and one per window after it.
func TestClientFileReadAtRunSharesOneRange(t *testing.T) {
	fsys, ranges := recordingClient(t)

	// A file a window and a half long, so the run needs a second window and stops
	// at the end of the file rather than at the end of a window.
	content := bytes.Repeat([]byte("0123456789abcdef"), readChunkSize/16+readChunkSize/32+3)
	ufsdriversTesting.WriteFile(t, fsys, "run.bin", string(content))

	at := readHandleAt(t, fsys, "run.bin")
	var one [1]byte
	for i := range len(content) {
		if _, err := at.ReadAt(one[:], int64(i)); err != nil {
			t.Fatalf("ReadAt at %d = %v, want the byte that is there", i, err)
		}
		if one[0] != content[i] {
			t.Fatalf("ReadAt at %d read %q, want %q", i, one[:], content[i:i+1])
		}
	}

	// The first byte, the window it opens, the window that follows it, and the
	// rest of the file at the end of that one.
	if got := len(*ranges); got < 2 || got > 4 {
		t.Errorf("%d ranged reads answered %d one-byte reads, want one per %d-byte window of the file rather than one per read",
			got, len(content), readChunkSize)
	}
	for _, r := range *ranges {
		if r.Limit < readChunkSize && r.Limit != 1 {
			t.Errorf("a ranged read asked for %d bytes at %d, less than a window and more than the one byte that starts the run",
				r.Limit, r.Offset)
		}
	}
}

// TestClientFileReadAtAsksForWhatItNeeds covers the other side of the same
// choice: a read aimed somewhere the handle has not read is not treated as the
// start of a run, so it takes exactly its own bytes rather than a window of the
// file it happens to land in.
func TestClientFileReadAtAsksForWhatItNeeds(t *testing.T) {
	fsys, ranges := recordingClient(t)

	content := bytes.Repeat([]byte("abcdefghijklmnop"), 4096)
	ufsdriversTesting.WriteFile(t, fsys, "jump.bin", string(content))
	at := readHandleAt(t, fsys, "jump.bin")

	// The two that have to go to the service, and the one that must not: 22..27
	// sits inside the 20..30 the handle already holds.
	for _, ask := range []struct {
		buf int
		off int64
	}{
		{buf: 100, off: 5000},
		{buf: 10, off: 20},
		{buf: 5, off: 22},
	} {
		if _, err := at.ReadAt(make([]byte, ask.buf), ask.off); err != nil {
			t.Fatalf("ReadAt %d bytes at %d = %v", ask.buf, ask.off, err)
		}
	}

	want := []rangeAsk{
		{Name: "jump.bin", Offset: 5000, Limit: 100},
		{Name: "jump.bin", Offset: 20, Limit: 10},
	}
	if diff := cmp.Diff(want, *ranges); diff != "" {
		t.Errorf("ranged reads the handle asked for (+want -got):\n%s", diff)
	}
}

// TestClientFileReadAtSeekGivesUpTheRange pins what a seek costs a reader that
// uses [io.ReaderAt]: the seek gives up what the handle fetched, so the next
// ranged read looks at the file again rather than at the answer it held.
func TestClientFileReadAtSeekGivesUpTheRange(t *testing.T) {
	fsys, ranges := recordingClient(t)

	ufsdriversTesting.WriteFile(t, fsys, "seek.bin", string(bytes.Repeat([]byte("z"), 4096)))
	f, err := fsys.Open("seek.bin")
	if err != nil {
		t.Fatalf("Open(seek.bin): %v", err)
	}
	defer mustClose(t, f)
	at, ok := f.(io.ReaderAt)
	if !ok {
		t.Fatalf("Open(seek.bin) answered a %T, which is not an io.ReaderAt", f)
	}
	seeker, ok := f.(io.Seeker)
	if !ok {
		t.Fatalf("Open(seek.bin) answered a %T, which is not an io.Seeker", f)
	}

	if _, err := at.ReadAt(make([]byte, 8), 1024); err != nil {
		t.Fatalf("ReadAt at 1024 = %v", err)
	}
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("Seek(0) = %v", err)
	}
	if _, err := at.ReadAt(make([]byte, 8), 1024); err != nil {
		t.Fatalf("ReadAt at 1024 after the seek = %v", err)
	}

	want := []rangeAsk{
		{Name: "seek.bin", Offset: 1024, Limit: 8},
		{Name: "seek.bin", Offset: 1024, Limit: 8},
	}
	if diff := cmp.Diff(want, *ranges); diff != "" {
		t.Errorf("ranged reads across a seek (+want -got):\n%s", diff)
	}
}

// TestClientFileReadAtNewRangeSeesAWrite names the freshness a held range gives
// up, which is the same thing a [grpcFile.Read] stream gave up before it: the
// handle is not a window onto the file, so a write through another client shows
// up in the next range the handle fetches rather than in the next byte it reads.
func TestClientFileReadAtNewRangeSeesAWrite(t *testing.T) {
	fsys, _ := recordingClient(t)

	ufsdriversTesting.WriteFile(t, fsys, "shared.txt", string(bytes.Repeat([]byte("a"), 4096)))
	f, err := fsys.Open("shared.txt")
	if err != nil {
		t.Fatalf("Open(shared.txt): %v", err)
	}
	defer mustClose(t, f)
	at, ok := f.(io.ReaderAt)
	if !ok {
		t.Fatalf("Open(shared.txt) answered a %T, which is not an io.ReaderAt", f)
	}
	seeker := f.(io.Seeker)

	buf := make([]byte, 16)
	if _, err := at.ReadAt(buf, 0); err != nil {
		t.Fatalf("ReadAt at 0 = %v", err)
	}
	if want := string(bytes.Repeat([]byte("a"), 16)); string(buf) != want {
		t.Fatalf("ReadAt before the write read %q, want %q", buf, want)
	}

	ufsdriversTesting.WriteFile(t, fsys, "shared.txt", string(bytes.Repeat([]byte("b"), 4096)))
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("Seek(0) = %v", err)
	}
	clear(buf)
	if _, err := at.ReadAt(buf, 0); err != nil {
		t.Fatalf("ReadAt at 0 after the write = %v", err)
	}
	if want := string(bytes.Repeat([]byte("b"), 16)); string(buf) != want {
		t.Errorf("ReadAt after a write through another handle read %q, want %q", buf, want)
	}
}

// TestClientFileReadAtEdges covers the reads a held range answers worst: the one
// that runs off the end of the file, the one aimed at the end itself, which
// leaves a range that holds nothing and says so only by having been asked, and
// the ones that never reach the service at all.
func TestClientFileReadAtEdges(t *testing.T) {
	fsys, ranges := recordingClient(t)

	content := bytes.Repeat([]byte("0123456789"), 512)
	ufsdriversTesting.WriteFile(t, fsys, "edge.txt", string(content))
	end := int64(len(content))

	t.Run("a_read_over_the_end_reports_the_end", func(t *testing.T) {
		at := readHandleAt(t, fsys, "edge.txt")
		buf := make([]byte, 4096)
		n, err := at.ReadAt(buf, end-100)
		if n != 100 || err != io.EOF {
			t.Errorf("ReadAt 4096 bytes at %d = %d, %v, want 100 and %v", end-100, n, err, io.EOF)
		}
		if got, want := string(buf[:n]), string(content[end-100:]); got != want {
			t.Errorf("the %d bytes it did read are %q, want the last %d bytes of the file", n, got, 100)
		}
	})

	t.Run("a_read_at_the_end_reports_it_twice", func(t *testing.T) {
		at := readHandleAt(t, fsys, "edge.txt")
		for range 2 {
			n, err := at.ReadAt(make([]byte, 16), end)
			if n != 0 || err != io.EOF {
				t.Fatalf("ReadAt 16 bytes at the end of the file = %d, %v, want 0 and %v", n, err, io.EOF)
			}
		}
	})

	t.Run("a_negative_offset_is_an_error", func(t *testing.T) {
		at := readHandleAt(t, fsys, "edge.txt")
		if n, err := at.ReadAt(make([]byte, 4), -1); n != 0 || err == nil {
			t.Errorf("ReadAt at -1 = %d, %v, want 0 and an error", n, err)
		}
	})

	t.Run("an_empty_read_never_asks", func(t *testing.T) {
		before := len(*ranges)
		at := readHandleAt(t, fsys, "edge.txt")
		if n, err := at.ReadAt(nil, 0); n != 0 || err != nil {
			t.Errorf("ReadAt with nothing to fill = %d, %v, want 0 and nil", n, err)
		}
		if len(*ranges) != before {
			t.Errorf("an empty read asked the service for %d ranges", len(*ranges)-before)
		}
	})

	t.Run("a_closed_handle_reports_that_it_is_closed", func(t *testing.T) {
		f, err := fsys.Open("edge.txt")
		if err != nil {
			t.Fatalf("Open(edge.txt): %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if n, err := f.(io.ReaderAt).ReadAt(make([]byte, 4), 0); n != 0 || !errors.Is(err, fs.ErrClosed) {
			t.Errorf("ReadAt after Close = %d, %v, want 0 and %v", n, err, fs.ErrClosed)
		}
	})
}

// TestClientFileReadAtWindowGrows walks a large file forward in reads the size
// a mount asks for, and checks that the ranges the handle fetches double from
// one window up to the read-ahead ceiling, so the walk costs a handful of
// round trips rather than one per read.
func TestClientFileReadAtWindowGrows(t *testing.T) {
	fsys, ranges := recordingClient(t)
	const size = 12 << 20
	content := bytes.Repeat([]byte("0123456789abcdef"), size/16)
	ufsdriversTesting.WriteFile(t, fsys, "large.bin", string(content))

	at := readHandleAt(t, fsys, "large.bin")
	buf := make([]byte, 128<<10)
	for off := 0; off < size; off += len(buf) {
		n, err := at.ReadAt(buf, int64(off))
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("ReadAt at %d = %v", off, err)
		}
		if !bytes.Equal(buf[:n], content[off:off+n]) {
			t.Fatalf("ReadAt at %d returned the wrong bytes", off)
		}
	}
	reads := size / len(buf)
	if got := len(*ranges); got > 12 {
		t.Errorf("%d ranged reads for %d sequential reads of %d bytes, want the window to grow so there are at most 12", got, reads, len(buf))
	}
	var largest int64
	for _, r := range *ranges {
		largest = max(largest, r.Limit)
	}
	if largest != maxReadAhead {
		t.Errorf("the largest range asked for was %d bytes, want the %d byte read-ahead ceiling", largest, maxReadAhead)
	}
}
