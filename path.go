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

package ufs

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// AbsPathGetter is implemented by file systems whose files are also accessible
// outside of the virtual file system, such as files on the local disk.
// [AbsPath] uses it to resolve a name.
type AbsPathGetter interface {
	// GetAbsPath returns the absolute host path of name. It returns an error if
	// name is not accessible outside of the virtual file system, or if name is
	// not a valid path ([fs.ValidPath]).
	GetAbsPath(name string) (string, error)
}

// AbsPath returns the absolute path of the file that's accessible outside of the virtual file system.
//
// If the virtual file system name resolves to a path that is not accessible outside of the virtual file system, an error is returned.
//
// name must be a valid path of the file system ([fs.ValidPath]); any other
// name, such as one that contains "..", is an error, so that the result is
// always at or below the root of the file system.
func AbsPath(fsys any, name string) (string, error) {
	if err := pathutil.Validate(absPathOp, name); err != nil {
		return "", err
	}
	if rfs, ok := fsys.(AbsPathGetter); ok {
		return rfs.GetAbsPath(name)
	}
	if rfs, ok := fsys.(*os.Root); ok {
		return filepath.Join(rfs.Name(), name), nil
	}

	return "", realAbsPathNotSupported(fsys, name)
}

// absPathOp is the operation reported in errors from [AbsPath].
const absPathOp = "absPath"

func realAbsPathNotSupported(fsys any, name string) error {
	return ufserrors.NewPathError(absPathOp, name, fmt.Errorf("%q is not accessible outside of the virtual file system, %q", name, fsys))
}
