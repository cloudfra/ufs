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

// Stat and Lstat are the [fs.StatFS] half of [types.ReadFS], reported
// with the write permission bits masked off.

import (
	"io/fs"
)

// Stat masks the wrapped file system's mode bits.
func (fsys *readOnlyFS) Stat(name string) (fs.FileInfo, error) {
	return maskFileInfo(fsys.ReadFS.Stat(name))
}

// Lstat masks the wrapped file system's mode bits. It is required because
// [types.ReadFS] is a [fs.ReadLinkFS], whose Lstat result is reported next to
// Stat's by emulators such as FUSE.
func (fsys *readOnlyFS) Lstat(name string) (fs.FileInfo, error) {
	return maskFileInfo(fsys.ReadFS.Lstat(name))
}
