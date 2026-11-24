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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strconv"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ proto.FSServer = (*Server)(nil)

const (
	// readChunkSize is how much file content one ReadFileResponse carries. How
	// big a whole file has to be before it is worth streaming rather than
	// sending whole is server policy rather than part of the wire format, so the
	// server picks it: small enough that a large file arrives in frames instead
	// of one buffered message, large enough that a modest file is one frame.
	readChunkSize = 64 << 10

	// defaultListPageSize is how many children ListFiles answers when the
	// request asks for no maximum of its own.
	defaultListPageSize = 1000
)

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
// [types.WriteFileFS] when it has one, and is where a fallback built out of
// [types.CreateFileFS] belongs.
//
// Close and Release are deliberately not called here. The service has no rpc for
// either — see the service comment — and a server cannot decide when every client
// it serves is done with the file system, so what it was handed stays the caller's
// to close. What a handler does close is a file handle it opened itself, which the
// caller of the rpc never sees.
type Server struct {
	inner types.WriteFS

	// proto.UnimplementedFSServer is embedded by value so that an rpc added to
	// the .proto before this file catches up answers codes.Unimplemented instead
	// of silently doing nothing.
	proto.UnimplementedFSServer
}

// NewServer returns a Server serving inner. Register it on a grpc.Server with
// proto.RegisterFSServer.
func NewServer(inner types.WriteFS) *Server {
	return &Server{inner: inner}
}

// GetFile answers [types.ReadFS.Stat], or [types.ReadFS.Lstat] when the request
// asks that a trailing symbolic link not be followed.
func (s *Server) GetFile(_ context.Context, req *proto.GetFileRequest) (*proto.File, error) {
	var (
		info fs.FileInfo
		err  error
	)
	if req.GetSymlinkHandling() == proto.SymlinkHandling_SYMLINK_HANDLING_NO_FOLLOW {
		info, err = s.inner.Lstat(req.GetName())
	} else {
		info, err = s.inner.Stat(req.GetName())
	}
	if err != nil {
		return nil, toStatus(err)
	}
	return toProtoFile(req.GetName(), info), nil
}

// ListFiles answers one page of [types.ReadFS.ReadDir].
func (s *Server) ListFiles(_ context.Context, req *proto.ListFilesRequest) (*proto.ListFilesResponse, error) {
	entries, err := s.inner.ReadDir(req.GetParent())
	if err != nil {
		return nil, toStatus(err)
	}
	offset, err := listOffset(req.GetPageToken(), req.GetParent())
	if err != nil {
		return nil, toStatus(err)
	}
	limit := int(req.GetPageSize())
	if limit <= 0 {
		limit = defaultListPageSize
	}
	if offset > len(entries) {
		offset = len(entries)
	}
	// The end of the page is checked against overflow as well as against the end
	// of the listing: page_size is an int32 a client chooses, so on a 32-bit build
	// the sum can wrap past the end of the slice instead of stopping at it.
	end := offset + limit
	if end < offset || end > len(entries) {
		end = len(entries)
	}

	// A File carries size, mode and modification time, which the fs.DirEntry
	// ReadDir hands back does not, so every child in the page is looked up by name:
	// one page costs one listing plus one lookup per child it answers.
	files := make([]*proto.File, 0, end-offset)
	for _, entry := range entries[offset:end] {
		info, err := entry.Info()
		if err != nil {
			return nil, toStatus(err)
		}
		files = append(files, toProtoFile(path.Join(req.GetParent(), entry.Name()), info))
	}

	resp := &proto.ListFilesResponse{Files: files}
	if end < len(entries) {
		resp.NextPageToken = strconv.Itoa(end)
	}
	return resp, nil
}

// listOffset reads a page token back. The service leaves the token's shape to
// the server, so this one is simply how many children of the directory have been
// answered already: ReadDir returns the whole listing in order, so an offset
// into it is the shortest token that keeps its meaning from one page to the next.
//
// Every page reads the directory again, so a directory that changes while a
// client walks it moves the offset and the next page can repeat a child or skip
// one. Holding a position steady across calls means the server has to remember a
// listing, which is a decision about servers rather than about this rpc.
func listOffset(token string, parent string) (int, error) {
	if token == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(token)
	if err != nil || offset < 0 {
		return 0, ufserrors.NewPathError("readdir", parent,
			fmt.Errorf("page token %q is not an offset into a directory listing: %w", token, fs.ErrInvalid))
	}
	return offset, nil
}

// ReadFile streams the part of a file's content that the request asks for.
func (s *Server) ReadFile(req *proto.ReadFileRequest, stream proto.FS_ReadFileServer) error {
	if req.GetOffset() == 0 && req.GetLimit() == 0 {
		// The whole file, which is what [types.ReadFS.ReadFile] answers, so a
		// file system with a native whole-file read does not have to open a
		// handle to serve it.
		data, err := s.inner.ReadFile(req.GetName())
		if err != nil {
			return toStatus(err)
		}
		return sendContent(stream, data)
	}
	return s.sendRange(req, stream)
}

