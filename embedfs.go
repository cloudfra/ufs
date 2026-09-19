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
	"embed"
	"fmt"
	"io/fs"
	"net/url"

	"github.com/cloudfra/ufs/internal/device"
	"github.com/cloudfra/ufs/internal/pathutil"
)

const embedFSPrefix = "embed://"

var _ WriteFS = (*embedFS)(nil)

type embedFS struct {
	name string
	fsys embed.FS
}

func (fsys *embedFS) getDeviceInfo() device.Map {
	return device.NewMap(device.New("/dev/embed/"+fsys.name, "memory", 1, false))
}

func (fsys *embedFS) URI() *url.URL {
	return &url.URL{Scheme: "embed", Path: "/" + fsys.name, RawQuery: "ro=true"}
}

func (fsys *embedFS) String() string {
	return fmt.Sprintf("embedFS(%s)", fsys.URI())
}

func (fsys *embedFS) Open(name string) (fs.File, error) {
	if err := pathutil.ValidPath("open", name); err != nil {
		return nil, err
	}
	return fsys.fsys.Open(name)
}

func (fsys *embedFS) Close() error {
	return nil
}

func (fsys *embedFS) Stat(name string) (fs.FileInfo, error) {
	if err := pathutil.ValidPath("stat", name); err != nil {
		return nil, err
	}
	return fs.Stat(fsys.fsys, name)
}

func (fsys *embedFS) Lstat(name string) (fs.FileInfo, error) {
	// embed.FS contains no symlinks, so Lstat is identical to Stat.
	return fsys.Stat(name)
}

func (fsys *embedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := pathutil.ValidPath("readdir", name); err != nil {
		return nil, err
	}
	return fsys.fsys.ReadDir(name)
}

func (fsys *embedFS) ReadFile(name string) ([]byte, error) {
	if err := pathutil.ValidPath("readfile", name); err != nil {
		return nil, err
	}
	return fsys.fsys.ReadFile(name)
}

func (fsys *embedFS) ReadLink(name string) (string, error) {
	if err := pathutil.ValidPath("readlink", name); err != nil {
		return "", err
	}
	// embed.FS contains no symlinks.
	return "", pathutil.PathError("readlink", name, fs.ErrInvalid)
}

func (fsys *embedFS) Create(name string) (File, error) {
	if err := pathutil.ValidPath("create", name); err != nil {
		return nil, err
	}
	return nil, pathutil.PathError("create", name, fmt.Errorf("embedFS is read-only, cannot create file %q: %w", name, fs.ErrPermission))
}

func (fsys *embedFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := pathutil.ValidPath("mkdir", name); err != nil {
		return err
	}
	return pathutil.PathError("mkdir", name, fmt.Errorf("embedFS is read-only, cannot create directory %q: %w", name, fs.ErrPermission))
}

func (fsys *embedFS) Remove(name string) error {
	if err := pathutil.ValidPath("remove", name); err != nil {
		return err
	}
	return pathutil.PathError("remove", name, fmt.Errorf("embedFS is read-only, cannot remove %q: %w", name, fs.ErrPermission))
}

func (fsys *embedFS) RemoveAll(name string) error {
	if err := pathutil.ValidPath("removeall", name); err != nil {
		return err
	}
	return pathutil.PathError("removeall", name, fmt.Errorf("embedFS is read-only, cannot remove %q: %w", name, fs.ErrPermission))
}

// NewEmbedFS wraps a Go [embed.FS] as a read-only [FS]. name is used as the
// label returned by [FS.String]; it is typically the mount path or a
// description of the embedded content. Read operations delegate directly to
// the embed.FS; all write operations return [fs.ErrPermission].
func NewEmbedFS(name string, fsys embed.FS) FS {
	return &embedFS{name: name, fsys: fsys}
}
