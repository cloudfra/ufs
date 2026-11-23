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

const lchownOp = "lchown"

func Lchown(fsys fs.FS, name string, uid int, gid int) error {
	if cFS, ok := fsys.(types.LchownFS); ok {
		return cFS.Lchown(name, uid, gid)
	}
	if err := pathutil.ValidateFileName(lchownOp, name); err != nil {
		return err
	}
	// TODO: Populate this with the best effort implementation of this method based on any combinations of methods that are available to reconstruct this method. Also include tests.
	return ufserrors.NewUnimplemented(fsys, lchownOp)
}
