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
	"errors"
	"io/fs"
	"path"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const closestParentOp = "closestparent"

// ClosestParent returns the closest node that exists at or above name, name
// itself included, and reports whether that node is a directory, keeping the
// contract of [types.ClosestParentFS].
//
// fsys's own [types.ClosestParentFS] implementation is used when it has one.
// Otherwise the levels are looked up with [fs.Stat] from name upward, stopping
// at the first that exists, so a name whose parent is there costs two lookups
// and the root always answers. Only a level reported missing ([fs.ErrNotExist])
// moves the walk up: any other error is the file system being unable to
// answer, which the contract reports as an error rather than as a missing
// level. A name that is not a valid file name, the root included, is refused
// with an error wrapping [fs.ErrInvalid].
func ClosestParent(fsys fs.FS, name string) (parent string, isDir bool, err error) {
	if cFS, ok := fsys.(types.ClosestParentFS); ok {
		return cFS.ClosestParent(name)
	}
	if err := pathutil.ValidateFileName(closestParentOp, name); err != nil {
		return "", false, err
	}
	for dir := name; ; dir = path.Dir(dir) {
		info, err := fs.Stat(fsys, dir)
		switch {
		case err == nil:
			return dir, info.IsDir(), nil
		case !errors.Is(err, fs.ErrNotExist):
			return "", false, err
		case dir == pathutil.CwdPath:
			// Even the root is missing, which no file system that answers
			// lookups at all reports; say so rather than inventing a parent.
			return "", false, ufserrors.NewPathError(closestParentOp, name, err)
		}
	}
}
