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

// Rename is the [types.RenameFS] half of [types.FS].

import (
	"errors"
	"io/fs"
	"os"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// Rename moves oldPath to newPath with os.Root.Rename, which would replace an
// existing newPath, so newPath is checked first. A newPath that is oldPath
// under another spelling, as on a case-insensitive disk, is allowed.
func (fsys *localFS) Rename(oldPath, newPath string) error {
	if err := validLocalPath("rename", oldPath); err != nil {
		return err
	}
	if err := validLocalPath("rename", newPath); err != nil {
		return err
	}
	if err := pathutil.ValidateRename(oldPath, newPath); err != nil {
		return err
	}
	if newInfo, err := fsys.osFS.Lstat(newPath); err == nil {
		oldInfo, err := fsys.osFS.Lstat(oldPath)
		if err != nil {
			return ufserrors.NewLinkError("rename", oldPath, newPath, err)
		}
		if oldPath == newPath || !os.SameFile(oldInfo, newInfo) {
			return ufserrors.NewLinkError("rename", oldPath, newPath, fs.ErrExist)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ufserrors.NewLinkError("rename", oldPath, newPath, err)
	}
	return fsys.osFS.Rename(oldPath, newPath)
}
