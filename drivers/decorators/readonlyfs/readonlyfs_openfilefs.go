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

// OpenFile is the [types.OpenFileFS] half of [types.FS]: it refuses
// every flag that writes, creates or truncates, because a write of this file
// system always returns [fs.ErrPermission].

import (
	"io/fs"
	"os"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// OpenFile opens name with the [os.OpenFile] flags in flag, refusing every flag
// that writes, creates or truncates: those opens are writes, and a write of
// this file system always returns [fs.ErrPermission]. The handle of an allowed
// open reports the wrapped file's mode without its write permission bits, as
// [Open] does.
func (fsys *readOnlyFS) OpenFile(name string, flag int, perm fs.FileMode) (types.File, error) {
	if _, writes := polyfills.OpenFileAccess(flag); writes || flag&(os.O_CREATE|os.O_TRUNC) != 0 {
		if err := pathutil.Validate("open", name); err != nil {
			return nil, err
		}
		return nil, ufserrors.NewPathError("open", name, fs.ErrPermission)
	}
	f, err := polyfills.OpenFile(fsys.ReadFS, name, flag, perm)
	if err != nil {
		return nil, err
	}
	return maskFileMode(f), nil
}
