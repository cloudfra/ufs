// Copyright 2026 Cloudfra
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

package types

import "io/fs"

// RenameFileFS is an optional interface that a file system may implement to
// support file and directory renaming. It is embedded in the file-system type
// returned by the ufs constructors, so such a file system can rename directly;
// the Rename function in the ops package falls back to a copy-and-remove when
// the backend does not implement it.
type RenameFileFS interface {
	fs.FS

	// Rename moves a file or directory from oldPath to newPath. It returns an
	// error wrapping [fs.ErrNotExist] if oldPath does not exist, one wrapping
	// [fs.ErrExist] if newPath already exists, and one wrapping
	// [fs.ErrInvalid] if newPath is below oldPath. Renaming to or from the
	// root (".") returns [fs.ErrPermission]. Missing parent directories of
	// newPath are handled as Create handles them. Errors other than for an
	// invalid path are *os.LinkError values.
	//
	// Rename is not guaranteed to be atomic; some backends may implement it as
	// a copy-and-delete operation. Callers should not assume that the file at
	// oldPath is deleted if Rename returns an error.
	Rename(oldPath, newPath string) error
}
