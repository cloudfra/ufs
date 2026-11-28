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
	"fmt"
	"io/fs"
	"path"
	"strconv"

	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
)

// ListFiles answers one page of [types.ReadFS.ReadDir].
func (s *Server) ListFiles(_ context.Context, req *proto.ListFilesRequest) (*proto.ListFilesResponse, error) {
	if !s.life.Enter() {
		return nil, errShutdown
	}
	defer s.life.Exit()
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
	// However many a request asks for, a page stays well inside the message
	// ceiling, so asking for more than the client could receive costs a page
	// boundary rather than a failed call.
	limit = min(limit, maxListPageSize)
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
// TODO: Several listings of one directory cannot be in flight at once on this
// shape, and none of them is stable. Each page re-reads the directory and cuts a
// window out of what it gets, so a child added or removed between two pages moves
// the offset and the next page repeats a child or skips one, and two clients
// walking the same directory share nothing but the directory. Fixing both needs a
// listing that is a thing of its own rather than a window onto a fresh read: a
// method beside ReadDir that hands back a snapshot with its own position and its
// own lifetime, so a page token names a listing rather than an offset, and a
// context that says how long one walk may hold it. That is a change to the
// interfaces in drivers/types and to the service, not to this handler, so it is
// drafted in cloudfra/ufs#723 rather than answered here.
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
