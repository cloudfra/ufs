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

// Rename with the two bucket-moving helpers only it uses is the [types.RenameFS] half, which [types.WriteFS] does not ask for but this driver answers.

import (
	"bytes"
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	bolt "go.etcd.io/bbolt"
)

// Rename moves oldPath, and everything below it, to newPath in a single
// transaction, so it is atomic. Missing parent directories of newPath are
// created as Create does.
func (fsys *boltFS) Rename(oldPath, newPath string) error {
	if err := pathutil.ValidateRename(oldPath, newPath); err != nil {
		return err
	}
	var moved []string
	err := fsys.update(func(tx *bolt.Tx) error {
		srcParent, srcKey, err := lookupParent(tx, oldPath)
		if err != nil {
			return err
		}
		srcDir, srcRecord := lookupEntry(srcParent, srcKey)
		if srcDir == nil && srcRecord == nil {
			return fs.ErrNotExist
		}
		dstParent, dstKey, err := parentBucket(tx, newPath)
		if err != nil {
			return err
		}
		if dstDir, dstRecord := lookupEntry(dstParent, dstKey); dstDir != nil || dstRecord != nil {
			return fs.ErrExist
		}

		if srcDir == nil {
			// A file: its record moves as it is.
			moved = append(moved, oldPath)
			if err := dstParent.Put(dstKey, bytes.Clone(srcRecord)); err != nil {
				return err
			}
			return srcParent.Delete(srcKey)
		}
		// A directory: its bucket, with everything below it, is copied and
		// then deleted, as bbolt cannot move a bucket.
		if err := collectPaths(srcDir, oldPath, &moved); err != nil {
			return err
		}
		moved = append(moved, oldPath)
		dstDir, err := dstParent.CreateBucket(dstKey)
		if err != nil {
			return err
		}
		if err := copyBucket(srcDir, dstDir); err != nil {
			return err
		}
		return srcParent.DeleteBucket(srcKey)
	})
	if err != nil {
		return ufserrors.NewLinkError("rename", oldPath, newPath, err)
	}
	for _, oldKey := range moved {
		fsys.notify(types.NotifyRename, oldKey)
		fsys.notify(types.NotifyCreate, newPath+oldKey[len(oldPath):])
	}
	return nil
}

// lookupEntry returns the bucket of the directory or the record of the file
// named key in parent. At most one is non-nil: bbolt's Get returns nil for a
// key that holds a bucket, and a record is never empty, as it always holds a
// modification time. Both are nil when key does not exist.
func lookupEntry(parent *bolt.Bucket, key []byte) (dir *bolt.Bucket, record []byte) {
	if dir = parent.Bucket(key); dir != nil {
		return dir, nil
	}
	return nil, parent.Get(key)
}

// copyBucket copies every key of src, and every bucket below it, into dst.
// Keys and values are cloned, as bbolt requires them to stay valid until the
// transaction ends.
func copyBucket(src, dst *bolt.Bucket) error {
	return src.ForEach(func(k, v []byte) error {
		if v != nil {
			return dst.Put(bytes.Clone(k), bytes.Clone(v))
		}
		child, err := dst.CreateBucket(k)
		if err != nil {
			return err
		}
		return copyBucket(src.Bucket(k), child)
	})
}
