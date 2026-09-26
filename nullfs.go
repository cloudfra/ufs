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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"strings"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const (
	nullFSPrefix = "null:"
)

var (
	_ File           = (*nullFile)(nil)
	_ WriteFS        = (*nullFS)(nil)
	_ fs.GlobFS      = (*nullFS)(nil)
	_ fs.ReadDirFile = (*nullReadDirFile)(nil)

	nullDirStat = &fsInfo{
		name:    ".",
		size:    osutil.EmptyDirSize,
		mode:    fs.ModeDir | fs.ModePerm,
		modTime: unixEpochTime,
		isDir:   true,
		sys:     nil,
	}

	nullDeviceInfo    = newDeviceInfo("null", "null", 1, false)
	nullDeviceInfoMap = NewDeviceMap(nullDeviceInfo)
)

func init() {
	Register(newDriver("null", newNullFS, isNullFSUri, 1, false, true))
}

type nullFile struct {
	name string
}

func (n *nullFile) Stat() (fs.FileInfo, error) {
	isDir := pathutil.IsDirName(n.name)
	mode := fs.ModePerm
	size := int64(0)
	if isDir {
		mode = fs.ModeDir | fs.ModePerm
		size = osutil.EmptyDirSize
	}
	return &fsInfo{
		name:    path.Base(n.name),
		size:    size,
		mode:    mode,
		modTime: unixEpochTime,
		isDir:   isDir,
		sys:     nil,
	}, nil
}

func (n *nullFile) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (n *nullFile) Close() error {
	return nil
}

func (n *nullFile) Write(p []byte) (n2 int, err error) {
	return len(p), nil
}

func (n *nullFile) Seek(_ int64, _ int) (int64, error) {
	return 0, nil
}

func (n *nullFile) ReadAt(_ []byte, _ int64) (int, error) {
	return 0, io.EOF
}

func (n *nullFile) WriteString(s string) (int, error) {
	return len(s), nil
}

func newNullFile(name string) *nullFile {
	return &nullFile{
		name: name,
	}
}

type nullReadDirFile struct{}

func (vrd *nullReadDirFile) Stat() (fs.FileInfo, error) {
	return nullDirStat, nil
}

func (vrd *nullReadDirFile) Read(_ []byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: ".", Err: errors.New("is a directory")}
}

func (vrd *nullReadDirFile) Close() error {
	return nil
}

func (vrd *nullReadDirFile) ReadDir(_ int) ([]fs.DirEntry, error) {
	return []fs.DirEntry{}, nil
}

type nullFS struct {
	name string
}

func (fsys *nullFS) GetDeviceInfo() DeviceMap {
	return nullDeviceInfoMap
}

func (fsys *nullFS) URI() (*url.URL, error) {
	u, err := url.Parse(fsys.name)
	if err != nil {
		return nil, fmt.Errorf("URI %q is not valid, %w", fsys.name, err)
	}
	v := u.Query()
	v.Set("ro", "true")
	u.RawQuery = v.Encode()
	return u, nil
}

func (fsys *nullFS) String() string {
	return fmt.Sprintf("nullFS(%s)", uriOrDefault(fsys, fsys.name))
}

func (fsys *nullFS) Open(name string) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	if pathutil.IsDirName(name) {
		return &nullReadDirFile{}, nil
	}
	return newNullFile(name), nil
}

func (fsys *nullFS) Close() error {
	return nil
}

func (fsys *nullFS) Create(name string) (File, error) {
	if err := pathutil.Validate("create", name); err != nil {
		return nil, err
	}

	return newNullFile(name), nil
}

func (fsys *nullFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := pathutil.Validate("mkdir", name); err != nil {
		return err
	}
	return nil
}

func (fsys *nullFS) ReadFile(name string) ([]byte, error) {
	if err := pathutil.Validate("readfile", name); err != nil {
		return nil, err
	}
	return []byte{}, nil
}

func (fsys *nullFS) ReadLink(name string) (string, error) {
	if err := pathutil.Validate("readlink", name); err != nil {
		return "", err
	}
	return "", ufserrors.NewPathError("readlink", name, fs.ErrInvalid)
}

func (fsys *nullFS) Stat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("stat", name); err != nil {
		return nil, err
	}
	return nullDirStat, nil
}

func (fsys *nullFS) Lstat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("lstat", name); err != nil {
		return nil, err
	}
	isDir := pathutil.IsDirName(name)
	mode := fs.ModePerm
	size := int64(0)
	if isDir {
		mode = fs.ModeDir | fs.ModePerm
		size = osutil.EmptyDirSize
	}
	return &fsInfo{
		name:    name,
		size:    size,
		mode:    mode,
		modTime: unixEpochTime,
		isDir:   isDir,
		sys:     nil,
	}, nil
}

func (fsys *nullFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := pathutil.Validate("readdir", name); err != nil {
		return nil, err
	}
	return []fs.DirEntry{}, nil
}

func (fsys *nullFS) Glob(_ string) ([]string, error) {
	return []string{}, nil
}

func (fsys *nullFS) Remove(name string) error {
	if err := pathutil.Validate("remove", name); err != nil {
		return err
	}
	return nil
}

func (fsys *nullFS) RemoveAll(name string) error {
	if err := pathutil.Validate("removeall", name); err != nil {
		return err
	}
	return nil
}

func newNullFS(_ context.Context, name string) (FS, error) {
	return makeNullFS(name), nil
}

func makeNullFS(name string) *nullFS {
	return &nullFS{
		name: name,
	}
}

func isNullFSUri(name string) bool {
	return strings.HasPrefix(name, nullFSPrefix)
}
