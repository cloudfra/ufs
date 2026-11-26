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

// Stat and Lstat are the [fs.StatFS] half of [types.ReadFS], over the one lookup they share. A stored path is never a symbolic link, so Lstat reads what Stat reads.

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	bolt "go.etcd.io/bbolt"
)

func (fsys *boltFS) Stat(name string) (fs.FileInfo, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("stat", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("stat", name); err != nil {
		return nil, err
	}
	return fsys.statPath("stat", name)
}

func (fsys *boltFS) Lstat(name string) (fs.FileInfo, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("lstat", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("lstat", name); err != nil {
		return nil, err
	}
	return fsys.statPath("lstat", name)
}

func (fsys *boltFS) statPath(op, name string) (fs.FileInfo, error) {
	var info fs.FileInfo
	err := fsys.view(func(tx *bolt.Tx) error {
		if name == pathutil.CwdPath {
			bkt, err := rootBucket(tx)
			if err != nil {
				return err
			}
			mode, modTime, err := dirMeta(bkt)
			if err != nil {
				return err
			}
			info = types.NewFileInfo(pathutil.CwdPath, osutil.EmptyDirSize, mode, modTime)
			return nil
		}
		bkt, key, err := lookupParent(tx, name)
		if err != nil {
			return err
		}
		if sub := bkt.Bucket(key); sub != nil {
			mode, modTime, err := dirMeta(sub)
			if err != nil {
				return err
			}
			info = types.NewFileInfo(string(key), osutil.EmptyDirSize, mode, modTime)
			return nil
		}
		data := bkt.Get(key)
		if data == nil {
			return fs.ErrNotExist
		}
		rec, err := decodeBoltRecord(data)
		if err != nil {
			return err
		}
		info = types.NewFileInfo(string(key), int64(len(rec.content)), rec.mode, rec.modTime)
		return nil
	})
	if err != nil {
		return nil, ufserrors.NewPathError(op, name, err)
	}
	return info, nil
}
