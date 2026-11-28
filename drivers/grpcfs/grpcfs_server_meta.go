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
	"context"
	"io/fs"

	"github.com/cloudfra/ufs/proto"
)

// The handlers that answer a question about what a name is rather than about what
// is inside it or what to change: one lookup, one link read, and the metadata of
// the file system itself. None of them moves file content, so all three are
// unary and short.

// GetFile answers [types.ReadFS.Stat], or [types.ReadFS.Lstat] when the request
// asks that a trailing symbolic link not be followed.
func (s *Server) GetFile(_ context.Context, req *proto.GetFileRequest) (*proto.File, error) {
	if !s.life.Enter() {
		return nil, errShutdown
	}
	defer s.life.Exit()
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

// ReadLink answers [types.ReadFS.ReadLink].
func (s *Server) ReadLink(_ context.Context, req *proto.ReadLinkRequest) (*proto.ReadLinkResponse, error) {
	if !s.life.Enter() {
		return nil, errShutdown
	}
	defer s.life.Exit()
	target, err := s.inner.ReadLink(req.GetName())
	if err != nil {
		return nil, toStatus(err)
	}
	return &proto.ReadLinkResponse{Target: target}, nil
}

// GetMetadata answers the three methods that say something about the file system
// rather than about a path in it, in the one response the rpc exists to give.
func (s *Server) GetMetadata(_ context.Context, _ *proto.GetMetadataRequest) (*proto.FileSystemInfo, error) {
	if !s.life.Enter() {
		return nil, errShutdown
	}
	defer s.life.Exit()
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
