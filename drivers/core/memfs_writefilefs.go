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

package core

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var _ types.WriteFileFS = (*memFS)(nil)

// WriteFile replaces the content of name with data in one step: the name holds
// either what it held before or all of data, and a reader never sees the empty
// file that opening it with os.O_TRUNC and then writing would show in between.
// That is what a server answering the grpcfs WriteFile rpc promises its
// readers, and why memFS has a whole-file write of its own rather than leaving
// it to the polyfill.
//
// Otherwise it is OpenFile with os.O_WRONLY|os.O_CREATE|os.O_TRUNC followed by
// one write: a missing name is created with perm and its parent directories,
// an existing file keeps its mode, a directory is refused, and data is copied,
// so the caller may reuse it.
func (fsys *memFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	const op = "writefile"
	if fsys.isClosed() {
		return ufserrors.NewPathError(op, name, fs.ErrClosed)
	}
	if err := pathutil.ValidateFileName(op, name); err != nil {
		return err
	}
	content := bytes.Clone(data)
	now := time.Now()

	fsys.mu.Lock()
	node, existed := fsys.nodes[name]
	switch {
	case existed && node.isDir:
		fsys.mu.Unlock()
		return ufserrors.NewPathError(op, name, fmt.Errorf("is a directory: %w", fs.ErrInvalid))
	case !existed:
		if dir := path.Dir(name); dir != pathutil.CwdPath {
			if err := fsys.checkDirsLocked(dir); err != nil {
				fsys.mu.Unlock()
				return ufserrors.NewPathError(op, name, err)
			}
		}
		fsys.nodes[name] = &memNode{name: path.Base(name), content: content, mode: perm, modTime: now}
		fsys.ensureParentsLocked(name, now)
		fsys.notify(types.NotifyCreate, name)
	default:
		node.content = content
		node.modTime = now
	}
	fsys.notify(types.NotifyWrite, name)
	fsys.mu.Unlock()
	return nil
}
