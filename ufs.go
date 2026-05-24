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

// Package ufs provides a unified virtual file system abstraction for Go. It
// allows applications to treat diverse storage backends — local disk, memory,
// archives, Google Cloud Storage, remote Git repositories, and more — through
// a single consistent interface.
//
// # Creating a file system
//
// Use [New] with a URI to open a file system:
//
//	fsys, err := ufs.New("memory://")       // in-memory, volatile
//	fsys, err := ufs.New("null://")         // /dev/null semantics
//	fsys, err := ufs.New("/path/to/dir")    // local directory
//	fsys, err := ufs.New("file:///abs/dir") // same, explicit scheme
//	fsys, err := ufs.New("gs://bucket/prefix") // Google Cloud Storage
//	fsys, err := ufs.New("https://host/file.zip") // remote archive (downloaded to temp dir)
//
// Every [FS] must be closed when no longer needed.
//
// # Nested mounts
//
// [CreateURI] builds a URI that layers multiple file systems at different paths.
// Pass the resulting URI to [New]:
//
//	uri, _ := ufs.CreateURI("file:///data", map[string]string{
//	    "cache": "memory://",
//	})
//	fsys, _ := ufs.New(uri)
//
// # Path conventions
//
// All path arguments follow [fs.ValidPath]: forward-slash separated, no leading
// slash, no "." or ".." components. The root directory is always ".".
package ufs

import (
	"io"
	"io/fs"
)

// FileInfo provides file metadata. It currently mirrors [fs.FileInfo] and is
// defined as a separate interface to allow future extensions without breaking
// callers.
type FileInfo interface {
	fs.FileInfo
}

// ReadFile is a read-only file handle. It satisfies [fs.File].
type ReadFile interface {
	fs.File
}

// File is a read-write file handle. It extends [ReadFile] with write, seek, and
// random-read operations. All methods are safe to call at any time after the
// file is opened; unlike bare [fs.File], no method returns a "not supported"
// error.
type File interface {
	ReadFile
	io.ReadWriteSeeker
	io.ReaderAt
	io.StringWriter
}

// ReadFS is a read-only file system. In addition to the standard [fs.FS]
// interface it requires [io.Closer] for lifecycle management and the four
// extended read interfaces from the standard library. Callers should always
// Close a ReadFS when they are done with it.
type ReadFS interface {
	fs.FS
	io.Closer
	fs.ReadDirFS
	fs.ReadFileFS
	fs.ReadLinkFS
	fs.StatFS
}

// FS is a read-write file system. It extends [ReadFS] with file creation and
// directory creation.
type FS interface {
	ReadFS

	// Create opens a new writable file at name, replacing any existing file at
	// that path. Parent directories are not created automatically; call
	// MkdirAll first if needed.
	Create(name string) (File, error)

	// MkdirAll creates the directory at name and any missing parent directories,
	// using perm for newly created nodes. It is a no-op if the directory already
	// exists. Backends that do not have a real directory concept (e.g. GCS) treat
	// this as a no-op.
	MkdirAll(name string, perm fs.FileMode) error

	// String returns a human-readable description of the file system, typically
	// the URI or absolute path that was used to open it.
	String() string
}

// ListFilenames is an optional interface that a file system may implement to
// return all file paths under a directory without building an intermediate
// [fs.FileInfo] slice, reducing memory usage for large trees. [ListFiles] will
// use this interface when available.
type ListFilenames interface {
	// ListFilenames returns the paths of all files (not directories) under dir,
	// in unspecified order, with reduced allocations.
	ListFilenames(string) ([]string, error)
}

// ForEachFileInfoIter is an optional interface for streaming [fs.FileInfo]
// values without building a full slice. [ForEachFileInfo] uses this when the
// file system implements it.
type ForEachFileInfoIter interface {
	// ForEachFileInfo calls f for each file (not directory) under dir. If f
	// returns a non-nil error the walk stops and that error is returned.
	ForEachFileInfo(dir string, f func(fs.FileInfo) error) error
}

// ForEachFilenameIter is an optional interface for streaming file names without
// building a full slice. [ForEachFilename] uses this when the file system
// implements it.
type ForEachFilenameIter interface {
	// ForEachFilename calls f for each file path (not directory) under dir. If f
	// returns a non-nil error the walk stops and that error is returned.
	ForEachFilename(dir string, f func(string) error) error
}

