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
	"os"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const writeFileOp = "writefile"

// WriteFile writes data to the file at name, creating it when it is missing and
// truncating what was there before, and gives a newly created file the
// permission bits in mode, keeping the contract of [types.WriteFileFS].
//
// fsys's own [types.WriteFileFS] implementation is used when it has one, since a
// file system knows better than a standin how to store a completed file.
// Otherwise the write is built from the methods that hand back a writable
// handle, the closest one first: [types.OpenFileFS], which takes both creating
// the file and its permission bits as arguments, then [types.CreateFileFS],
// which takes neither. A file system with nothing to write a file with gets an
// unimplemented error, and a name that is not a valid file name is refused with
// an error wrapping [fs.ErrInvalid] before the tree is touched.
//
// What an emulation can promise is what the file system's own methods promise,
// which is where it differs from the interface it stands in for:
//
//   - The mode reaches a file made through OpenFile and does not reach one made
//     through Create, which has no permission argument and chooses a mode for
//     itself. A file system with a umask narrows the mode either way, as it
//     narrows any mode.
//   - [types.WriteFileFS] reports a missing parent directory as an error wrapping
//     [fs.ErrNotExist], while the methods this write is built from follow the
//     file system's own rule about the levels above a name: memFS creates them, a
//     local file system reports that they are missing. The rule of the file
//     system stands, not the rule of the interface being emulated.
func WriteFile(fsys fs.FS, name string, data []byte, mode fs.FileMode) error {
	if cFS, ok := fsys.(types.WriteFileFS); ok {
		return cFS.WriteFile(name, data, mode)
	}
	if err := pathutil.ValidateFileName(writeFileOp, name); err != nil {
		return err
	}
	// The flags are what the interface asks for in one call: writable, created
	// when the name is missing, truncated when it is already there.
	if oFS, ok := fsys.(types.OpenFileFS); ok {
		f, err := oFS.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		return writeWhole(f, name, data)
	}
	if cFS, ok := fsys.(types.CreateFileFS); ok {
		f, err := cFS.Create(name)
		if err != nil {
			return err
		}
		return writeWhole(f, name, data)
	}
	return ufserrors.NewUnimplemented(fsys, writeFileOp)
}
