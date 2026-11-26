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

// Create is the [types.CreateFileFS] half of [types.WriteFS]. The handle it returns stays beside Open, where every other handle lives.

import (
	"fmt"
	"io/fs"
	"time"

	"github.com/cloudfra/ufs/drivers/common/buffile"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	bolt "go.etcd.io/bbolt"
)

func (fsys *boltFS) Create(name string) (types.File, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("create", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("create", name); err != nil {
		return nil, err
	}
	if name == pathutil.CwdPath {
		return nil, ufserrors.NewPathError("create", name, fmt.Errorf("%w: %w", errIsDirectory, fs.ErrInvalid))
	}

	now := time.Now()
	mode := fs.ModePerm
	record, err := encodeBoltRecord(mode, now, nil)
	if err != nil {
		return nil, ufserrors.NewPathError("create", name, err)
	}
	var existed bool
	err = fsys.update(func(tx *bolt.Tx) error {
		bkt, key, err := parentBucket(tx, name)
		if err != nil {
			return err
		}
		if bkt.Bucket(key) != nil {
			return fmt.Errorf("%w: %w", errIsDirectory, fs.ErrInvalid)
		}
		existed = bkt.Get(key) != nil
		return bkt.Put(key, record)
	})
	if err != nil {
		return nil, ufserrors.NewPathError("create", name, err)
	}

	if existed {
		fsys.notify(types.NotifyWrite, name)
	} else {
		fsys.notify(types.NotifyCreate, name)
	}

	return &boltFile{
		fsys: fsys,
		File: buffile.New(name, nil, mode, now),
	}, nil
}
