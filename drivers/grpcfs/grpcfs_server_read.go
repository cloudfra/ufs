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
	"errors"
	"io"
	"io/fs"
	"sync"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
)

// ReadFile streams the content of the file at name, in as many frames as it takes.
//
// A request for a whole file goes through the file system's own whole-file read
// only when the file is small enough for that to be a bounded read; anything larger
// is read through a handle, a frame at a time.
func (s *Server) ReadFile(req *proto.ReadFileRequest, stream proto.FS_ReadFileServer) error {
	if !s.life.Enter() {
		return errShutdown
	}
	defer s.life.Exit()
	if req.GetOffset() != 0 || req.GetLimit() != 0 || !s.wholeFileFits(req.GetName()) {
		return s.sendRange(req, stream)
	}
	// The whole file, which is what [types.ReadFS.ReadFile] answers, so a file
	// system with a native whole-file read does not have to open a handle to serve
	// it.
	data, err := s.inner.ReadFile(req.GetName())
	if err != nil {
		return toStatus(err)
	}
	return sendContent(stream, data)
}

// wholeFileFits says whether the file at name is small enough that reading it in
// one call is a bounded read. A request for a whole file asks for as much of it as
// exists, so the file decides what the answer costs the server: a whole-file read
// hands back all of it in one slice, which on a file system that keeps content in
// memory or fetches it in one go is the whole file at once. Above
// [maxMessageSize] — the ceiling the WriteFile rpc comment already puts between a
// file worth sending whole and one worth streaming — the content goes through a
// handle instead, where it moves a frame at a time and the peak stays at a frame.
//
// A name that cannot be looked up at all fits nothing, which leaves the read
// itself to report what is wrong with the path.
//
// Whether the handle path is cheaper than the whole read is the driver's business
// rather than this one's: memFS, the file system these tests serve, clones a
// file's content when a handle is opened on it, so on that file system the two
// paths cost the same and the gate only decides which of the driver's own copies
// is taken. What the handler guarantees is its own share: above the ceiling it
// holds one [readChunkSize] buffer, whatever size the file turns out to be.
func (s *Server) wholeFileFits(name string) bool {
	info, err := s.inner.Stat(name)
	if err != nil {
		return false
	}
	return info.Size() <= maxMessageSize
}

// sendRange answers a read that starts partway into a file or stops before its
// end, which is also how a whole file larger than [maxMessageSize] is answered: it
// opens a handle, moves the position to where the request starts, and reads no
// further than the limit asks.
func (s *Server) sendRange(req *proto.ReadFileRequest, stream proto.FS_ReadFileServer) error {
	f, err := s.inner.Open(req.GetName())
	if err != nil {
		return toStatus(err)
	}
	// [polyfills.ToFile] so the seek the offset asks for is available even from a
	// file system whose handle is only an [fs.File].
	handle := polyfills.ToFile(f)
	offset, limit := req.GetOffset(), req.GetLimit()
	if offset != 0 {
		if _, err := handle.Seek(offset, io.SeekStart); err != nil {
			return ufserrors.Join(toStatus(err), handle.Close())
		}
	}
	var reader io.Reader = handle
	if limit > 0 {
		reader = io.LimitReader(handle, limit)
	}
	return ufserrors.Join(sendReader(stream, reader, rangeSize(handle, offset, limit)), handle.Close())
}

// rangeSize is how many bytes a read from offset, for at most limit bytes when
// limit is set, will carry from the handle as it stands now, or 0 when the
// handle cannot say: it is the size hint of the first frame.
func rangeSize(handle fs.File, offset, limit int64) int64 {
	info, err := handle.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	size := max(info.Size()-offset, 0)
	if limit > 0 {
		size = min(size, limit)
	}
	return size
}

// sendContent writes data out as frames of at most readChunkSize bytes. The frames
// are windows onto data rather than copies of it: [Send] marshals the message into
// storage of its own before it answers (prepareMsg, in google.golang.org/grpc
// stream.go), so nothing here has to keep a frame readable after it has answered.
//
// The first frame carries the size of the whole answer, so a client collecting
// it allocates once.
func sendContent(stream proto.FS_ReadFileServer, data []byte) error {
	size := int64(len(data))
	for len(data) > 0 {
		n := min(len(data), readChunkSize)
		if err := stream.Send(&proto.ReadFileResponse{Data: data[:n], Size: size}); err != nil {
			return err
		}
		size = 0
		data = data[n:]
	}
	return nil
}

// readChunks holds the buffers sendReader reads into, one per stream in
// progress, so a busy server reuses them rather than allocating
// [readChunkSize] for every ranged read.
var readChunks = sync.Pool{New: func() any {
	chunk := make([]byte, readChunkSize)
	return &chunk
}}

// sendReader copies what r gives into frames until it runs out, stopping as soon
// as the stream is gone. One buffer serves every frame, taken from a pool shared
// by every stream: [Send] marshals the message into pooled storage of its own
// before it answers (prepareMsg, in google.golang.org/grpc stream.go), so the
// bytes it was handed are not still in use afterwards, and a buffer per frame,
// or per stream, would be an allocation for no reason the transport can see.
//
// size is the hint the first frame carries, 0 when it is not known.
func sendReader(stream proto.FS_ReadFileServer, r io.Reader, size int64) error {
	chunkp := readChunks.Get().(*[]byte)
	defer readChunks.Put(chunkp)
	chunk := *chunkp
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			if sendErr := stream.Send(&proto.ReadFileResponse{Data: chunk[:n], Size: size}); sendErr != nil {
				return sendErr
			}
			size = 0
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return toStatus(err)
		}
	}
}
