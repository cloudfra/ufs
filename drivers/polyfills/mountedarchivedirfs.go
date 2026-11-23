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

package polyfills

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
)

// IsMountedArchiveDir reports whether name is the virtual directory through
// which a file system exposes the content of an archive, keeping the contract
// of [types.MountedArchiveDirFS].
//
// fsys's own [types.MountedArchiveDirFS] implementation is used when it has
// one. The fallback is not implemented yet and reports false, which is the
// answer for a file system that has no such directories, since
// [types.MountedArchiveDirFS] is what says a file system has them. A wrapper
// that does not forward the method of the file system it wraps is answered
// false here as well, which is the failure that
// [types.MountedArchiveDirFS] warns wrappers about.
func IsMountedArchiveDir(fsys fs.FS, name string) bool {
	if cFS, ok := fsys.(types.MountedArchiveDirFS); ok {
		return cFS.IsMountedArchiveDir(name)
	}
	// TODO: Populate this with the best effort implementation of this method based on any combinations of methods that are available to reconstruct this method. Also include tests.
	return false
}
