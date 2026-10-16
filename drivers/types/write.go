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

package types

import (
	"io/fs"
)

// CreateFS is a file system that can create files.
type CreateFS interface {
	// Create opens a new writable file at name, replacing any existing file at
	// that path. Parent directories are not created automatically; call
	// MkdirAll first if needed.
	Create(name string) (File, error)
}

// MkdirAllFS is a file system that can create directories.
type MkdirAllFS interface {
	// MkdirAll creates the directory at name and any missing parent directories,
	// using perm for newly created nodes. It is a no-op if the directory already
	// exists. Backends that do not have a real directory concept (e.g. GCS) treat
	// this as a no-op.
	MkdirAll(name string, perm fs.FileMode) error
}

// RemoveFS is a file system that can remove a file or an empty directory.
type RemoveFS interface {
	// Remove deletes the file or empty directory at name. It returns an error
	// wrapping [fs.ErrNotExist] if name does not exist, or an error if name is
	// a non-empty directory. Removing the root (".") returns [fs.ErrPermission].
	Remove(name string) error
}

// RemoveAllFS is a file system that can remove a directory tree.
type RemoveAllFS interface {
	// RemoveAll removes name and all contents beneath it. It is a no-op (returns
	// nil) if name does not exist.
	RemoveAll(name string) error
}

// OpenFileFS provides OpenFile extension for WriteFS.
type OpenFileFS interface {
	// OpenFile is the generalized open call; most users will use Open or Create instead.
	OpenFile(name string, flag int, perm fs.FileMode) (fs.File, error)
}

// RenameFileFS is an optional interface that a file system may implement to support
// file and directory renaming. It is embedded in ufs.FS, so every ufs.FS supports
// it; call ufs.Rename to rename within any ufs.WriteFS.
type RenameFileFS interface {
	// Rename moves a file or directory from oldPath to newPath. It returns an
	// error wrapping [fs.ErrNotExist] if oldPath does not exist, one wrapping
	// [fs.ErrExist] if newPath already exists, and one wrapping
	// [fs.ErrInvalid] if newPath is below oldPath. Renaming to or from the
	// root (".") returns [fs.ErrPermission]. Missing parent directories of
	// newPath are handled as Create handles them. Errors other than for an
	// invalid path are [*os.LinkError] values.
	//
	// Rename is not guaranteed to be atomic; some backends may implement it as
	// a copy-and-delete operation. Callers should not assume that the file at
	// oldPath is deleted if Rename returns an error.
	Rename(oldPath, newPath string) error
}

// CopyFileFS is an optional interface that a file system may implement to support
// file copying. ufs.FS will embed it, at which point every writable backend
// must implement it.
type CopyFileFS interface {
	// CopyFile copies a file from srcPath to dstPath. It returns an error
	// wrapping [fs.ErrNotExist] if srcPath does not exist, or an error if dstPath
	// already exists. Copying the root (".") returns [fs.ErrPermission].
	//
	// CopyFile is not guaranteed to be atomic; some backends may implement it as
	// a read-and-write operation. Callers should not assume that the file at
	// dstPath is created if CopyFile returns an error.
	CopyFile(srcPath, dstPath string) error
}
