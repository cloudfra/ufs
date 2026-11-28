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

// Create is the [types.CreateFileFS] half of [types.WriteFS]: a handle over
// the WriteFile upload, and the walk up the path that lets the open answer at
// open time.

import (
	"errors"
	"io/fs"
	"path"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
)

// Create returns a handle for writing the file at name, truncating whatever is
// already there and leaving the parents alone. What is written goes into a
// WriteFile upload, so the handle has no position to move back to and nothing to
// read out of — see [grpcUpload] — and the content reaches the served file system
// when the handle is closed.
//
// The GetFile is what lets the open answer at open time: a client-streaming rpc
// says nothing until its last frame is in, so without it the refusal of a name
// that cannot hold a file — a directory, or a name under a regular file — would
// wait for Close, by which point the caller has been handed a handle for a file
// that was never going to be there. A name that is not there yet also looks at
// the levels in front of it, which is what [grpcFS.checkAbove] does: the lookup
// of the name itself says nothing about them.
func (fsys *grpcFS) Create(name string) (types.File, error) {
	if err := fsys.closedErr("create", name); err != nil {
		return nil, err
	}
	if err := pathutil.ValidateFileName("create", name); err != nil {
		return nil, err
	}
	info, statErr := fsys.stat("create", name, proto.SymlinkHandling_SYMLINK_HANDLING_NO_FOLLOW)
	switch {
	case statErr == nil && info.IsDir():
		return nil, ufserrors.NewPathError("create", name, fs.ErrInvalid)
	case errors.Is(statErr, fs.ErrNotExist):
		// Nothing under that name is there yet, so the only thing that can say
		// whether the name could hold a file is what stands above it.
		if err := fsys.checkAbove("create", name); err != nil {
			return nil, err
		}
	case statErr != nil:
		// The name cannot be reached at all, which is the served file system's
		// answer about the path above it rather than about this name.
		return nil, statErr
	}
	return newGrpcUpload(name, fsys), nil
}

// checkAbove walks the levels above name until it reaches one that is there, and
// refuses the name when that one is not a directory: io/fs file systems refuse a
// name with a regular file among its levels, and the shared suite every
// types.WriteFS is held to asks for the refusal as a *fs.PathError.
//
// A lookup of the name itself cannot answer this, because the file systems the
// service is served from do not agree about it: one rooted on a directory
// answers that a level is not a directory, while memFS keeps one flat map of
// names and answers that the whole name is simply not there, which says nothing
// about the file that is. What does answer it is the closest level that exists,
// which the ClosestParent rpc reports in one round trip however many levels are
// missing. The levels are followed through a symbolic link rather than looked
// at as links, because a linked directory is a parent a file system accepts.
func (fsys *grpcFS) checkAbove(op, name string) error {
	dir := path.Dir(name)
	if dir == "." || dir == "/" {
		return nil
	}
	// One ClosestParent answers the whole walk in one round trip. A server
	// from before that rpc answers Unimplemented, and then the walk is done
	// here one lookup per level.
	parent, isDir, err := fsys.ClosestParent(dir)
	switch {
	case err == nil:
		if !isDir && parent != "." {
			return ufserrors.NewPathError(op, name, fs.ErrExist)
		}
		return nil
	case !errors.Is(err, errors.ErrUnsupported):
		return err
	}
	return fsys.walkAbove(op, name)
}

// walkAbove is checkAbove for a server without the ClosestParent rpc.
func (fsys *grpcFS) walkAbove(op, name string) error {
	for dir := path.Dir(name); dir != "." && dir != "/" && dir != name; dir = path.Dir(dir) {
		info, err := fsys.stat(op, dir, proto.SymlinkHandling_SYMLINK_HANDLING_FOLLOW)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return ufserrors.NewPathError(op, name, fs.ErrExist)
		}
		return nil
	}
	return nil
}
