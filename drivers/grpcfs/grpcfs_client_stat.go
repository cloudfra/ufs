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

// Stat and Lstat are the [fs.StatFS] half of [types.ReadFS]. The stat helper
// they share is the one GetFile lookup that [grpcFS.Open] and [grpcFS.Create]
// use as well.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
)

// Stat reports the node at name, following a trailing symbolic link.
func (fsys *grpcFS) Stat(name string) (fs.FileInfo, error) {
	return fsys.stat("stat", name, proto.SymlinkHandling_SYMLINK_HANDLING_FOLLOW)
}

// Lstat reports the node at name, without following a trailing symbolic link.
func (fsys *grpcFS) Lstat(name string) (fs.FileInfo, error) {
	return fsys.stat("lstat", name, proto.SymlinkHandling_SYMLINK_HANDLING_NO_FOLLOW)
}

// stat is the GetFile lookup the verbs share. The closed check and the rpc sit in
// one read-lock section; the path validation, which needs no connection, stays
// outside it.
//
// Stat and Lstat are thin verbs over it and take no lock of their own: an RLock
// held across a call into a sibling of the same file system that takes the lock
// again deadlocks against a Close waiting between them, because RWMutex blocks a
// new reader while a writer waits.
func (fsys *grpcFS) stat(op, name string, handling proto.SymlinkHandling) (fs.FileInfo, error) {
	if err := pathutil.Validate(op, name); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	if fsys.closed.Load() {
		fsys.mu.RUnlock()
		return nil, ufserrors.NewPathError(op, name, fs.ErrClosed)
	}
	info, err := fsys.statLocked(op, name, handling)
	fsys.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return info, nil
}

// statLocked is the rpc behind a lookup. Its caller holds the read lock for the
// whole of it, which is what keeps the connection alive underneath it; the helper
// takes no lock itself, so [grpcFS.Open], [grpcFS.Create] and
// [grpcFS.checkAboveLocked] can make a run of lookups one section.
func (fsys *grpcFS) statLocked(op, name string, handling proto.SymlinkHandling) (fs.FileInfo, error) {
	file, err := fsys.client.GetFile(fsys.ctx, &proto.GetFileRequest{Name: name, SymlinkHandling: handling})
	if err != nil {
		return nil, pathError(op, name, err)
	}
	return fromProtoFile(file), nil
}
