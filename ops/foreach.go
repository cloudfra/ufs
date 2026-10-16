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

// ForEachFilename calls f for each file path (not directory) under dir,
// streaming results without building an intermediate slice. If fsys implements
// [types.ForEachFilenameIter], its native implementation is used directly; otherwise
// the paths are collected via [fs.WalkDir] and iterated. f receives paths
// relative to dir. The walk stops and returns the first non-nil error from f.
func ForEachFilename(fsys fs.FS, dir string, f func(string) error) error {
	lf, ok := fsys.(types.ForEachFilenameIter)
	if ok {
		return lf.ForEachFilename(dir, f)
	}
	return fs.WalkDir(fsys, dir, func(name string, d fs.DirEntry, err error) error {
		if skip, err := excludeDirs(name, d, err); skip {
			return err
		}
		return f(name)
	})
}

// ForEachFileInfo calls f for each file (not directory) under dir, providing
// its [fs.FileInfo]. It is the typed companion to [ForEachFilename] and prefers
// a native [types.ForEachFileInfoIter] implementation when available, falling back to
// [fs.WalkDir]. The walk stops and returns the first non-nil error from f.
func ForEachFileInfo(fsys fs.FS, dir string, f func(fs.FileInfo) error) error {
	lf, ok := fsys.(types.ForEachFileInfoIter)
	if ok {
		return lf.ForEachFileInfo(dir, f)
	}
	return fs.WalkDir(fsys, dir, func(name string, d fs.DirEntry, err error) error {
		if skip, err := excludeDirs(name, d, err); skip {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return f(info)
	})
}

func excludeDirs(name string, d fs.DirEntry, err error) (bool, error) {
	if pathutil.IsCwd(name) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	if d.IsDir() {
		return true, nil
	}
	return false, nil
}
