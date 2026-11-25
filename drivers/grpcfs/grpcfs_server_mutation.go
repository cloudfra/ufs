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
	"google.golang.org/protobuf/types/known/emptypb"
)

// The three rpcs that change the shape of the tree without carrying content: the
// upload that writes a file lives in grpcfs_server_write.go beside the reads,
// because what it does to the name is what a read of that name would report.

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
