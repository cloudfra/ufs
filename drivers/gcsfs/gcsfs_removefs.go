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

package gcsfs

// Remove is the [types.RemoveFS] half of [types.WriteFS]: it deletes one object and refuses a prefix that still has children.

import (
	"errors"
	"io/fs"
	"path"

	"cloud.google.com/go/storage"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// Remove deletes one object, after a listing proves the name has no children.
// A closed file system reports [fs.ErrClosed].
func (fsys *gcsFS) Remove(name string) error {
	if err := pathutil.Validate("remove", name); err != nil {
		return err
	}
	if fsys.closed.Load() {
		return ufserrors.NewPathError("remove", name, fs.ErrClosed)
	}
	return fsys.removeObject(name)
}

// removeObject is the bucket work behind Remove, once Remove has refused a
// closed file system.
func (fsys *gcsFS) removeObject(name string) error {
	bkt := fsys.client.Bucket(fsys.bucket)
	objPath := path.Join(fsys.baseDir, name)

	// If there are virtual-directory children, refuse (non-empty directory).
	entries, err := fsys.listDir(name)
	if err != nil {
		return ufserrors.NewPathError("remove", name, err)
	}
	if len(entries) > 0 {
		return ufserrors.NewPathError("remove", name, ufserrors.ErrDirNotEmpty)
	}

	if err := bkt.Object(objPath).Delete(fsys.ctx); err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return ufserrors.NewPathError("remove", name, fs.ErrNotExist)
		}
		return ufserrors.NewPathError("remove", name, err)
	}
	return nil
}
