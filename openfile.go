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
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/fsutil"
)

// OpenFile opens the file at name in fsys with the given flag, a combination
// of the O_* constants of package os, and perm for a file that it creates. It
// calls the OpenFile method of a file system that implements [types.OpenFileFS]. A
// file system that does not is opened as
// github.com/cloudfra/ufs/drivers/polyfill.OpenFile opens it: to read a file,
// or to replace it.
//
// A file opened for writing implements [types.File]. An error that wraps
// [errors.ErrUnsupported] means that the file system cannot open a file with
// that combination of flags.
func OpenFile(fsys types.ReadFS, name string, flag int, perm fs.FileMode) (types.File, error) {
	if o, ok := fsys.(types.OpenFileFS); ok {
		return o.OpenFile(name, flag, perm)
	}
	return fsutil.OpenFilePolyfill(fsys, name, flag)
}
