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

// Open is the [fs.FS] half of [types.SimpleFS], the smallest file system
// drivers/types describes: a handle over the two rpcs that carry content and
// children.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/proto"
)

// Open returns a read-only handle on the file at name.
//
// A regular file gets a [grpcFile], which reads through the ReadFile rpc and
// carries its position here because the service carries no file that the server
// holds open. A directory cannot be opened that way — it has no content, only
// children — so it gets a [grpcDir] over the ListFiles answer instead, which is
// listed on the first ReadDir rather than at Open so that opening a large
// directory costs nothing until it is walked.
//
// The extra GetFile is what tells the two apart, which makes Open two round trips
// rather than one; the alternative is a handle that only learns it is a directory
// when something asks it for a listing. It is also where a handle's size comes
// from, so it can be seeked from its end before it has read anything.
func (fsys *grpcFS) Open(name string) (fs.File, error) {
	if err := fsys.closedErr("open", name); err != nil {
		return nil, err
	}
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	info, err := fsys.stat("open", name, proto.SymlinkHandling_SYMLINK_HANDLING_FOLLOW)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return &grpcDir{name: name, fsys: fsys, info: info}, nil
	}
	return newGrpcFile(name, info, fsys), nil
}
