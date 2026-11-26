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

//go:build !wasm

package boltfs

// Remove is the [types.RemoveFS] half of [types.WriteFS]: it deletes one key or one empty bucket.

import (
	"bytes"
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	bolt "go.etcd.io/bbolt"
)

func (fsys *boltFS) Remove(name string) error {
	if fsys.isClosed() {
		return ufserrors.NewPathError("remove", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("remove", name); err != nil {
		return err
	}
	if name == pathutil.CwdPath {
		return ufserrors.NewPathError("remove", name, fs.ErrPermission)
	}
	err := fsys.update(func(tx *bolt.Tx) error {
		bkt, key, err := lookupParent(tx, name)
		if err != nil {
			return err
		}
		if sub := bkt.Bucket(key); sub != nil {
			if !isEmptyDir(sub) {
				return ufserrors.ErrDirNotEmpty
			}
			return bkt.DeleteBucket(key)
		}
		if bkt.Get(key) == nil {
			return fs.ErrNotExist
		}
		return bkt.Delete(key)
	})
	if err != nil {
		return ufserrors.NewPathError("remove", name, err)
	}
	fsys.notify(types.NotifyRemove, name)
	return nil
}

// isEmptyDir reports whether the directory bucket bkt has no children. Every
// directory bucket holds its own selfKey record, so it is empty when that is
// the only key; at most two keys are inspected.
func isEmptyDir(bkt *bolt.Bucket) bool {
	c := bkt.Cursor()
	k, _ := c.First()
	if bytes.Equal(k, selfKey) {
		k, _ = c.Next()
	}
	return k == nil
}
