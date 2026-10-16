// Copyright 2026 Jeremy Edwards
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

package ops

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
)

// List returns all paths (both files and directories) under dir in lexical
// order. The root directory "." is never included in the result. For
// files-only, prefer [ListFiles].
func List(fsys fs.FS, dir string) ([]string, error) {
	return list(fsys, dir, true)
}

// ListFiles returns the paths of all files (excluding directories) under dir in
// lexical order. If fsys implements [types.ListFilenames], its native implementation
// is used to avoid building intermediate [fs.FileInfo] values.
//
// This method may take a long time since it may traverse a large file system and build a large slice of paths in memory.
func ListFiles(fsys fs.FS, dir string) ([]string, error) {
	lf, ok := fsys.(types.ListFilenames)
	if ok {
		return lf.ListFilenames(dir)
	}
	return list(fsys, dir, false)
}

func list(fsys fs.FS, dir string, includeDirs bool) ([]string, error) {
	// WalkDir visits each path exactly once in lexical order, so no dedup map
	// or sort is needed.
	var items []string
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if pathutil.IsCwd(p) {
			return nil // never include "." itself; also tolerates missing root
		}
		if err != nil {
			return err
		}
		if includeDirs || !d.IsDir() {
			items = append(items, p)
		}
		return nil
	})
	return items, err
}
