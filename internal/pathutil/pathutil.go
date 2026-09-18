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

// Package pathutil provides the slash-separated, fs.ValidPath-flavored path
// helpers shared by every ufs backend: validating a path against the
// [fs.FS] contract, building a [fs.PathError], and small string
// manipulations (trimming slashes, splitting into components, normalizing
// Windows separators to Unix ones) that every backend needs when
// translating between ufs's virtual paths and its own storage keys.
package pathutil

import (
	"fmt"
	"io/fs"
	"path"
	"runtime"
	"strings"
)

const (
	// UnixSeparator is the path separator ufs uses for all virtual paths,
	// regardless of host OS.
	UnixSeparator = "/"
	// WindowsSeparator is the OS-native separator on Windows, rejected in
	// virtual paths and normalized away by [CoerceUnix].
	WindowsSeparator = "\\"
	// CwdPath is the virtual path denoting the root of a file system, mirroring
	// ".", the value [fs.ValidPath] requires for the root.
	CwdPath = "."
	// SlashCutset contains both separators, for use with strings.Trim.
	SlashCutset = UnixSeparator + WindowsSeparator
)

// RemovePrefix strips removePath from the front of name, returning the
// remainder and true. If removePath is the root ([CwdPath], empty, or an
// alias of it per [IsCwd]) name is returned unchanged. If name equals
// removePath exactly, [CwdPath] is returned. If name does not have
// removePath as a prefix, ok is false.
func RemovePrefix(name string, removePath string) (rest string, ok bool) {
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

// TrimSlash trims leading and trailing Unix and Windows path separators from
// name.
func TrimSlash(name string) string {
	return strings.Trim(name, SlashCutset)
}

// SplitPath splits name into its slash-separated components, after trimming
// leading/trailing separators.
func SplitPath(name string) []string {
	return strings.Split(TrimSlash(name), UnixSeparator)
}

// ValidPath returns a [fs.PathError] wrapping [fs.ErrInvalid] if name does
// not satisfy [fs.ValidPath], and nil otherwise. op is used to label the
// returned error.
func ValidPath(op string, name string) error {
	if !fs.ValidPath(name) {
		return PathError(op, name, fmt.Errorf("%q is not a valid path for %s, %w", name, runtime.GOOS, fs.ErrInvalid))
	}
	return nil
}

// CoerceUnix replaces every [WindowsSeparator] in name with [UnixSeparator].
func CoerceUnix(name string) string {
	return strings.ReplaceAll(name, WindowsSeparator, UnixSeparator)
}

// IsDirName reports whether name denotes a directory by ufs's virtual-path
// convention: the root, or any path with a trailing separator.
func IsDirName(name string) bool {
	return IsCwd(name) || strings.HasSuffix(name, UnixSeparator)
}

// IsCwd reports whether name is empty or [CwdPath], ufs's two spellings of
// "the root of this file system".
func IsCwd(name string) bool {
	return name == "" || name == CwdPath
}

// PathError builds an [fs.PathError] for operation op on name, wrapping err.
func PathError(op string, name string, err error) error {
	return &fs.PathError{
		Op:   op,
		Path: name,
		Err:  err,
	}
}
