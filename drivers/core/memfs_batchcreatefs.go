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

// BatchCreate with the batch event type only it uses is the [types.BatchCreateFS] half, which [types.WriteFS] does not ask for.

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// batchCreateOp is the op of the errors of BatchCreate, as in [ufs.BatchCreate].
const batchCreateOp = "batchcreate"

// batchEvent is a notification a batch queues while its lock is held, so that
// watchers are called only after the batch is committed.
type batchEvent struct {
	op   types.NotifyOp
	path string
}

// BatchCreate writes every file under one lock, so readers see none or all
// of them. A file that fails restores every node the batch changed.
func (fsys *memFS) BatchCreate(files []types.BatchFile) error {
	if err := types.ValidateBatchFiles(batchCreateOp, files); err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	now := time.Now()
	var events []batchEvent
	// undo holds the node each changed key had before the batch, nil if it
	// had none.
	undo := map[string]*memNode{}
	set := func(key string, node *memNode) {
		if _, ok := undo[key]; !ok {
			undo[key] = fsys.nodes[key]
		}
		fsys.nodes[key] = node
	}

	fsys.mu.Lock()
	if fsys.nodes == nil {
		fsys.mu.Unlock()
		return ufserrors.NewPathError(batchCreateOp, files[0].Name, fs.ErrClosed)
	}
	for i := range files {
		file := &files[i]
		existing, existed := fsys.nodes[file.Name]
		var err error
		if existed && existing.isDir {
			err = fmt.Errorf("is a directory: %w", fs.ErrInvalid)
		} else if dir := path.Dir(file.Name); dir != pathutil.CwdPath {
			err = fsys.checkDirsLocked(dir)
		}
		if err != nil {
			for key, node := range undo {
				if node == nil {
					delete(fsys.nodes, key)
				} else {
					fsys.nodes[key] = node
				}
			}
			fsys.mu.Unlock()
			return ufserrors.NewPathError(batchCreateOp, file.Name, err)
		}

		for dir := range parentDirs(file.Name) {
			if _, ok := fsys.nodes[dir]; !ok {
				set(dir, &memNode{name: path.Base(dir), mode: fs.ModeDir | fs.ModePerm, modTime: now, isDir: true})
				events = append(events, batchEvent{types.NotifyCreate, dir})
			}
		}
		perm := file.Perm.Perm()
		if perm == 0 {
			perm = fs.ModePerm
		}
		set(file.Name, &memNode{name: path.Base(file.Name), content: bytes.Clone(file.Data), mode: perm, modTime: now})
		if existed {
			events = append(events, batchEvent{types.NotifyWrite, file.Name})
		} else {
			events = append(events, batchEvent{types.NotifyCreate, file.Name})
		}
	}
	fsys.mu.Unlock()
	for _, e := range events {
		fsys.notify(e.op, e.path)
	}
	return nil
}
