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

package readonlyfs

// ReadDir is the [fs.ReadDirFS] half of [types.ReadFS], with each
// entry's mode masked. The entries are copied because a wrapped file system may
// cache the slice it returns, and its own view must keep its real mode bits.

import (
	"io/fs"
)

// ReadDir returns copies of the wrapped entries whose Info mode is masked. The
// entries are copied because a wrapped file system may cache the slice it
// returns, and its own view must keep its real mode bits.
func (fsys *readOnlyFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return maskDirEntries(fsys.ReadFS.ReadDir(name))
}
