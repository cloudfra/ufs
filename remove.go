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

	"github.com/cloudfra/ufs/internal/ufserrors"
)

// Remove removes the file or empty directory at name in fsys.
// If fsys implements [RemoveFileFS], its Remove method is used directly.
// Otherwise Remove returns [fs.ErrPermission] wrapped in an [fs.PathError].
func Remove(fsys fs.FS, name string) error {
	r, ok := fsys.(RemoveFileFS)
	if !ok {
		return ufserrors.NewPathError("remove", name, fs.ErrPermission)
	}
	return r.Remove(name)
}

// RemoveAll removes name and everything beneath it in fsys.
// If fsys implements [RemoveFileFS], its RemoveAll method is used directly.
// Otherwise RemoveAll returns [fs.ErrPermission] wrapped in an [fs.PathError].
func RemoveAll(fsys fs.FS, name string) error {
	r, ok := fsys.(RemoveFileFS)
	if !ok {
		return ufserrors.NewPathError("removeall", name, fs.ErrPermission)
	}
	return r.RemoveAll(name)
}