// sendRange answers a read that starts partway into a file or stops before its
// end, which a whole-file read cannot do: it opens a handle, moves the position
// to where the request starts, and reads no further than the limit asks.
func (s *Server) sendRange(req *proto.ReadFileRequest, stream proto.FS_ReadFileServer) error {
	f, err := s.inner.Open(req.GetName())
	if err != nil {
		return toStatus(err)
	}
	// [polyfills.ToFile] so the seek the offset asks for is available even from a
	// file system whose handle is only an [fs.File].
	handle := polyfills.ToFile(f)
	if offset := req.GetOffset(); offset != 0 {
		if _, err := handle.Seek(offset, io.SeekStart); err != nil {
			return ufserrors.Join(toStatus(err), handle.Close())
		}
	}
	var reader io.Reader = handle
	if limit := req.GetLimit(); limit > 0 {
		reader = io.LimitReader(handle, limit)
	}
	return ufserrors.Join(sendReader(stream, reader), handle.Close())
}

// sendContent writes data out as frames of at most readChunkSize bytes.
func sendContent(stream proto.FS_ReadFileServer, data []byte) error {
	for len(data) > 0 {
		n := min(len(data), readChunkSize)
		if err := stream.Send(&proto.ReadFileResponse{Data: data[:n]}); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

// sendReader copies what r gives into frames until it runs out, stopping as soon
// as the stream is gone. Each frame gets its own bytes rather than a window onto
// a buffer that the next read overwrites.
func sendReader(stream proto.FS_ReadFileServer, r io.Reader) error {
	for {
		chunk := make([]byte, readChunkSize)
		n, err := r.Read(chunk)
		if n > 0 {
			if sendErr := stream.Send(&proto.ReadFileResponse{Data: chunk[:n]}); sendErr != nil {
				return sendErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return toStatus(err)
		}
	}
}

// ReadLink answers [types.ReadFS.ReadLink].
func (s *Server) ReadLink(_ context.Context, req *proto.ReadLinkRequest) (*proto.ReadLinkResponse, error) {
	target, err := s.inner.ReadLink(req.GetName())
	if err != nil {
		return nil, toStatus(err)
	}
	return &proto.ReadLinkResponse{Target: target}, nil
}

// GetMetadata answers the three methods that say something about the file system
// rather than about a path in it, in the one response the rpc exists to give.
func (s *Server) GetMetadata(_ context.Context, _ *proto.GetMetadataRequest) (*proto.FileSystemInfo, error) {
	uri, err := s.inner.URI()
	if err != nil {
		return nil, toStatus(err)
	}
	info := &proto.FileSystemInfo{Description: s.inner.String(), Devices: toProtoDeviceMap(s.inner.DeviceInfo())}
	if uri != nil {
		info.Uri = uri.String()
	}
	return info, nil
}

// WriteFile collects one file's upload and writes it whole, as
// [types.WriteFileFS.WriteFile] does — through [polyfills.WriteFile] for a file
// system that has no whole-file write of its own, which is why the content is
// buffered rather than handed to the file system a frame at a time.
func (s *Server) WriteFile(stream proto.FS_WriteFileServer) error {
	first, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			// The comment on the rpc says an empty stream writes an empty file,
			// which cannot be answered: the path of the file travels in the
			// first frame, so a stream with no frames names nothing to write to.
			return status.Error(codes.InvalidArgument,
				"a WriteFile stream has to carry a first frame, which is where the path of the file is")
		}
		return err
	}
	var data bytes.Buffer
	data.Write(first.GetData())
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		// Only the first frame's name and mode count, which is what lets the
		// whole upload travel as one HTTP request body.
		data.Write(frame.GetData())
	}
	// The mode goes through as the frame carries it, 0 included: proto3 cannot
	// tell an unset field from one set to 0, so a request that names no mode is
	// indistinguishable from one that asks for 000, and substituting a default for
	// a 0 here would break the caller that asked for 000. That leaves the wire
	// format and the io/fs permission bits disagreeing about what an omitted mode
	// means, which is a question about the .proto rather than one a handler can
	// settle.
	if err := polyfills.WriteFile(s.inner, first.GetName(), data.Bytes(), fs.FileMode(first.GetMode())); err != nil {
		return toStatus(err)
	}
	return stream.SendAndClose(&emptypb.Empty{})
}

// MkdirAll answers [types.MkdirAllFS.MkdirAll], with the same pass-through of the
// requested mode as the upload.
func (s *Server) MkdirAll(_ context.Context, req *proto.MkdirAllRequest) (*emptypb.Empty, error) {
	if err := s.inner.MkdirAll(req.GetName(), fs.FileMode(req.GetMode())); err != nil {
		return nil, toStatus(err)
	}
	return &emptypb.Empty{}, nil
}

// Remove answers [types.RemoveFS.Remove].
func (s *Server) Remove(_ context.Context, req *proto.RemoveRequest) (*emptypb.Empty, error) {
	if err := s.inner.Remove(req.GetName()); err != nil {
		return nil, toStatus(err)
	}
	return &emptypb.Empty{}, nil
}

// RemoveAll answers [types.RemoveAllFS.RemoveAll]: a name that is not there is not
// an error, which is what the rpc documents and what the file system already
// answers.
func (s *Server) RemoveAll(_ context.Context, req *proto.RemoveAllRequest) (*emptypb.Empty, error) {
	if err := s.inner.RemoveAll(req.GetName()); err != nil {
		return nil, toStatus(err)
	}
	return &emptypb.Empty{}, nil
}
