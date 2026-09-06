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
	"log/slog"
	"path"
)

// Rsync copies all files under dir from srcFS into destFS, preserving the
// relative path structure. Parent directories in destFS are created with
// [fs.ModePerm] as needed. Existing files in destFS are overwritten. The copy
// is not atomic: if an error occurs mid-walk, destFS may be partially written.
//
// dir must satisfy [fs.ValidPath]; use "." to copy the entire file system.
func Rsync(srcFS fs.FS, destFS FS, dir string) error {
	// TODO: Prevent archive traversal.
	return ForEachFilename(srcFS, dir, func(name string) error {
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
	defer func() {
		if err := sfp.Close(); err != nil {
			slog.Error("failed to close source file", "path", srcFilename, "error", err)
		}
	}()

	dfp, err := destFS.Create(destFilename)
	if err != nil {
		return err
	}
	defer func() {
		if err := dfp.Close(); err != nil {
			slog.Error("failed to close destination file", "path", destFilename, "error", err)
		}
	}()
	if _, err := io.Copy(dfp, sfp); err != nil {
		return err
	}
	return nil
}

// ForEachFilename calls f for each file path (not directory) under dir,
// streaming results without building an intermediate slice. If fsys implements
// [ForEachFilenameIter], its native implementation is used directly; otherwise
// the paths are collected via [fs.WalkDir] and iterated. f receives paths
// relative to dir. The walk stops and returns the first non-nil error from f.
func ForEachFilename(fsys fs.FS, dir string, f func(string) error) error {
	lf, ok := fsys.(ForEachFilenameIter)
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
// a native [ForEachFileInfoIter] implementation when available, falling back to
// [fs.WalkDir]. The walk stops and returns the first non-nil error from f.
func ForEachFileInfo(fsys fs.FS, dir string, f func(fs.FileInfo) error) error {
	lf, ok := fsys.(ForEachFileInfoIter)
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
	if isCwd(name) {
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

// archiveDirChecker is implemented by nestFS to identify virtual archive-mount
// directories without exposing the concrete type.
type archiveDirChecker interface {
	isMountedArchiveDir(name string) bool
}

// WalkArgs configures traversal behavior for [Walk].
type WalkArgs struct {
	// IncludeMountedArchive controls whether virtual archive-mount directories
	// (e.g. "data.zip.d") are descended into during the walk. When false
	// (the default), such directories are skipped entirely.
	IncludeMountedArchive bool

	// ExcludeDirectory is a list of glob patterns matched against each
	// directory's base name using [path.Match]. Directories whose names match
	// any pattern are skipped along with all their contents. A nil or empty
	// slice applies no filter.
	ExcludeDirectory []string
}

// Walk walks dir in fsys, calling f for each file whose ancestor directories
// pass the filters in args. It differs from [ForEachFilename] in two ways:
// virtual archive-mount directories (e.g. "data.zip.d") are skipped by default
// (set [WalkArgs.IncludeMountedArchive] to descend into them), and directories
// whose base names match any [WalkArgs.ExcludeDirectory] glob are skipped
// entirely. The walk stops and returns the first non-nil error from f.
func Walk(fsys fs.FS, dir string, args WalkArgs, f func(string) error) error {
	adc, ok := fsys.(archiveDirChecker)
	return fs.WalkDir(fsys, dir, func(name string, d fs.DirEntry, err error) error {
		if isCwd(name) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			if !args.IncludeMountedArchive && ok && adc.isMountedArchiveDir(name) {
				return fs.SkipDir
			}
			for _, pattern := range args.ExcludeDirectory {
				if matched, _ := path.Match(pattern, d.Name()); matched {
					return fs.SkipDir
				}
			}
			return nil
		}
		return f(name)
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
//
// This method may take a long time since it may traverse a large file system and build a large slice of paths in memory.
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

// Remove removes the file or empty directory at name in fsys.
// If fsys implements [Remover], its Remove method is used directly.
// Otherwise Remove returns [fs.ErrPermission] wrapped in an [fs.PathError].
func Remove(fsys fs.FS, name string) error {
	r, ok := fsys.(Remover)
	if !ok {
		return pathError("remove", name, fs.ErrPermission)
	}
	return r.Remove(name)
}

// RemoveAll removes name and everything beneath it in fsys.
// If fsys implements [Remover], its RemoveAll method is used directly.
// Otherwise RemoveAll returns [fs.ErrPermission] wrapped in an [fs.PathError].
func RemoveAll(fsys fs.FS, name string) error {
	r, ok := fsys.(Remover)
	if !ok {
		return pathError("removeall", name, fs.ErrPermission)
	}
	return r.RemoveAll(name)
}
