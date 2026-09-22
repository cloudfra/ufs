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

// Package ufspath provides path helpers that do not depend on any specific
// file-system implementation.
package ufspath

import (
	"io/fs"
	"strings"
)

const (
	unixPathSeparator    = "/"
	windowsPathSeparator = "\\"
)

// Error wraps op, name, and err in a *fs.PathError. If err is nil the
// returned error is a *fs.PathError with a nil Err field (and therefore still
// a non-nil error value).
func Error(op, name string, err error) error {
	return &fs.PathError{Op: op, Path: name, Err: err}
}

// CoerceUnix converts Windows backslash path separators to forward
// slashes. It has no effect on paths that do not contain backslashes.
func CoerceUnix(name string) string {
	return strings.ReplaceAll(name, windowsPathSeparator, unixPathSeparator)
}
