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

// ReadFile is the [fs.ReadFileFS] half of [types.ReadFS].

import (
	"bytes"
	"fmt"
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	bolt "go.etcd.io/bbolt"
)

func (fsys *boltFS) ReadFile(name string) ([]byte, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("readfile", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("readfile", name); err != nil {
		return nil, err
	}
	if name == pathutil.CwdPath {
		return nil, ufserrors.NewPathError("readfile", name, fmt.Errorf("%w: %w", errIsDirectory, fs.ErrInvalid))
	}
	var content []byte
	err := fsys.view(func(tx *bolt.Tx) error {
		bkt, key, err := lookupParent(tx, name)
		if err != nil {
			return err
		}
		if bkt.Bucket(key) != nil {
			return fmt.Errorf("%w: %w", errIsDirectory, fs.ErrInvalid)
		}
		data := bkt.Get(key)
		if data == nil {
			return fs.ErrNotExist
		}
		rec, err := decodeBoltRecord(data)
		if err != nil {
			return err
		}
		content = bytes.Clone(rec.content)
		return nil
	})
	if err != nil {
		return nil, ufserrors.NewPathError("readfile", name, err)
	}
	return content, nil
}
