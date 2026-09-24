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
	"io"
	"io/fs"
	"net/url"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var _ ReadFS = (*readWrapFS)(nil)

type readWrapFS struct {
	fsys fs.FS
}

func (fsys *readWrapFS) getDeviceInfo() map[string]deviceInfo {
	return getDeviceInfoOrDefault(fsys.fsys)
}

func (fsys *readWrapFS) URI() (*url.URL, error) {
	if ug, ok := fsys.fsys.(URIGet); ok {
		return ug.URI()
	}
	return nil, nil
}

func (fsys *readWrapFS) String() string {
	return fmt.Sprintf("readWrapFS(%T)", fsys.fsys)
}

func (fsys *readWrapFS) Open(name string) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	return fsys.fsys.Open(name)
}

func (fsys *readWrapFS) Close() error {
	if c, ok := fsys.fsys.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func (fsys *readWrapFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := pathutil.Validate("readdir", name); err != nil {
		return nil, err
	}
	if rdfs, ok := fsys.fsys.(fs.ReadDirFS); ok {
		return rdfs.ReadDir(name)
	}
	return fs.ReadDir(fsys.fsys, name)
}

func (fsys *readWrapFS) ReadFile(name string) ([]byte, error) {
	if err := pathutil.Validate("readfile", name); err != nil {
		return nil, err
	}
	if rffs, ok := fsys.fsys.(fs.ReadFileFS); ok {
		return rffs.ReadFile(name)
	}
	return fs.ReadFile(fsys.fsys, name)
}

func (fsys *readWrapFS) Stat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("stat", name); err != nil {
		return nil, err
	}
	if sfs, ok := fsys.fsys.(fs.StatFS); ok {
		return sfs.Stat(name)
	}
	return fs.Stat(fsys.fsys, name)
}

func (fsys *readWrapFS) Lstat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("lstat", name); err != nil {
		return nil, err
	}
	if rlfs, ok := fsys.fsys.(fs.ReadLinkFS); ok {
		return rlfs.Lstat(name)
	}
	return fsys.Stat(name)
}

func (fsys *readWrapFS) ReadLink(name string) (string, error) {
	if err := pathutil.Validate("readlink", name); err != nil {
		return "", err
	}
	if rlfs, ok := fsys.fsys.(fs.ReadLinkFS); ok {
		return rlfs.ReadLink(name)
	}
	return "", ufserrors.NewPathError("readlink", name, fs.ErrInvalid)
}

// FromFS wraps a standard library [fs.FS] as a read-only [ReadFS].
func FromFS(fsys fs.FS) ReadFS {
	return &readWrapFS{fsys: fsys}
}
