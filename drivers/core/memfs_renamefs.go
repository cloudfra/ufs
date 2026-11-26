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

// Rename and the locked core it drives are the [types.RenameFS] half of [types.FS].

import (
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// Rename moves the node at oldPath, and every node below it, to newPath,
// creating missing parent directories of newPath as Create does.
func (fsys *memFS) Rename(oldPath, newPath string) error {
	if err := pathutil.ValidateRename(oldPath, newPath); err != nil {
		return err
	}
	fsys.mu.Lock()
	if fsys.nodes == nil {
		fsys.mu.Unlock()
		return ufserrors.NewLinkError("rename", oldPath, newPath, fs.ErrClosed)
	}
	moved, err := fsys.renameLocked(oldPath, newPath)
	fsys.mu.Unlock()
	if err != nil {
		return ufserrors.NewLinkError("rename", oldPath, newPath, err)
	}
	for _, oldKey := range moved {
		fsys.notify(types.NotifyRename, oldKey)
		fsys.notify(types.NotifyCreate, newPath+oldKey[len(oldPath):])
	}
	return nil
}

// renameLocked moves the nodes for Rename and returns their old paths.
// fsys.mu must be held for writing.
func (fsys *memFS) renameLocked(oldPath, newPath string) ([]string, error) {
	node, ok := fsys.nodes[oldPath]
	if !ok {
		return nil, fs.ErrNotExist
	}
	if _, ok := fsys.nodes[newPath]; ok {
		return nil, fs.ErrExist
	}
	if dir := path.Dir(newPath); dir != pathutil.CwdPath {
		if err := fsys.checkDirsLocked(dir); err != nil {
			return nil, err
		}
	}

	moved := []string{oldPath}
	if node.isDir {
		prefix := oldPath + "/"
		for key := range fsys.nodes {
			if strings.HasPrefix(key, prefix) {
				moved = append(moved, key)
			}
		}
	}
	for _, oldKey := range moved {
		fsys.nodes[newPath+oldKey[len(oldPath):]] = fsys.nodes[oldKey]
		delete(fsys.nodes, oldKey)
	}
	node.name = path.Base(newPath)
	fsys.ensureParentsLocked(newPath, time.Now())
	return moved, nil
}
