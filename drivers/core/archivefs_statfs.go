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

// Stat and Lstat are the [fs.StatFS] half of [types.ReadFS]. An archive holds no symbolic links, so Lstat is Stat with another op in its error. A closed file system reports [fs.ErrClosed].

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *archiveFS) Stat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("stat", name); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	if fsys.closed.Load() {
		fsys.mu.RUnlock()
		return nil, ufserrors.NewPathError("stat", name, fs.ErrClosed)
	}
	info, err := fsys.fsys.Stat(name)
	fsys.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (fsys *archiveFS) Lstat(name string) (fs.FileInfo, error) {
	// Archives contain no symlinks, so Lstat == Stat. The error is relabeled
	// so it reports the caller's op, as other Lstat delegations do. No
	// lifetime lock is taken here: Stat holds it across its own use of the
	// archive, and Lstat must not hold it across this delegation, because a
	// reader that re-enters RLock while Close waits for the write lock
	// deadlocks against that Close.
	val, err := fsys.Stat(name)
	return ufserrors.ChangePathErrorOp(val, err, "lstat")
}
