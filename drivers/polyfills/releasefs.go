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

// Release lets go of the resources fsys acquired for itself, keeping the
// contract of [types.ReleaseFS].
//
// fsys's own [types.ReleaseFS] implementation is used when it has one, since
// only it knows what it holds on to: a handle open on a remote file, a local
// copy of one, an archive it read its tree from. A file system that is not a
// [types.ReleaseFS] acquired nothing that a caller can let go of, so Release
// answers nil for it, which is what [types.ReleaseFS] asks of a file system
// that has acquired nothing.
//
// A decorator that holds no resource of its own uses this to pass the release
// down to the file system it wraps, so that releasing the view releases what
// the view kept in use below it. Unlike [types.ReleaseFS] it cannot join the
// errors of more than one file system, so a wrapper that holds several calls it
// for each and joins them itself.
func Release(fsys fs.FS) error {
	if rFS, ok := fsys.(types.ReleaseFS); ok {
		return rFS.Release()
	}
	return nil
}
