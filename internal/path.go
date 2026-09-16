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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	unixPathSeparator         = "/"
	windowsPathSeparator      = "\\"
	cwdPath                   = "."
	unixAndWindowsSlashCutset = unixPathSeparator + windowsPathSeparator
	emptyDirSize              = 0
)

func removePathPrefix(name string, removePath string) (string, bool) {
	removePath = path.Clean(removePath)
	name = path.Clean(name)
	if isCwd(removePath) {
		return name, true
	}
	if removePath == name {
		return cwdPath, true
	}
	return strings.CutPrefix(name, removePath+unixPathSeparator)
}

func trimSlash(name string) string {
	return strings.Trim(name, unixAndWindowsSlashCutset)
}

func splitPath(name string) []string {
	return strings.Split(trimSlash(name), unixPathSeparator)
}

func validPath(op string, name string) error {
	if !fs.ValidPath(name) {
		return pathError(op, name, fmt.Errorf("%q is not a valid path for %s, %w", name, runtime.GOOS, fs.ErrInvalid))
	}
	return nil
}

func coerceUnix(name string) string {
	return strings.ReplaceAll(name, windowsPathSeparator, unixPathSeparator)
}

func isDirName(name string) bool {
	return isCwd(name) || strings.HasSuffix(name, unixPathSeparator)
}

func isCwd(name string) bool {
	return name == "" || name == cwdPath
}

func pathError(op string, name string, err error) error {
	return &fs.PathError{
		Op:   op,
		Path: name,
		Err:  err,
	}
}

// joinErrors returns nil if all errs are nil, returns the single non-nil error
// directly (without wrapping) if exactly one is non-nil, or errors.Join when
// multiple are non-nil. This avoids the join wrapper overhead and the change in
// error identity that errors.Join introduces for the single-error case.
func joinErrors(errs ...error) error {
	var nonNil []error
	for _, err := range errs {
		if err != nil {
			nonNil = append(nonNil, err)
		}
	}
	switch len(nonNil) {
	case 0:
		return nil
	case 1:
		return nonNil[0]
	default:
		return errors.Join(nonNil...)
	}
}

type realAbsPathGet interface {
	getAbsPath(name string) (string, error)
}

// AbsPath returns the absolute path of the file that's accessible outside of the virtual file system.
//
// If the virtual file system name resolves to a path that is not accessible outside of the virtual file system, an error is returned.
func AbsPath(fsys any, name string) (string, error) {
	if rfs, ok := fsys.(realAbsPathGet); ok {
		return rfs.getAbsPath(name)
	}
	if rfs, ok := fsys.(*os.Root); ok {
		return filepath.Join(rfs.Name(), name), nil
	}

	return "", realAbsPathNotSupported(fsys, name)
}

func realAbsPathNotSupported(fsys any, name string) error {
	return pathError("absPath", name, fmt.Errorf("%q is not accessible outside of the virtual file system, %q", name, fsys))
}
