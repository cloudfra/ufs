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

// ReadDir is the [fs.ReadDirFS] half of [types.ReadFS]: the paged ListFiles
// answer that a [grpcDir] walks a handle at a time.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
)

// ReadDir lists the children of name, asking for one page of the listing at a
// time and keeping the order the server answered them in, which is the order
// [fs.ReadDirFS] asks for.
//
// The closed check and the whole page walk are one read-lock section. The walk is
// not one call: every page asks the same connection again, so a connection given
// up in the middle of it would answer the middle of a walk rather than its start,
// which is why the loop belongs inside the section rather than its first round
// trip. The path validation stays outside it, and the directory handle of
// [grpcFS.Open] walks through this method rather than holding a section of its
// own: the handle takes no lock, so its own ReadDir comes here and decides at this
// use site.
func (fsys *grpcFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := pathutil.Validate("readdir", name); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	if fsys.closed.Load() {
		fsys.mu.RUnlock()
		return nil, ufserrors.NewPathError("readdir", name, fs.ErrClosed)
	}
	entries, err := fsys.readDirLocked(name)
	fsys.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// readDirLocked is the page walk behind ReadDir. Its caller holds the read lock
// for the whole of it, which is what keeps the connection alive underneath every
// page; the helper takes no lock itself.
func (fsys *grpcFS) readDirLocked(name string) ([]fs.DirEntry, error) {
	var entries []fs.DirEntry
	token := ""
	for {
		resp, err := fsys.client.ListFiles(fsys.ctx, &proto.ListFilesRequest{Parent: name, PageToken: token})
		if err != nil {
			return nil, pathError("readdir", name, err)
		}
		for _, file := range resp.GetFiles() {
			entries = append(entries, fs.FileInfoToDirEntry(fromProtoFile(file)))
		}
		token = resp.GetNextPageToken()
		if token == "" {
			return entries, nil
		}
	}
}
