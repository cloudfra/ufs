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

package keyguardfs

// The [fs.ReadDirFS] half of a guard, which is where a guard that hides names
// does most of its work: a listing is the only place a hidden file can show up
// without an operation naming it.

import (
	"io"
	"io/fs"
	"path"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
)

// hiddenDirFile is the handle of a directory, with the methods a guard keeps
// when it filters a listing.
type hiddenDirFile interface {
	types.File
	fs.ReadDirFile
}

// hiddenDirHandle answers ReadDir from one filtered snapshot of the listing,
// tracking the position that the guarded handle does not. The snapshot keeps a
// call with a count limit from losing the entries behind a run of hidden ones.
type hiddenDirHandle struct {
	hiddenDirFile
	guard   *keyGuardFS
	dir     string
	entries []fs.DirEntry
	off     int
}

func (dir *hiddenDirHandle) ReadDir(count int) ([]fs.DirEntry, error) {
	if dir.entries == nil {
		entries, err := dir.hiddenDirFile.ReadDir(-1)
		if err != nil {
			return nil, err
		}
		dir.entries = dir.guard.visible(dir.dir, entries)
	}
	rest := dir.entries[dir.off:]
	if count <= 0 {
		dir.off = len(dir.entries)
		return rest, nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if count < len(rest) {
		rest = rest[:count]
	}
	dir.off += len(rest)

	return rest, nil
}

func (fsys *keyGuardFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := pathutil.Validate("readdir", name); err != nil {
		return nil, err
	}
	if fsys.hiding() && fsys.protected(name) {
		return nil, fsys.denied("readdir", name)
	}

	entries, err := fsys.inner.ReadDir(name)
	if err != nil {
		return nil, err
	}
	if !fsys.hiding() {
		return entries, nil
	}

	return fsys.visible(name, entries), nil
}

// visible returns the entries of the listing of dir that this guard leaves
// visible, in the order the guarded file system reported them. A hidden file
// keeps its place in the tree, so only the listings naming it change.
func (fsys *keyGuardFS) visible(dir string, entries []fs.DirEntry) []fs.DirEntry {
	visible := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if !fsys.protected(path.Join(dir, entry.Name())) {
			visible = append(visible, entry)
		}
	}

	return visible
}
