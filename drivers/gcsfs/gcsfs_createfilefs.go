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

package gcsfs

// Create is the [types.CreateFileFS] half of [types.WriteFS]: it opens a writer whose Close commits the object.

import (
	"io/fs"
	"path"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// Create hands back a writer bound to the client. The closed check and the call
// that binds it are one read-lock section; the path validation stays outside
// it. The writer itself belongs to the handle that Create returned: closing the
// file system afterwards is a separate question from the one this section
// settles, and is left alone here.
func (fsys *gcsFS) Create(name string) (types.File, error) {
	if err := pathutil.Validate("create", name); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	if fsys.closed.Load() {
		fsys.mu.RUnlock()
		return nil, ufserrors.NewPathError("create", name, fs.ErrClosed)
	}
	objPath := path.Join(fsys.baseDir, name)
	w := fsys.client.Bucket(fsys.bucket).Object(objPath).NewWriter(fsys.ctx)
	w.ChunkSize = minChunkSize
	fsys.mu.RUnlock()

	return &gcsFile{
		name:   name,
		isDir:  false,
		fsys:   fsys,
		writer: w,
	}, nil
}
