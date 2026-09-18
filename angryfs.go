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

	"github.com/cloudfra/ufs/internal/pathutil"
)

const (
	angryFSPrefix = "angry:"
)

var (
	_ WriteFS   = (*angryFS)(nil)
	_ fs.GlobFS = (*angryFS)(nil)

	errAngry = fs.ErrInvalid

	angryDeviceInfo = deviceInfo{
		name:        "angry",
		deviceType:  "angry",
		threadCount: 1,
	}
	angryDeviceInfoMap = newDeviceInfoMap(angryDeviceInfo)
)

func init() {
	Register(Driver{
		Name:       "angry",
		MatchFunc:  isAngryFSUri,
		CreateFunc: newAngryFS,
		Priority:   1,
		Standard:   false,
		ReadWrite:  false,
	})
}

type angryFS struct {
	name string
}

func (fsys *angryFS) getDeviceInfo() map[string]deviceInfo {
	return angryDeviceInfoMap
}

func (fsys *angryFS) URI() *url.URL {
	u, _ := url.Parse(fsys.name)
	v := u.Query()
	v.Set("ro", "true")
	u.RawQuery = v.Encode()
	return u
}

func (fsys *angryFS) String() string {
	return fmt.Sprintf("angryFS(%s)", fsys.URI())
}

func (fsys *angryFS) Open(name string) (fs.File, error) {
	if err := pathutil.ValidPath("open", name); err != nil {
		return nil, err
	}
	return nil, pathutil.PathError("open", name, errAngry)
}

func (fsys *angryFS) Close() error {
	return errAngry
}

func (fsys *angryFS) Stat(name string) (fs.FileInfo, error) {
	if err := pathutil.ValidPath("stat", name); err != nil {
		return nil, err
	}
	return nil, pathutil.PathError("stat", name, errAngry)
}

func (fsys *angryFS) Create(name string) (File, error) {
	if err := pathutil.ValidPath("create", name); err != nil {
		return nil, err
	}
	return nil, pathutil.PathError("create", name, errAngry)
}

func (fsys *angryFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := pathutil.ValidPath("mkdir", name); err != nil {
		return err
	}
	return pathutil.PathError("mkdir", name, errAngry)
}

func (fsys *angryFS) ReadFile(name string) ([]byte, error) {
	if err := pathutil.ValidPath("readfile", name); err != nil {
		return nil, err
	}
	return nil, pathutil.PathError("readfile", name, errAngry)
}

func (fsys *angryFS) ReadLink(name string) (string, error) {
	if err := pathutil.ValidPath("readlink", name); err != nil {
		return "", err
	}
	return "", pathutil.PathError("readlink", name, errAngry)
}

func (fsys *angryFS) Lstat(name string) (fs.FileInfo, error) {
	if err := pathutil.ValidPath("lstat", name); err != nil {
		return nil, err
	}
	return nil, pathutil.PathError("lstat", name, errAngry)
}

func (fsys *angryFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := pathutil.ValidPath("readdir", name); err != nil {
		return nil, err
	}
	return nil, pathutil.PathError("readdir", name, errAngry)
}

func (fsys *angryFS) Glob(_ string) ([]string, error) {
	return nil, errAngry
}

func (fsys *angryFS) Remove(name string) error {
	if err := pathutil.ValidPath("remove", name); err != nil {
		return err
	}
	return pathutil.PathError("remove", name, errAngry)
}

func (fsys *angryFS) RemoveAll(name string) error {
	if err := pathutil.ValidPath("removeall", name); err != nil {
		return err
	}
	return pathutil.PathError("removeall", name, errAngry)
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
