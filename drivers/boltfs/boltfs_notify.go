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

//go:build !wasm

package boltfs

import (
	"context"
	"io"
	"io/fs"

	bolt "go.etcd.io/bbolt"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/notifybus"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var _ ufs.Watcher = (*boltFS)(nil)

// Watch implements [ufs.Watcher] for bolt-backed file systems. It watches name
// (a directory) and all nested paths, invoking hook for each mutation
// performed through the boltFS API (Create, file Close after a write,
// Remove, RemoveAll, MkdirAll).
func (fsys *boltFS) Watch(ctx context.Context, name string, hook ufs.NotifyHook) (io.Closer, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("watch", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("watch", name); err != nil {
		return nil, err
	}

	if name != pathutil.CwdPath {
		if err := fsys.view(func(tx *bolt.Tx) error {
			_, err := lookupDir(tx, name)
			return err
		}); err != nil {
			return nil, ufserrors.NewPathError("watch", name, err)
		}
	}

	return fsys.notifyBus.Subscribe(ctx, name, func(op notifybus.Op, path string) {
		hook(ufs.NotifyOp(op), path)
	}), nil
}

// notify sends an event to all active watchers.
func (fsys *boltFS) notify(op ufs.NotifyOp, path string) {
	fsys.notifyBus.Publish(notifybus.Op(op), path)
}
