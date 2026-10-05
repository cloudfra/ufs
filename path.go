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

// AbsPathGetter is an optional interface implemented by file systems whose files
// are also reachable on the host, outside of the virtual file system. See
// [AbsPath].
type AbsPathGetter interface {
	// GetAbsPath returns the absolute host path of the file at name. The
	// file does not have to exist, but the path must be the one where the
	// host would find it: an implementation returns an error for a name
	// that it serves from somewhere other than the host, such as a mount or
	// the inside of an archive.
	GetAbsPath(name string) (string, error)
}

// AbsPath returns the absolute path of the file that's accessible outside of the virtual file system.
//
// If the virtual file system name resolves to a path that is not accessible outside of the virtual file system, an error is returned.
func AbsPath(fsys any, name string) (string, error) {
	if rfs, ok := fsys.(AbsPathGetter); ok {
		return rfs.GetAbsPath(name)
	}
	if rfs, ok := fsys.(*os.Root); ok {
		return filepath.Join(rfs.Name(), name), nil
	}

	return "", realAbsPathNotSupported(fsys, name)
}

func realAbsPathNotSupported(fsys any, name string) error {
	return ufserrors.NewPathError("absPath", name, fmt.Errorf("%q is not accessible outside of the virtual file system, %q", name, fsys))
}
