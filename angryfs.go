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
	"github.com/cloudfra/ufs/internal/ufserrors"
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
	return fmt.Sprintf("angryFS(%s)", uriOrDefault(fsys, fsys.name))
}

func (fsys *angryFS) Open(name string) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	return nil, ufserrors.NewPathError("open", name, errAngry)
}

func (fsys *angryFS) Close() error {
	return errAngry
}

func (fsys *angryFS) Stat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("stat", name); err != nil {
		return nil, err
	}
	return nil, ufserrors.NewPathError("stat", name, errAngry)
}

func (fsys *angryFS) Create(name string) (File, error) {
	if err := pathutil.Validate("create", name); err != nil {
		return nil, err
	}
	return nil, ufserrors.NewPathError("create", name, errAngry)
}

func (fsys *angryFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := pathutil.Validate("mkdir", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("mkdir", name, errAngry)
}

func (fsys *angryFS) ReadFile(name string) ([]byte, error) {
	if err := pathutil.Validate("readfile", name); err != nil {
		return nil, err
	}
	return nil, ufserrors.NewPathError("readfile", name, errAngry)
}

func (fsys *angryFS) ReadLink(name string) (string, error) {
	if err := pathutil.Validate("readlink", name); err != nil {
		return "", err
	}
	return "", ufserrors.NewPathError("readlink", name, errAngry)
}

func (fsys *angryFS) Lstat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("lstat", name); err != nil {
		return nil, err
	}
	return nil, ufserrors.NewPathError("lstat", name, errAngry)
}

func (fsys *angryFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := pathutil.Validate("readdir", name); err != nil {
		return nil, err
	}
	return nil, ufserrors.NewPathError("readdir", name, errAngry)
}

func (fsys *angryFS) Glob(_ string) ([]string, error) {
	return nil, errAngry
}

func (fsys *angryFS) Remove(name string) error {
	if err := pathutil.Validate("remove", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("remove", name, errAngry)
}

func (fsys *angryFS) RemoveAll(name string) error {
	if err := pathutil.Validate("removeall", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("removeall", name, errAngry)
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
