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
	"time"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The rpcs of the optional interfaces in drivers/types. Each goes through
// drivers/polyfills, which uses the served file system's own method when it has
// one, emulates it on the server where an emulation keeps the method's
// contract (a rename or a copy by moving content on the server, never over
// the network), and otherwise reports an error wrapping
// [errors.ErrUnsupported], which travels as codes.Unimplemented and comes back
// out of the client as that same sentinel. So a client sees what a caller in
// the server's own process would see from the polyfill.

// empty runs op as one admitted rpc whose answer is only an error.
func (s *Server) empty(op func() error) (*emptypb.Empty, error) {
	if !s.life.Enter() {
		return nil, errShutdown
	}
	defer s.life.Exit()
	if err := op(); err != nil {
		return nil, toStatus(err)
	}
	return &emptypb.Empty{}, nil
}

// Rename answers [types.RenameFS.Rename].
func (s *Server) Rename(_ context.Context, req *proto.RenameRequest) (*emptypb.Empty, error) {
	return s.empty(func() error {
		return polyfills.Rename(s.inner, req.GetOldName(), req.GetNewName())
	})
}

// Mkdir answers [types.MkdirFS.Mkdir].
func (s *Server) Mkdir(_ context.Context, req *proto.MkdirRequest) (*emptypb.Empty, error) {
	return s.empty(func() error {
		return polyfills.Mkdir(s.inner, req.GetName(), fs.FileMode(req.GetMode()))
	})
}

// Chmod answers [types.ChmodFS.Chmod].
func (s *Server) Chmod(_ context.Context, req *proto.ChmodRequest) (*emptypb.Empty, error) {
	return s.empty(func() error {
		return polyfills.Chmod(s.inner, req.GetName(), fs.FileMode(req.GetMode()))
	})
}

// Chown answers [types.ChownFS.Chown], or [types.LchownFS.Lchown] when the
// request asks that a trailing symbolic link not be followed.
func (s *Server) Chown(_ context.Context, req *proto.ChownRequest) (*emptypb.Empty, error) {
	return s.empty(func() error {
		uid, gid := int(req.GetUid()), int(req.GetGid())
		if req.GetSymlinkHandling() == proto.SymlinkHandling_SYMLINK_HANDLING_NO_FOLLOW {
			return polyfills.Lchown(s.inner, req.GetName(), uid, gid)
		}
		return polyfills.Chown(s.inner, req.GetName(), uid, gid)
	})
}

// Chtimes answers [types.ChtimesFS.Chtimes]. A time the request leaves unset is
// the zero time, which leaves that time as it is.
func (s *Server) Chtimes(_ context.Context, req *proto.ChtimesRequest) (*emptypb.Empty, error) {
	return s.empty(func() error {
		return polyfills.Chtimes(s.inner, req.GetName(),
			fromTimestamp(req.GetAccessTime()), fromTimestamp(req.GetModificationTime()))
	})
}

// Symlink answers [types.SymlinkFS.Symlink].
func (s *Server) Symlink(_ context.Context, req *proto.SymlinkRequest) (*emptypb.Empty, error) {
	return s.empty(func() error {
		return polyfills.Symlink(s.inner, req.GetTarget(), req.GetName())
	})
}

// Link answers [types.LinkFS.Link].
func (s *Server) Link(_ context.Context, req *proto.LinkRequest) (*emptypb.Empty, error) {
	return s.empty(func() error {
		return polyfills.Link(s.inner, req.GetOldName(), req.GetNewName())
	})
}

// CopyFile answers [types.CopyFileFS.CopyFile] on the server, so the content
// moves inside the served file system rather than down to the client and back.
func (s *Server) CopyFile(_ context.Context, req *proto.CopyFileRequest) (*emptypb.Empty, error) {
	return s.empty(func() error {
		return polyfills.CopyFile(s.inner, req.GetSourceName(), req.GetDestinationName())
	})
}

// ClosestParent answers [types.ClosestParentFS.ClosestParent].
func (s *Server) ClosestParent(_ context.Context, req *proto.ClosestParentRequest) (*proto.ClosestParentResponse, error) {
	if !s.life.Enter() {
		return nil, errShutdown
	}
	defer s.life.Exit()
	parent, isDir, err := polyfills.ClosestParent(s.inner, req.GetName())
	if err != nil {
		return nil, toStatus(err)
	}
	return &proto.ClosestParentResponse{Name: parent, IsDir: isDir}, nil
}

// fromTimestamp converts a wire time, an unset one being the zero time.
func fromTimestamp(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}

// toTimestamp converts a time for the wire, the zero time being unset.
func toTimestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
