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

// Package pathutil provides helpers for manipulating and validating the
// slash-separated path names used by ufs file systems.
package pathutil

import (
	"fmt"
	"io/fs"
	"path"
	"runtime"
	"strings"

	"github.com/cloudfra/ufs/internal/ufserrors"
)

const (
	// CwdPath is the [fs.ValidPath] name of a file system's root directory.
	CwdPath = "."
	// UnixSeparator is the path separator used by [fs.FS] names.
	UnixSeparator = "/"
	// WindowsSeparator is the Windows path separator.
	WindowsSeparator = "\\"

	unixAndWindowsSlashCutset = UnixSeparator + WindowsSeparator
)

// RemovePrefix removes the directory removePath from name. It reports false
// if name is not removePath or a descendant of it.
func RemovePrefix(name string, removePath string) (string, bool) {
	removePath = path.Clean(removePath)
	name = path.Clean(name)
	if IsCwd(removePath) {
		return name, true
	}
	if removePath == name {
		return CwdPath, true
	}
	return strings.CutPrefix(name, removePath+UnixSeparator)
}

// TrimSlash removes leading and trailing Unix and Windows separators.
func TrimSlash(name string) string {
	return strings.Trim(name, unixAndWindowsSlashCutset)
}

// Split splits name into its slash-separated components.
func Split(name string) []string {
	return strings.Split(TrimSlash(name), UnixSeparator)
}

// Validate returns an [fs.PathError] wrapping [fs.ErrInvalid] if name is not
// an [fs.ValidPath].
func Validate(op string, name string) error {
	if !fs.ValidPath(name) {
		return ufserrors.NewPathError(op, name, fmt.Errorf("%q is not a valid path for %s, %w", name, runtime.GOOS, fs.ErrInvalid))
	}
	return nil
}

// CoerceUnix replaces Windows separators in name with Unix separators.
func CoerceUnix(name string) string {
	return strings.ReplaceAll(name, WindowsSeparator, UnixSeparator)
}

// IsDirName reports whether name refers to a directory by its spelling alone.
func IsDirName(name string) bool {
	return IsCwd(name) || strings.HasSuffix(name, UnixSeparator)
}

// IsCwd reports whether name refers to the root directory.
func IsCwd(name string) bool {
	return name == "" || name == CwdPath
}
