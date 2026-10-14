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

package ufs

import (
	"errors"
	"io/fs"
	"os"

	"github.com/cloudfra/ufs/internal/fsutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// OpenFile opens the file at name in fsys with the given flag, a combination
// of the O_* constants of package os, and perm for a file that it creates. It
// calls the OpenFile method of a file system that implements [OpenFileFS] and
// falls back to [OpenFilePolyfill] for one that does not.
//
// A file opened for writing implements [File]. An error that wraps
// [errors.ErrUnsupported] means that the file system cannot open a file with
// that combination of flags.
func OpenFile(fsys ReadFS, name string, flag int, perm fs.FileMode) (fs.File, error) {
	if o, ok := fsys.(OpenFileFS); ok {
		return o.OpenFile(name, flag, perm)
	}
	return OpenFilePolyfill(fsys, name, flag, perm)
}

// OpenFilePolyfill implements [OpenFileFS] with only the Open and Create
// methods of fsys. It is the OpenFile of file systems that can read a file and
// replace it, but cannot change one in place. It supports two kinds of flag:
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
// [WriteFS] fails with [fs.ErrPermission]. perm is ignored, as it is by
// Create.
func OpenFilePolyfill(fsys ReadFS, name string, flag int, _ fs.FileMode) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	if !fsutil.IsWriteFlag(flag) {
		return fsys.Open(name)
	}
	if flag&fsutil.AccessModeMask == os.O_RDONLY || flag&os.O_TRUNC == 0 || flag&(os.O_APPEND|os.O_EXCL) != 0 {
		return nil, ufserrors.NewPathError("open", name, errors.ErrUnsupported)
	}
	wfs, ok := fsys.(WriteFS)
	if !ok {
		return nil, ufserrors.NewPathError("open", name, fs.ErrPermission)
	}
	if flag&os.O_CREATE == 0 {
		if _, err := fsys.Stat(name); err != nil {
			return nil, err
		}
	}
	f, err := wfs.Create(name)
	if err != nil {
		return nil, err
	}
	return f, nil
}
