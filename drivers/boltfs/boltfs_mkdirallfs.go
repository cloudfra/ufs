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

// MkdirAll is the [types.MkdirAllFS] half of [types.WriteFS]: one directory per bucket, each carrying its own record.

import (
	"io/fs"
	"strings"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	bolt "go.etcd.io/bbolt"
)

func (fsys *boltFS) MkdirAll(name string, perm fs.FileMode) error {
	if fsys.isClosed() {
		return ufserrors.NewPathError("mkdir", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("mkdir", name); err != nil {
		return err
	}
	if name == pathutil.CwdPath {
		// The root always exists.
		return nil
	}

	now := time.Now()
	var created []string
	err := fsys.update(func(tx *bolt.Tx) error {
		bkt, err := rootBucket(tx)
		if err != nil {
			return err
		}
		end := 0
		for part := range strings.SplitSeq(name, pathutil.UnixSeparator) {
			if end > 0 {
				end++
			}
			end += len(part)
			var isNew bool
			if bkt, isNew, err = createDirBucket(bkt, []byte(part), fs.ModeDir|perm, now); err != nil {
				return err
			}
			if isNew {
				created = append(created, name[:end])
			}
		}
		return nil
	})
	if err != nil {
		return ufserrors.NewPathError("mkdir", name, err)
	}
	for _, p := range created {
		fsys.notify(types.NotifyCreate, p)
	}
	return nil
}
