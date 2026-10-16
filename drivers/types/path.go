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

// ExternalPathGet is an optional interface implemented by file system backends that
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

// MountedArchiveDirFS is an optional interface implemented by file systems
// that expose the contents of an archive as a virtual directory next to the
// archive file, such as "data.zip.d" for "data.zip". Walkers such as
// ops.Walk use it to skip those directories.
//
// A file system that wraps another one should forward the method, otherwise
// the wrapped file system's archive directories look like ordinary ones.
type MountedArchiveDirFS interface {
	// IsMountedArchiveDir reports whether name, a path of a directory in this
	// file system that satisfies [fs.ValidPath], is a virtual directory backed
	// by an archive rather than a directory of the underlying file system. It
	// reports false for an invalid path, and true when it cannot tell whether
	// the archive exists, so that a walker does not descend into it.
	IsMountedArchiveDir(name string) bool
}
