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

//go:build windows

package ufs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	_ ForEachFilenameIter = (*localFS)(nil)
	_ ForEachFileInfoIter = (*localFS)(nil)
	_ ListFilenames       = (*localFS)(nil)
)

// localFSNormalizePath strips the "file://" URI prefix and converts a
// "/C:/path" form (left after stripping "file://" from "file:///C:/path") to
// the Windows-native "C:\path" form required by filepath.Abs.
func localFSNormalizePath(name string) string {
	if after, ok := strings.CutPrefix(name, "file://"); ok {
		name = after
	} else {
		name = strings.TrimPrefix(name, "file:")
	}
	// "/C:/path/..." → "C:\path\..." (strip leading slash, convert separators)
	if len(name) >= 3 && name[0] == '/' && name[2] == ':' {
		name = filepath.FromSlash(name[1:])
	}
	return name
}

// validLocalPath extends validPath by also rejecting backslash paths on Windows,
// since os.Root accepts them as separators but fs.FS requires forward slashes only.
func validLocalPath(op, name string) error {
	if err := validPath(op, name); err != nil {
		return err
	}
	if strings.Contains(name, windowsPathSeparator) {
		return pathError(op, name, fs.ErrInvalid)
	}
	return nil
}

// localFSFile wraps *os.File to normalize directory sizes via Stat().
// On Windows, os.Root.Stat() returns size=4096 for directories while ReadDir
// entries return size=0, causing fstest.TestFS consistency checks to fail.
type localFSFile struct {
	*os.File
}

func (f *localFSFile) Stat() (fs.FileInfo, error) {
	fi, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return localFSNormalizeDirInfo(fi), nil
}

func localFSWrapFile(f *os.File) fs.File {
	return &localFSFile{f}
}

// localFSNormalizeDirInfo normalizes directory size to 0 on Windows.
// os.Root.Lstat/Stat reports size=4096 for directories but ReadDir entries
// report size=0, causing fstest.TestFS consistency checks to fail.
func localFSNormalizeDirInfo(fi fs.FileInfo) fs.FileInfo {
	if !fi.IsDir() || fi.Size() == 0 {
		return fi
	}
	return &fsInfo{
		name:    fi.Name(),
		size:    0,
		mode:    fi.Mode(),
		modTime: fi.ModTime(),
		isDir:   true,
		sys:     fi.Sys(),
	}
}

func (fsys *localFS) mftScanPaths(dir string) ([]string, error) {
	scanner, err := newMFTScanner(fsys.osFS.Name())
	if err != nil {
		return nil, errMFTUnavailable
	}
	records, err := scanner.enumSubtree()
	if err != nil {
		// Any enumSubtree failure (access denied, USN journal inactive, etc.)
		// signals that MFT is unusable for this FS; fall back to WalkDir.
		return nil, errMFTUnavailable
	}
	paths := scanner.buildPaths(records)
	if len(paths) == 0 {
		// MFT scan succeeded but returned no files — USN journal may be
		// inactive or the volume root reference didn't match. Fall back to
		// WalkDir so callers still see correct results.
		return nil, errMFTUnavailable
	}
	if isCwd(dir) {
		return paths, nil
	}
	prefix := dir + "/"
	var filtered []string
	for _, p := range paths {
		if strings.HasPrefix(p, prefix) {
			filtered = append(filtered, p)
		}
	}
	return filtered, nil
}

func (fsys *localFS) ForEachFilename(dir string, f func(string) error) error {
	paths, err := fsys.mftScanPaths(dir)
	if errors.Is(err, errMFTUnavailable) {
		return fsys.forEachFilenameWalkDir(dir, f)
	}
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := f(p); err != nil {
			return err
		}
	}
	return nil
}

func (fsys *localFS) forEachFilenameWalkDir(dir string, f func(string) error) error {
	return fs.WalkDir(fsys, dir, func(name string, d fs.DirEntry, err error) error {
		if skip, err := excludeDirs(name, d, err); skip {
			return err
		}
		return f(name)
	})
}

func (fsys *localFS) ForEachFileInfo(dir string, f func(fs.FileInfo) error) error {
	paths, err := fsys.mftScanPaths(dir)
	if errors.Is(err, errMFTUnavailable) {
		return fsys.forEachFileInfoWalkDir(dir, f)
	}
	if err != nil {
		return err
	}
	sort.Strings(paths)
	absRoot := fsys.osFS.Name()
	for _, p := range paths {
		info, statErr := os.Stat(filepath.Join(absRoot, filepath.FromSlash(p)))
		if statErr != nil {
			return statErr
		}
		if err := f(info); err != nil {
			return err
		}
	}
	return nil
}

func (fsys *localFS) forEachFileInfoWalkDir(dir string, f func(fs.FileInfo) error) error {
	return fs.WalkDir(fsys, dir, func(name string, d fs.DirEntry, err error) error {
		if skip, err := excludeDirs(name, d, err); skip {
			return err
		}
		info, statErr := fs.Stat(fsys, name)
		if statErr != nil {
			return statErr
		}
		return f(info)
	})
}

func (fsys *localFS) ListFilenames(dir string) ([]string, error) {
	paths, err := fsys.mftScanPaths(dir)
	if errors.Is(err, errMFTUnavailable) {
		return fsys.listFilenamesWalkDir(dir)
	}
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func (fsys *localFS) listFilenamesWalkDir(dir string) ([]string, error) {
	var items []string
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if isCwd(p) {
			return nil
		}
		if err != nil {
			return err
		}
		if !d.IsDir() {
			items = append(items, p)
		}
		return nil
	})
	return items, err
}
