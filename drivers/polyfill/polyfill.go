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

// Package polyfill implements optional file system interfaces with the
// methods that every file system has, for drivers that cannot implement them
// natively.
package polyfill

import (
	"io/fs"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/fsutil"
)

// OpenFile implements [ufs.OpenFileFS] with only the Open, Stat and Create
// methods of fsys. It is the OpenFile of file systems that can read a file and
// replace it, but cannot change one in place:
//
//	func (fsys *myFS) OpenFile(name string, flag int, perm fs.FileMode) (fs.File, error) {
//		return polyfill.OpenFile(fsys, name, flag, perm)
//	}
//
// It never calls the OpenFile method of fsys. It supports two kinds of flag:
//
//   - Read-only: O_RDONLY without O_CREATE, O_TRUNC, O_APPEND or O_EXCL, which
//     is Open.
//   - Write by truncation: O_WRONLY or O_RDWR with O_TRUNC, which is Create.
//     Without O_CREATE the file must already exist. That check and the
//     truncation are two steps and not atomic.
//
// Any other combination fails with an error that wraps
// [errors.ErrUnsupported]: a write without O_TRUNC would have to keep the
// content of the file, O_APPEND would have to write at its end and O_EXCL
// would have to create it atomically. A write to a file system that is not a
// [ufs.WriteFS] fails with [fs.ErrPermission]. perm is ignored, as it is by
// Create.
func OpenFile(fsys ufs.ReadFS, name string, flag int, _ fs.FileMode) (fs.File, error) {
	return fsutil.OpenFilePolyfill[ufs.File, ufs.WriteFS](fsys, name, flag)
}
