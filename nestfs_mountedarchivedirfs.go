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

package ufs

// IsMountedArchiveDir is [types.MountedArchiveDirFS]: it reports the a.zip.d
// directory that nestFS mounted from a.zip.

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/cloudfra/ufs/internal/archive"
)

// IsMountedArchiveDir reports whether name (a full path within this FS) is a
// virtual directory backed by a mounted archive. It returns true only when:
//   - name ends with archiveDirExt
//   - the trimmed name satisfies archive.IsMountablePath
//   - the archive file is not confirmed absent; any Stat error other than
//     ErrNotExist is treated as "file likely exists" so that a permission-denied
//     error does not cause Walk to descend and trigger a mount failure
//
// Note: if a real directory named "foo.zip.d" coexists with "foo.zip" in the
// same FS, nestFS's path routing always redirects access through the archive
// (see getFSAndSubpath). The real directory is unreachable via this FS
// regardless of what this method returns; that is a nestFS limitation.
func (fsys *nestFS) IsMountedArchiveDir(name string) bool {
	if !strings.HasSuffix(name, archiveDirExt) || !fs.ValidPath(name) {
		return false
	}
	archiveName := strings.TrimSuffix(name, archiveDirExt)
	if !archive.IsMountablePath(archiveName) {
		return false
	}
	_, err := fsys.Stat(archiveName)
	return !errors.Is(err, fs.ErrNotExist)
}
