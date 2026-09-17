// Copyright 2026 Jeremy Edwards
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

package ufs

import (
	"context"
	"io"
	"io/fs"

	"github.com/cloudfra/ufs/internal/notifybus"
)

var _ Watcher = (*memFS)(nil)

// Watch implements [Watcher] for in-memory file systems. It watches name (a
// directory) and all nested paths, invoking hook for each mutation performed
// through the memFS API (Create, Write, Remove, RemoveAll, MkdirAll).
func (fsys *memFS) Watch(ctx context.Context, name string, hook NotifyHook) (io.Closer, error) {
	if fsys.isClosed() {
		return nil, pathError("watch", name, fs.ErrClosed)
	}
	if err := validPath("watch", name); err != nil {
		return nil, err
	}

	fsys.mu.RLock()
	if name != cwdPath {
		node, ok := fsys.nodes[name]
		if !ok {
			fsys.mu.RUnlock()
			return nil, pathError("watch", name, fs.ErrNotExist)
		}
		if !node.isDir {
			fsys.mu.RUnlock()
			return nil, pathError("watch", name, fs.ErrInvalid)
		}
	}
	fsys.mu.RUnlock()

	sub := fsys.notifyBus.Subscribe(ctx, name, func(op notifybus.Op, path string) {
		hook(NotifyOp(op), path)
	})
	return sub, nil
}

// notify sends an event to all active watchers.
func (fsys *memFS) notify(op NotifyOp, path string) {
	fsys.notifyBus.Publish(notifybus.Op(op), path)
}
