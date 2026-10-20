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

// MountedArchiveDirFS is an optional interface implemented by file systems
// that expose the contents of an archive as a virtual directory next to the
// archive file, such as "data.zip.d" for "data.zip". Walkers such as
// ops.Walk use it to skip those directories.
//
// A file system that wraps another one should forward the method, otherwise
// the wrapped file system's archive directories look like ordinary ones.
type MountedArchiveDirFS interface {
	fs.FS

	// IsMountedArchiveDir reports whether name, a path of a directory in this
	// file system that satisfies [fs.ValidPath], is a virtual directory backed
	// by an archive rather than a directory of the underlying file system. It
	// reports false for an invalid path, and true when it cannot tell whether
	// the archive exists, so that a walker does not descend into it.
	IsMountedArchiveDir(name string) bool
}
