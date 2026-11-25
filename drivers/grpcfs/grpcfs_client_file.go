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

// grpcFile is the handle [grpcFS.Open] returns for a regular file: [types.File]
// read side, paged over the ReadFile rpc.

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"sync"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
)

var _ types.File = (*grpcFile)(nil)

// grpcFile is a read handle over the ReadFile rpc.
//
// The service carries no file the server holds open, so the position lives here
// and the content comes from a ReadFile stream that starts at it: consecutive
// reads keep pulling from the stream that is already open, and a seek away from
// where that stream has reached gives it up and starts one at the new position.
// What that keeps is the handle's own position; what it gives up is one open file
// behind the handle, so the bytes are whatever the path holds each time a stream
// runs and a write through another client shows up in the next read here. A
// caller that wants the whole file in one call uses [grpcFS.ReadFile], which is
// one round trip rather than a handle plus its streams.
//
// A directory is not a grpcFile: it has no content to stream, so [grpcFS.Open]
// answers one with a [grpcDir] over the ListFiles rpc instead.
type grpcFile struct {
	fsys *grpcFS
	name string

	// info is the metadata the GetFile of the open answered with. It is also the
	// size io.SeekEnd is measured from, which [grpcFile.Stat] refreshes.
	info fs.FileInfo

	mu sync.Mutex
	// pos is where the next read continues from.
	pos int64
	// stream is the ReadFile read that feeds pos and buf the part of its answer
	// that has not been handed over yet. Both are unset before the first read and
	// again after a seek away from pos, which is what makes the next read open a
	// stream at the place the caller moved to.
	stream proto.FS_ReadFileClient
	cancel context.CancelFunc
	buf    []byte
	// done says the stream ended, so every later read reports the end of the file
	// instead of opening another stream at the same place.
	done bool
	// failure is the error that ended a stream. A stream that failed cannot be
	// asked again, so every later call reports that failure rather than waiting
	// for an answer that will never arrive.
	failure error
	closed  bool
}

func newGrpcFile(name string, info fs.FileInfo, fsys *grpcFS) *grpcFile {
	return &grpcFile{fsys: fsys, name: name, info: info}
}

// Read reads up to len(p) bytes from the position the handle has reached.
func (f *grpcFile) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, ufserrors.NewPathError("read", f.name, fs.ErrClosed)
	}
	if f.failure != nil {
		return 0, f.failure
	}
	if len(f.buf) == 0 {
		if err := f.fillLocked(); err != nil {
			return 0, err
		}
		if len(f.buf) == 0 {
			return 0, io.EOF
		}
	}
	n := copy(p, f.buf)
	f.buf = f.buf[n:]
	f.pos += int64(n)
	return n, nil
}

// fillLocked makes sure there is an answer to read from, opening a ReadFile at
// the position the handle reached if there is no stream open there. It is called
// with the mutex held.
func (f *grpcFile) fillLocked() error {
	if f.done {
		return nil
	}
	if f.stream == nil {
		ctx, cancel := context.WithCancel(f.fsys.ctx)
		stream, err := f.fsys.client.ReadFile(ctx, &proto.ReadFileRequest{Name: f.name, Offset: f.pos})
		if err != nil {
			cancel()
			f.failure = pathError("read", f.name, err)
			return f.failure
		}
		f.stream, f.cancel = stream, cancel
	}
	for {
		frame, err := f.stream.Recv()
		if errors.Is(err, io.EOF) {
			// The end of the stream is the end of the range the request asked for,
			// which for a read to the end of the file is the answer io.Reader asks
			// for there. A frame that carried no bytes is skipped rather than
			// answered as an end, because a short frame is allowed to be short.
			f.done = true
			return nil
		}
		if err != nil {
			f.failure = pathError("read", f.name, err)
			return f.failure
		}
		if len(frame.GetData()) > 0 {
			f.buf = frame.GetData()
			return nil
		}
	}
}

// Write is refused: [grpcFS.Open] hands out a read-only handle, which is all
// [fs.FS.Open] promises. Content goes into a file through the handle of
// [grpcFS.Create].
func (f *grpcFile) Write([]byte) (int, error) {
	return 0, ufserrors.NewPathError("write", f.name, fs.ErrInvalid)
}

// WriteString is refused for the same reason Write is.
func (f *grpcFile) WriteString(string) (int, error) {
	return 0, ufserrors.NewPathError("write", f.name, fs.ErrInvalid)
}

// Seek moves the position the handle reads from.
func (f *grpcFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, ufserrors.NewPathError("seek", f.name, fs.ErrClosed)
	}
	if f.failure != nil {
		return 0, f.failure
	}
	var pos int64
	switch whence {
	case io.SeekStart:
		pos = offset
	case io.SeekCurrent:
		pos = f.pos + offset
	case io.SeekEnd:
		pos = f.info.Size() + offset
	default:
		return 0, ufserrors.NewPathError("seek", f.name, fs.ErrInvalid)
	}
	if pos < 0 {
		return 0, ufserrors.NewPathError("seek", f.name, fs.ErrInvalid)
	}
	if pos != f.pos {
		// The stream is left where it was, so moving means giving it up and
		// letting the next read ask again from the place the caller chose.
		f.dropStreamLocked()
		f.pos = pos
		f.done = false
	}
	return pos, nil
}

// dropStreamLocked gives up the ReadFile read that feeds the handle, so that the
// next one starts fresh. It is called with the mutex held. Closing a gRPC stream
// of this shape has no answer to report: what is given up is the call's own
// context.
func (f *grpcFile) dropStreamLocked() {
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
	f.stream, f.buf = nil, nil
}

// ReadAt reads from an absolute offset without moving the position the handle
// reads at, which the service makes the cheap case rather than the awkward one:
// the request carries the offset, so the read gets a stream of its own instead of
// moving a shared position and putting it back.
func (f *grpcFile) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, ufserrors.NewPathError("readat", f.name, fs.ErrClosed)
	}
	if f.failure != nil {
		return 0, f.failure
	}
	data, err := f.fsys.readRange("readat", f.name, off, int64(len(p)))
	if err != nil {
		return 0, err
	}
	n := copy(p, data)
	if n < len(p) {
		// The rpc answered with everything the range held, which is less than the
		// caller asked for only at the end of the file — and [io.ReaderAt] reports
		// that with the bytes it did read.
		return n, io.EOF
	}
	return n, nil
}

// Stat reports the node the handle was opened on.
//
// The metadata of a handle is not on the wire — the service carries the metadata
// of a path — so this is a fresh lookup of the name rather than a question about
// an open file, which is also what keeps the size io.SeekEnd is measured from
// from going stale under a handle that is held across a write.
func (f *grpcFile) Stat() (fs.FileInfo, error) {
	info, err := f.fsys.stat("stat", f.name, proto.SymlinkHandling_SYMLINK_HANDLING_FOLLOW)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.info = info
	f.mu.Unlock()
	return info, nil
}

// Close ends the handle. Closing a handle twice is not an error, and there is
// nothing to report either way: the handle holds no file on the server, only the
// stream of a call that is still running.
func (f *grpcFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	f.dropStreamLocked()
	return nil
}
