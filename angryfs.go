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
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"strings"

	"github.com/cloudfra/ufs/internal/ufspath"
	"github.com/cloudfra/ufs/internal/ufsurl"
)

const (
	angryFSPrefix = "angry:"
)

var (
	_ WriteFS   = (*angryFS)(nil)
	_ fs.GlobFS = (*angryFS)(nil)

	errAngry = fs.ErrInvalid

	angryDeviceInfo    = newDeviceInfo("angry", "angry", 1, false)
	angryDeviceInfoMap = newDeviceInfoMap(angryDeviceInfo)
)

func init() {
	Register(newDriver("angry", newAngryFS, isAngryFSUri, 1, false, false))
}

type angryFS struct {
	name string
}

func (fsys *angryFS) getDeviceInfo() map[string]deviceInfo {
	return angryDeviceInfoMap
}

func (fsys *angryFS) URI() (*url.URL, error) {
	u, err := url.Parse(fsys.name)
	if err != nil {
		return nil, fmt.Errorf("URI %q is not valid, %w", fsys.name, err)
	}
	v := u.Query()
	v.Set("ro", "true")
	u.RawQuery = v.Encode()
	return u, nil
}

func (fsys *angryFS) String() string {
	return fmt.Sprintf("angryFS(%s)", ufsurl.URIOrDefault(fsys, fsys.name))
}

func (fsys *angryFS) Open(name string) (fs.File, error) {
	if err := validPath("open", name); err != nil {
		return nil, err
	}
	return nil, ufspath.Error("open", name, errAngry)
}

func (fsys *angryFS) Close() error {
	return errAngry
}

func (fsys *angryFS) Stat(name string) (fs.FileInfo, error) {
	if err := validPath("stat", name); err != nil {
		return nil, err
	}
	return nil, ufspath.Error("stat", name, errAngry)
}

func (fsys *angryFS) Create(name string) (File, error) {
	if err := validPath("create", name); err != nil {
		return nil, err
	}
	return nil, ufspath.Error("create", name, errAngry)
}

func (fsys *angryFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := validPath("mkdir", name); err != nil {
		return err
	}
	return ufspath.Error("mkdir", name, errAngry)
}

func (fsys *angryFS) ReadFile(name string) ([]byte, error) {
	if err := validPath("readfile", name); err != nil {
		return nil, err
	}
	return nil, ufspath.Error("readfile", name, errAngry)
}

func (fsys *angryFS) ReadLink(name string) (string, error) {
	if err := validPath("readlink", name); err != nil {
		return "", err
	}
	return "", ufspath.Error("readlink", name, errAngry)
}

func (fsys *angryFS) Lstat(name string) (fs.FileInfo, error) {
	if err := validPath("lstat", name); err != nil {
		return nil, err
	}
	return nil, ufspath.Error("lstat", name, errAngry)
}

func (fsys *angryFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := validPath("readdir", name); err != nil {
		return nil, err
	}
	return nil, ufspath.Error("readdir", name, errAngry)
}

func (fsys *angryFS) Glob(_ string) ([]string, error) {
	return nil, errAngry
}

func (fsys *angryFS) Remove(name string) error {
	if err := validPath("remove", name); err != nil {
		return err
	}
	return ufspath.Error("remove", name, errAngry)
}

func (fsys *angryFS) RemoveAll(name string) error {
	if err := validPath("removeall", name); err != nil {
		return err
	}
	return ufspath.Error("removeall", name, errAngry)
}

func newAngryFS(_ context.Context, name string) (FS, error) {
	return makeAngryFS(name), nil
}

func makeAngryFS(name string) *angryFS {
	return &angryFS{
		name: name,
	}
}

func isAngryFSUri(name string) bool {
	return strings.HasPrefix(name, angryFSPrefix)
}
