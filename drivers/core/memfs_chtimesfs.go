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

// Chtimes is [types.ChtimesFS], which no memFS interface asks for: a caller reaches it with a type assertion.

import (
	"io/fs"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// Chtimes changes the modification time a node already carries. A node holds no
// access time, so the access time is one of the times [types.ChtimesFS] leaves a
// backend free to leave alone, which is what happens here rather than storing a
// value nothing else reads. A change to a node's attributes is what
// [types.NotifyChmod] reports, so watchers of a memory file system see the node
// change even though its content did not.
func (fsys *memFS) Chtimes(name string, _ time.Time, mtime time.Time) error {
	if fsys.isClosed() {
		return ufserrors.NewPathError("chtimes", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("chtimes", name); err != nil {
		return err
	}
	fsys.mu.Lock()
	node, ok := fsys.nodes[name]
	if !ok {
		fsys.mu.Unlock()
		return ufserrors.NewPathError("chtimes", name, fs.ErrNotExist)
	}
	if !mtime.IsZero() {
		node.modTime = mtime
	}
	fsys.notify(types.NotifyChmod, name)
	fsys.mu.Unlock()
	return nil
}
