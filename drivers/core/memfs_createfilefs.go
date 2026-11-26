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

// Create is the [types.CreateFileFS] half of [types.WriteFS].

import (
	"fmt"
	"io/fs"
	"path"
	"time"

	"github.com/cloudfra/ufs/drivers/common/buffile"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *memFS) Create(name string) (types.File, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("create", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("create", name); err != nil {
		return nil, err
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()

	existing, existed := fsys.nodes[name]
	if existed && existing.isDir {
		return nil, ufserrors.NewPathError("create", name, fmt.Errorf("is a directory: %w", fs.ErrInvalid))
	}
	if dir := path.Dir(name); dir != pathutil.CwdPath {
		if err := fsys.checkDirsLocked(dir); err != nil {
			return nil, ufserrors.NewPathError("create", name, err)
		}
	}

	now := time.Now()
	node := &memNode{
		name:    path.Base(name),
		mode:    fs.ModePerm,
		modTime: now,
	}
	fsys.nodes[name] = node
	fsys.ensureParentsLocked(name, now)

	if existed {
		fsys.notify(types.NotifyWrite, name)
	} else {
		fsys.notify(types.NotifyCreate, name)
	}

	return &memFile{
		fsys: fsys,
		File: buffile.New(name, nil, node.mode, node.modTime),
	}, nil
}
