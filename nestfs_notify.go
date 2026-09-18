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
)

var _ Watcher = (*nestFS)(nil)

// Watch implements [Watcher] by delegating to the underlying FS if it supports
// the [Watcher] interface. Returns [fs.ErrInvalid] if the underlying FS does
// not support watching.
func (fsys *nestFS) Watch(ctx context.Context, name string, hook NotifyHook) (io.Closer, error) {
	if err := fsys.validPath("watch", name); err != nil {
		return nil, err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	w, ok := mountFS.fsys.(Watcher)
	if !ok {
		return nil, pathError("watch", name, fs.ErrInvalid)
	}

	return w.Watch(ctx, subName, hook)
}
