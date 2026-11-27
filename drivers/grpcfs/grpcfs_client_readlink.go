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

// ReadLink is the [fs.ReadLinkFS] half of [types.ReadFS]. Making a link is
// not on the wire at all, so reading one is the only link method here.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
)

// ReadLink reports the target stored in the symbolic link at name. The closed
// check and the rpc are one read-lock section; the path validation stays outside
// it.
func (fsys *grpcFS) ReadLink(name string) (string, error) {
	if err := pathutil.Validate("readlink", name); err != nil {
		return "", err
	}
	fsys.mu.RLock()
	if fsys.closed.Load() {
		fsys.mu.RUnlock()
		return "", ufserrors.NewPathError("readlink", name, fs.ErrClosed)
	}
	resp, err := fsys.client.ReadLink(fsys.ctx, &proto.ReadLinkRequest{Name: name})
	fsys.mu.RUnlock()
	if err != nil {
		return "", pathError("readlink", name, err)
	}
	return resp.GetTarget(), nil
}
