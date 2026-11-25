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
	"github.com/cloudfra/ufs/proto"
)

// ReadDir lists the children of name, asking for one page of the listing at a
// time and keeping the order the server answered them in, which is the order
// [fs.ReadDirFS] asks for.
func (fsys *grpcFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := fsys.closedErr("readdir", name); err != nil {
		return nil, err
	}
	if err := pathutil.Validate("readdir", name); err != nil {
		return nil, err
	}
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
