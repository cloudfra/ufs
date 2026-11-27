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
	"io/fs"
	"path"

	"cloud.google.com/go/storage"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"google.golang.org/api/iterator"
)

// RemoveAll deletes an object and everything under its prefix. The closed check
// and the whole delete loop are one read-lock section: the loop keeps asking
// the same client for pages, so a client given up underneath it would answer
// the middle of the walk rather than its start. The path validation stays
// outside it. RemoveAll does the deleting itself rather than calling Remove, so
// it never re-enters the lock.
func (fsys *gcsFS) RemoveAll(name string) error {
	if name != pathutil.CwdPath {
		if err := pathutil.Validate("removeall", name); err != nil {
			return err
		}
	}
	fsys.mu.RLock()
	if fsys.closed.Load() {
		fsys.mu.RUnlock()
		return ufserrors.NewPathError("removeall", name, fs.ErrClosed)
	}
	err := fsys.removeAllLocked(name)
	fsys.mu.RUnlock()
	return err
}

// removeAllLocked is the bucket work behind RemoveAll. Its caller holds the read
// lock for the whole of it.
func (fsys *gcsFS) removeAllLocked(name string) error {
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
