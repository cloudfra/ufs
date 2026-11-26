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

// RemoveAll and the child-deleting walk only it drives are the [types.RemoveAllFS] half of [types.WriteFS].

import (
	"bytes"
	"errors"
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	bolt "go.etcd.io/bbolt"
)

func (fsys *boltFS) RemoveAll(name string) error {
	if fsys.isClosed() {
		return ufserrors.NewPathError("removeall", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("removeall", name); err != nil {
		return err
	}

	var removed []string
	err := fsys.update(func(tx *bolt.Tx) error {
		if name == pathutil.CwdPath {
			bkt, err := rootBucket(tx)
			if err != nil {
				return err
			}
			return removeAllChildren(bkt, pathutil.CwdPath, &removed)
		}
		bkt, key, err := lookupParent(tx, name)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if sub := bkt.Bucket(key); sub != nil {
			if err := collectPaths(sub, name, &removed); err != nil {
				return err
			}
			removed = append(removed, name)
			return bkt.DeleteBucket(key)
		}
		if bkt.Get(key) == nil {
			return nil
		}
		removed = append(removed, name)
		return bkt.Delete(key)
	})
	if err != nil {
		return ufserrors.NewPathError("removeall", name, err)
	}
	for _, p := range removed {
		fsys.notify(types.NotifyRemove, p)
	}
	return nil
}

// removeAllChildren deletes every child of bkt (whose own path is prefix),
// leaving bkt itself (and its selfKey) intact, appending the full path of
// everything removed to *removed.
func removeAllChildren(bkt *bolt.Bucket, prefix string, removed *[]string) error {
	// Keys are collected first because bbolt does not allow mutating a bucket
	// while iterating it with ForEach.
	var keys [][]byte
	if err := bkt.ForEach(func(k, _ []byte) error {
		if !bytes.Equal(k, selfKey) {
			keys = append(keys, bytes.Clone(k))
		}
		return nil
	}); err != nil {
		return err
	}
	for _, k := range keys {
		full := joinPath(prefix, string(k))
		if sub := bkt.Bucket(k); sub != nil {
			if err := collectPaths(sub, full, removed); err != nil {
				return err
			}
			*removed = append(*removed, full)
			if err := bkt.DeleteBucket(k); err != nil {
				return err
			}
			continue
		}
		*removed = append(*removed, full)
		if err := bkt.Delete(k); err != nil {
			return err
		}
	}
	return nil
}
