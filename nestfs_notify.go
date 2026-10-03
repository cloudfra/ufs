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
	"path"
	"strings"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var _ Watcher = (*nestFS)(nil)

// Watch implements [Watcher] by delegating to the underlying FS if it supports
// the [Watcher] interface. Returns [fs.ErrInvalid] if the underlying FS does
// not support watching. When name is inside a mount, the paths passed to hook
// are still relative to the root of this file system.
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
		return nil, ufserrors.NewPathError("watch", name, fs.ErrInvalid)
	}

	// The backend reports paths relative to its own root. Translate them back
	// to this file system's root when the backend is mounted below it.
	if prefix := mountPrefix(name, subName); prefix != "" {
		inner := hook
		hook = func(op NotifyOp, eventPath string) {
			inner(op, path.Join(prefix, eventPath))
		}
	}

	return w.Watch(ctx, subName, hook)
}

// mountPrefix returns the path at which the file system that resolved name to
// subName is mounted, or "" when it is the root file system.
func mountPrefix(name, subName string) string {
	switch {
	case subName == name:
		return ""
	case pathutil.IsCwd(subName):
		return name
	default:
		return strings.TrimSuffix(name, pathutil.UnixSeparator+subName)
	}
}
