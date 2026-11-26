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

package ufs

// ReadDir is the [fs.ReadDirFS] half of [types.ReadFS]: the listing of the
// file system holding name, with the nest's own mount directories added when
// that file system cannot report them.

import (
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *nestFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := fsys.validPath("readdir", name); err != nil {
		return nil, err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	if cFsys, ok := mountFS.fsys.(fs.ReadDirFS); ok {
		dirs, err := cFsys.ReadDir(subName)
		return mountFS.appendDirEntry(subName, dirs, rewritePathError(err, name, subName))
	}

	f, err := mountFS.fsys.Open(subName)
	if err != nil {
		return nil, rewritePathError(err, name, subName)
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("failed to close file during ReadDir", "path", name, "error", err)
		}
	}()

	readDirFile, ok := f.(fs.ReadDirFile)
	if !ok {
		return nil, ufserrors.NewPathError("readdir", name, fmt.Errorf("%s is not a directory", name))
	}

	dirs, err := readDirFile.ReadDir(-1)
	return mountFS.appendDirEntry(subName, dirs, rewritePathError(err, name, subName))
}
