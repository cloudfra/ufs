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

package core

// MkdirAll is the [types.MkdirAllFS] half of [types.WriteFS].

import (
	"io/fs"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *memFS) MkdirAll(name string, perm fs.FileMode) error {
	if fsys.isClosed() {
		return ufserrors.NewPathError("mkdir", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("mkdir", name); err != nil {
		return err
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()

	if name != pathutil.CwdPath {
		if err := fsys.checkDirsLocked(name); err != nil {
			return ufserrors.NewPathError("mkdir", name, err)
		}
	}

	now := time.Now()
	parts := pathutil.Split(name)
	accum := ""
	var created []string
	for i, part := range parts {
		if part == "" {
			continue
		}
		if i > 0 {
			accum += "/"
		}
		accum += part
		if _, ok := fsys.nodes[accum]; !ok {
			fsys.nodes[accum] = &memNode{
				name:    part,
				mode:    fs.ModeDir | perm,
				modTime: now,
				isDir:   true,
			}
			created = append(created, accum)
		}
	}
	for _, p := range created {
		fsys.notify(types.NotifyCreate, p)
	}
	return nil
}
