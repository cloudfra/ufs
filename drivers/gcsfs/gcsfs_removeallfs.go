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

// RemoveAll is the [types.RemoveAllFS] half of [types.WriteFS]: it deletes an object and every object under its prefix.

import (
	"errors"
	"path"

	"cloud.google.com/go/storage"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"google.golang.org/api/iterator"
)

func (fsys *gcsFS) RemoveAll(name string) error {
	if err := fsys.closedErr("removeall", name); err != nil {
		return err
	}
	if name != pathutil.CwdPath {
		if err := pathutil.Validate("removeall", name); err != nil {
			return err
		}
	}
	bkt := fsys.client.Bucket(fsys.bucket)

	var listPrefix string
	if name == pathutil.CwdPath {
		listPrefix = fsys.baseDir
		if listPrefix != "" {
			listPrefix += "/"
		}
	} else {
		objPath := path.Join(fsys.baseDir, name)
		// Best-effort delete of the exact object; ignore not-found.
		if delErr := bkt.Object(objPath).Delete(fsys.ctx); delErr != nil && !errors.Is(delErr, storage.ErrObjectNotExist) {
			return ufserrors.NewPathError("removeall", name, delErr)
		}
		listPrefix = objPath + "/"
	}

	it := bkt.Objects(fsys.ctx, &storage.Query{Prefix: listPrefix})
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return ufserrors.NewPathError("removeall", name, err)
		}
		if delErr := bkt.Object(attrs.Name).Delete(fsys.ctx); delErr != nil && !errors.Is(delErr, storage.ErrObjectNotExist) {
			return ufserrors.NewPathError("removeall", name, delErr)
		}
	}
	return nil
}
