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
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudfra/ufs/internal/ufserrors"
)

type realAbsPathGet interface {
	getAbsPath(name string) (string, error)
}

// AbsPath returns the absolute path of the file that's accessible outside of the virtual file system.
//
// If the virtual file system name resolves to a path that is not accessible outside of the virtual file system, an error is returned.
func AbsPath(fsys any, name string) (string, error) {
	if rfs, ok := fsys.(realAbsPathGet); ok {
		return rfs.getAbsPath(name)
	}
	if rfs, ok := fsys.(*os.Root); ok {
		return filepath.Join(rfs.Name(), name), nil
	}

	return "", realAbsPathNotSupported(fsys, name)
}

func realAbsPathNotSupported(fsys any, name string) error {
	return ufserrors.NewPathError("absPath", name, fmt.Errorf("%q is not accessible outside of the virtual file system, %q", name, fsys))
}
