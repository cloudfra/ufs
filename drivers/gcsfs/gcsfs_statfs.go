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

// Stat and Lstat are the [fs.StatFS] half of [types.ReadFS]. A bucket holds no symbolic links, so Lstat is Stat with another op in its error.

import (
	"errors"
	"io/fs"
	"path"
	"time"

	"cloud.google.com/go/storage"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// Stat answers from the object's attributes, or from a listing when the name is
// a prefix rather than an object. The closed check and both of those calls into
// the client are one read-lock section; the path validation, which needs no
// client, stays outside it.
func (fsys *gcsFS) Stat(name string) (fs.FileInfo, error) {
	if name != pathutil.CwdPath {
		if err := pathutil.Validate("stat", name); err != nil {
			return nil, err
		}
	}
	fsys.mu.RLock()
	if fsys.closed.Load() {
		fsys.mu.RUnlock()
		return nil, ufserrors.NewPathError("stat", name, fs.ErrClosed)
	}
	info, err := fsys.statLocked(name)
	fsys.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return info, nil
}

// statLocked is the bucket work behind Stat. Its caller holds the read lock for
// the whole of it. The root needs no request, but it is answered here too so
// that one closed file system answers every name the same way.
func (fsys *gcsFS) statLocked(name string) (fs.FileInfo, error) {
	if name == pathutil.CwdPath {
		return types.NewFileInfo(pathutil.CwdPath, 0, fs.ModeDir|fs.ModePerm, time.Time{}), nil
	}

	objPath := path.Join(fsys.baseDir, name)
	attrs, err := fsys.client.Bucket(fsys.bucket).Object(objPath).Attrs(fsys.ctx)
	if err == nil {
		return types.NewFileInfo(path.Base(name), attrs.Size, fs.ModePerm, attrs.Updated), nil
	}
	if !errors.Is(err, storage.ErrObjectNotExist) {
		return nil, ufserrors.NewPathError("stat", name, err)
	}

	// Not a file — check if it's a virtual directory (has objects under it).
	entries, listErr := fsys.listDir(name)
	if listErr != nil {
		return nil, ufserrors.NewPathError("stat", name, listErr)
	}
	if len(entries) == 0 {
		return nil, ufserrors.NewPathError("stat", name, fs.ErrNotExist)
	}
	return types.NewFileInfo(path.Base(name), 0, fs.ModeDir|fs.ModePerm, time.Time{}), nil
}

// Lstat is Stat with another op in its error. It takes no lock of its own: Stat
// makes the decision and the request at its own use site, and a read lock held
// here would deadlock the pair against a Close waiting in between.
func (fsys *gcsFS) Lstat(name string) (fs.FileInfo, error) {
	val, err := fsys.Stat(name)
	return ufserrors.ChangePathErrorOp(val, err, "lstat")
}
