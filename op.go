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
	return ForEachFilename(srcFS, dir, false, func(name string) error {
		dir, _ := path.Split(name)
		dir = path.Clean(dir)
		if err := destFS.MkdirAll(dir, fs.ModePerm); err != nil {
			return err
		}
		if err := Copy(srcFS, name, destFS, name); err != nil {
			return err
		}
		return nil
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

// ForEachFilename calls f for each file path (not directory) under dir. When
// traverseArchives is true, virtual archive mounts (*.d directories) are traversed; if fsys
// implements [ForEachFilenameIter] its native implementation is used, otherwise
// paths are collected via [ListFiles] and iterated. When traverseArchives is false, *.d
// directories whose adjacent archive file exists are skipped; real *.d
// configuration directories are still traversed. The walk stops and returns the
// first non-nil error from f.
func ForEachFilename(fsys fs.FS, dir string, traverseArchives bool, f func(string) error) error {
	if traverseArchives {
		if lf, ok := fsys.(ForEachFilenameIter); ok {
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
	return fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if isCwd(p) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			if isVirtualArchiveMount(fsys, p) {
				return fs.SkipDir
			}
			return nil
		}
		return f(p)
	})
}

// ForEachFileInfo calls f for each file (not directory) under dir, providing
// its [fs.FileInfo]. When traverseArchives is true, virtual archive mounts are traversed;
// if fsys implements [ForEachFileInfoIter] its native implementation is used,
// otherwise [fs.WalkDir] is used. When traverseArchives is false, *.d directories whose
// adjacent archive file exists are skipped; real *.d configuration directories
// are still traversed. The walk stops and returns the first non-nil error from f.
func ForEachFileInfo(fsys fs.FS, dir string, traverseArchives bool, f func(fs.FileInfo) error) error {
	if traverseArchives {
		if lf, ok := fsys.(ForEachFileInfoIter); ok {
			return lf.ForEachFileInfo(dir, f)
		}
	}
	return fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if isCwd(p) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			if !traverseArchives && isVirtualArchiveMount(fsys, p) {
				return fs.SkipDir
			}
			return nil
		}
		info, err := fs.Stat(fsys, p)
		if err != nil {
			return err
		}
		return f(info)
	})
}

// isVirtualArchiveMount reports whether p is a *.d directory in fsys that has a
// corresponding adjacent archive file. This distinguishes virtual archive mounts
// created by nestFS from real *.d configuration directories (e.g. apt sources).
func isVirtualArchiveMount(fsys fs.FS, p string) bool {
	if !strings.HasSuffix(p, archiveDirExt) {
		return false
	}
	archivePath := strings.TrimSuffix(p, archiveDirExt)
	if !isMountableArchivePath(archivePath) {
		return false
	}
	info, err := fs.Stat(fsys, archivePath)
	if err != nil {
		return false
	}
	return !info.IsDir()
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
