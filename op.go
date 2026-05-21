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

package ufs

import (
	"io"
	"io/fs"
	"path"
	"strings"
)

// Rsync copies all files under dir from srcFS into destFS, preserving the
// relative path structure. Parent directories in destFS are created with
// [fs.ModePerm] as needed. Existing files in destFS are overwritten. The copy
// is not atomic: if an error occurs mid-walk, destFS may be partially written.
//
// dir must satisfy [fs.ValidPath]; use "." to copy the entire file system.
func Rsync(srcFS fs.FS, destFS FS, dir string) error {
	return fs.WalkDir(srcFS, dir, func(p string, d fs.DirEntry, err error) error {
		if isCwd(p) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip virtual archive-mount directories (foo.zip.d) to prevent
			// archive contents from being silently extracted into destFS.
			name := d.Name()
			if strings.HasSuffix(name, archiveDirExt) && isMountableArchivePath(strings.TrimSuffix(name, archiveDirExt)) {
				return fs.SkipDir
			}
			return nil
		}
		dirPart, _ := path.Split(p)
		if err := destFS.MkdirAll(path.Clean(dirPart), fs.ModePerm); err != nil {
			return err
		}
		return Copy(srcFS, p, destFS, p)
	})
}

// Copy copies the single file at srcFilename in srcFS to destFilename in destFS.
// The parent directory of destFilename must already exist. The destination file
// is created (or truncated) via [FS.Create].
func Copy(srcFS fs.FS, srcFilename string, destFS FS, destFilename string) error {
	sfp, err := srcFS.Open(srcFilename)
	if err != nil {
		return err
	}
	defer sfp.Close()

	dfp, err := destFS.Create(destFilename)
	if err != nil {
		return err
	}
	defer dfp.Close()

	if _, err := io.Copy(dfp, sfp); err != nil {
		return err
	}
	return nil
}

// ForEachFilename calls f for each file path (not directory) under dir,
// streaming results without building an intermediate slice. If fsys implements
// [ForEachFilenameIter], its native implementation is used directly; otherwise
// the paths are collected via [ListFiles] and iterated. f receives paths
// relative to dir. The walk stops and returns the first non-nil error from f.
func ForEachFilename(fsys fs.FS, dir string, f func(string) error) error {
	lf, ok := fsys.(ForEachFilenameIter)
	if ok {
		return lf.ForEachFilename(dir, f)
	}
	files, err := ListFiles(fsys, dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := f(file); err != nil {
			return err
		}
	}
	return nil
}

// ForEachFileInfo calls f for each file (not directory) under dir, providing
// its [fs.FileInfo]. It is the typed companion to [ForEachFilename] and prefers
// a native [ForEachFileInfoIter] implementation when available, falling back to
// [fs.WalkDir]. The walk stops and returns the first non-nil error from f.
func ForEachFileInfo(fsys fs.FS, dir string, f func(fs.FileInfo) error) error {
	lf, ok := fsys.(ForEachFileInfoIter)
	if ok {
		return lf.ForEachFileInfo(dir, f)
	}
	return fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if isCwd(p) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := fs.Stat(fsys, p)
		if err != nil {
			return err
		}
		return f(info)
	})
}

// List returns all paths (both files and directories) under dir in lexical
// order. The root directory "." is never included in the result. For
// files-only, prefer [ListFiles].
func List(fsys fs.FS, dir string) ([]string, error) {
	return list(fsys, dir, true)
}

// ListFiles returns the paths of all files (excluding directories) under dir in
// lexical order. If fsys implements [ListFilenames], its native implementation
// is used to avoid building intermediate [fs.FileInfo] values.
func ListFiles(fsys fs.FS, dir string) ([]string, error) {
	lf, ok := fsys.(ListFilenames)
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
		if isCwd(p) {
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
