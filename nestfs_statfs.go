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

// Stat and Lstat are the [fs.StatFS] half of [types.ReadFS], asked of the file
// system that holds name.

import (
	"errors"
	"io/fs"
	"log/slog"
	"path"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *nestFS) Stat(name string) (fs.FileInfo, error) {
	if err := fsys.validPath("stat", name); err != nil {
		return nil, err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	if cFsys, ok := mountFS.fsys.(fs.StatFS); ok {
		// The closed check and the stat of the file system holding name are
		// one critical section with respect to that nest's Close.
		mountFS.mu.RLock()
		if mountFS.closed.Load() {
			mountFS.mu.RUnlock()
			return nil, ufserrors.NewPathError("stat", name, fs.ErrClosed)
		}
		info, err := cFsys.Stat(subName)
		mountFS.mu.RUnlock()
		if errors.Is(err, fs.ErrNotExist) && mountFS.isMountParent(subName) {
			return types.NewDirEntry(path.Base(subName)), nil
		}
		return info, rewritePathError(err, name, subName)
	}
	// The fallback opens through the nest, which guards the base at its own
	// use site; no lock is held across it.
	f, err := mountFS.Open(subName)
	if err != nil {
		return nil, rewritePathError(err, name, subName)
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("failed to close file during Stat", "path", name, "error", err)
		}
	}()
	info, err := f.Stat()
	return info, rewritePathError(err, name, subName)
}

func (fsys *nestFS) Lstat(name string) (fs.FileInfo, error) {
	if err := fsys.validPath("lstat", name); err != nil {
		return nil, err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	if cFsys, ok := mountFS.fsys.(fs.ReadLinkFS); ok {
		// One critical section with the nest's Close, as in Stat above.
		mountFS.mu.RLock()
		if mountFS.closed.Load() {
			mountFS.mu.RUnlock()
			return nil, ufserrors.NewPathError("lstat", name, fs.ErrClosed)
		}
		info, err := cFsys.Lstat(subName)
		mountFS.mu.RUnlock()
		if errors.Is(err, fs.ErrNotExist) && mountFS.isMountParent(subName) {
			return types.NewDirEntry(path.Base(subName)), nil
		}
		return info, rewritePathError(err, name, subName)
	}
	// The fallback asks the nest, which guards the base at its own use site.
	info, err := mountFS.Stat(subName)
	return info, rewritePathError(err, name, subName)
}
