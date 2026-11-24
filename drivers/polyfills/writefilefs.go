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
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const writeFileOp = "writefile"

// WriteFile writes data to the file at name, creating it when it is missing and
// truncating what was there before, and gives a newly created file the
// permission bits in mode, keeping the contract of [types.WriteFileFS].
//
// fsys's own [types.WriteFileFS] implementation is used when it has one. The
// fallback is not implemented yet: a name that is not a valid file name is
// refused with an error wrapping [fs.ErrInvalid], and any other file system
// gets an unimplemented error.
func WriteFile(fsys fs.FS, name string, data []byte, mode fs.FileMode) error {
	if cFS, ok := fsys.(types.WriteFileFS); ok {
		return cFS.WriteFile(name, data, mode)
	}
	if err := pathutil.ValidateFileName(writeFileOp, name); err != nil {
		return err
	}
	// TODO: Populate this with the best effort implementation of this method based on any combinations of methods that are available to reconstruct this method. Also include tests.
	return ufserrors.NewUnimplemented(fsys, writeFileOp)
}
