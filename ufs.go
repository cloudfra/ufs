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
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/url"
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

// ExternalPathGet is an optional interface implemented by [FS] backends that
// can expose a concrete external path for a virtual file. The returned value may
// be an absolute local path, a URL, or any other identifier that allows
// callers to access the file outside the virtual file system.
//
// This is useful when passing a file to a library or tool that does not
// understand [fs.FS] or the virtual path abstraction.
type ExternalPathGet interface {
	// ExternalPath returns the canonical external path for the file at path.
	// Implementations that do not support this operation should return an empty
	// string.
	ExternalPath(path string) string
}

// CopyFileFS is an optional interface that a file system may implement to support
// file copying. It is embedded in [FS], so every writable backend
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

// RenameFileFS is an optional interface that a file system may implement to support
// file and directory renaming. It is embedded in [FS], so every writable backend
// must implement it.
type RenameFileFS interface {
	// Rename moves a file or directory from oldPath to newPath. It returns an
	// error wrapping [fs.ErrNotExist] if oldPath does not exist, or an error if
	// newPath already exists. Renaming the root (".") returns [fs.ErrPermission].
	//
	// Rename is not guaranteed to be atomic; some backends may implement it as
	// a copy-and-delete operation. Callers should not assume that the file at
	// oldPath is deleted if Rename returns an error.
	Rename(oldPath, newPath string) error
}

// URIGet describes file systems that can report the canonical identifier used
// to reconstruct them. The returned [*url.URL] is typically passed to [New] or
// stored as a durable configuration value. It must be safe to round-trip:
// calling [New] with u.String() should recreate an equivalent file system when
// the backend supports a stable URI representation.
//
// Implementations may return nil if the file system is synthetic or has no
// meaningful external identity (for example, [FromFS]). Callers should treat a
// nil URL or a non-nil error as "no URI" and fall back to a default value when
// needed. Wrappers such as nestFS embed mount information in the query string so
// the full composition is preserved.
//
// Read-only backends commonly include the query parameter ro=true to indicate
// the underlying scheme is read-only.
type URIGet interface {
	// URI returns the canonical identifier for this file system as a [*url.URL].
	//
	// The result is intended to be stable and round-trippable: if the backend has
	// a meaningful external identity, calling [New] with u.String() should
	// reconstruct an equivalent file system with the same semantics and mount
	// composition. This allows a file system to be serialized, logged, cached, or
	// re-opened without losing configuration details.
	//
	// A nil value indicates the backend has no meaningful URI. This is common for
	// synthetic adapters or wrappers that exist only in memory and cannot be
	// reconstructed from a single URL (for example, [FromFS]). In that case,
	// callers should treat the result as absent and fall back to a default value or
	// another source of configuration.
	//
	// A non-nil error means the backend could not construct a URI at this time.
	// Errors are distinct from a nil URL: a nil URL is an intentional "no
	// identifier" state, while an error indicates that the implementation failed to
	// determine or format the identifier.
	//
	// Wrappers such as nestFS include mount information in the query string so the
	// full virtual composition is preserved. Read-only backends commonly include a
	// ro=true query parameter to indicate that writes are not supported.
	URI() (*url.URL, error)
}

// ReadFS is a read-only file system. In addition to the standard [fs.FS]
// interface it requires [io.Closer] for lifecycle management, the four
// extended read interfaces from the standard library, and [fmt.Stringer] so
// that every implementation can describe itself. Callers should always Close
// a ReadFS when they are done with it.
type ReadFS interface {
	fs.FS
	io.Closer
	fs.ReadDirFS
	fs.ReadFileFS
	fs.ReadLinkFS
	fs.StatFS

	fmt.Stringer

	deviceInfoGet

	// TODO: Implement ExternalPathGet

	URIGet

	// String returns a human-readable description of the file system that
	// shows how it is composed — wrapper layers, mount points, and the
	// underlying storage backends. It is intended for debugging and logging.
	String() string
}

// RemoveFileFS provides file and directory deletion. It is embedded in [WriteFS],
// so every writable backend must implement it.
type RemoveFileFS interface {
	// Remove deletes the file or empty directory at name. It returns an error
	// wrapping [fs.ErrNotExist] if name does not exist, or an error if name is
	// a non-empty directory. Removing the root (".") returns [fs.ErrPermission].
	Remove(name string) error

	// RemoveAll removes name and all contents beneath it. It is a no-op (returns
	// nil) if name does not exist.
	RemoveAll(name string) error
}

// WriteFS is a read-write file system. It extends [ReadFS] with file creation,
// directory creation, and deletion.
type WriteFS interface {
	ReadFS
	RemoveFileFS

	// Create opens a new writable file at name, replacing any existing file at
	// that path. Parent directories are not created automatically; call
	// MkdirAll first if needed.
	Create(name string) (File, error)

	// MkdirAll creates the directory at name and any missing parent directories,
	// using perm for newly created nodes. It is a no-op if the directory already
	// exists. Backends that do not have a real directory concept (e.g. GCS) treat
	// this as a no-op.
	MkdirAll(name string, perm fs.FileMode) error
}

// FS is the top-level file system interface returned by the public
// constructors. It extends [WriteFS] and will add file copying
// ([CopyFileFS]) and renaming ([RenameFileFS]); until those are implemented
// it is equivalent to [WriteFS]. Every concrete value is a *nestFS.
type FS interface {
	WriteFS

	// Copy(srcPath, dstPath string) error
	// TODO: Implement RenameFS
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

// NotifyOp describes the kind of change observed on a path.
type NotifyOp int

const (
	// NotifyCreate indicates a file or directory was created.
	NotifyCreate NotifyOp = iota
	// NotifyWrite indicates a file was written to.
	NotifyWrite
	// NotifyRemove indicates a file or directory was removed.
	NotifyRemove
	// NotifyRename indicates a file or directory was renamed.
	NotifyRename
	// NotifyChmod indicates permissions or attributes changed.
	NotifyChmod
)

// NotifyHook is invoked for each change observed by a [Watcher]. The path
// argument is root-relative, forward-slash separated, and satisfies
// [fs.ValidPath] — it is never an OS-native or absolute path.
type NotifyHook func(op NotifyOp, path string)

// Watcher is an optional interface implemented by file systems that can
// deliver recursive change notifications for a directory subtree.
type Watcher interface {
	// Watch begins watching name (a directory) and all nested directories,
	// invoking hook for each observed change. Watching stops when ctx is
	// canceled or the returned [io.Closer] is closed, whichever comes first.
	// Closing is idempotent and must terminate all background goroutines.
	//
	// The hook is called serially from a single background goroutine; it
	// should not block for long.
	Watch(ctx context.Context, name string, hook NotifyHook) (io.Closer, error)
}

// OpenFileFS provides OpenFile extension for WriteFS.
type OpenFileFS interface {
	// OpenFile is the generalized open call; most users will use Open or Create instead.
	OpenFile(name string, flag int, perm fs.FileMode) (fs.File, error)
}
