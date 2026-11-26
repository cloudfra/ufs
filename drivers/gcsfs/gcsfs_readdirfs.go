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

package gcsfs

// ReadDir is the [fs.ReadDirFS] half of [types.ReadFS]: a virtual directory is whatever lies under an object prefix.

import (
	"io/fs"
	"log/slog"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *gcsFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != pathutil.CwdPath {
		if err := pathutil.Validate("readdir", name); err != nil {
			return nil, err
		}
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("failed to close file during ReadDir", "path", name, "error", err)
		}
	}()
	gf := f.(*gcsFile)
	if !gf.isDir {
		return nil, ufserrors.NewPathError("readdir", name, fs.ErrInvalid)
	}
	return gf.ReadDir(-1)
}
