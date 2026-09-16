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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
