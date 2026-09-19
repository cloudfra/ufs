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
	"time"

	"github.com/cloudfra/ufs/internal/device"
	"github.com/cloudfra/ufs/internal/file"
	"github.com/cloudfra/ufs/internal/pathutil"
)

const (
	nullFSPrefix = "null:"
)

// unixEpochTime is the zero-cost stand-in ModTime for nullFS entries, which
// have no real modification time to report.
var unixEpochTime = time.Time{}

var (
	_ File           = (*nullFile)(nil)
	_ WriteFS        = (*nullFS)(nil)
	_ fs.GlobFS      = (*nullFS)(nil)
	_ fs.ReadDirFile = (*nullReadDirFile)(nil)

	nullDirStat = file.New(file.Params{
		Name:    ".",
		Size:    emptyDirSize,
		Mode:    fs.ModeDir | fs.ModePerm,
		ModTime: unixEpochTime,
		IsDir:   true,
		Sys:     nil,
	})

	nullDeviceInfo    = device.New("null", "null", 1, false)
	nullDeviceInfoMap = device.NewMap(nullDeviceInfo)
)

func init() {
	Register(Driver{
		Name:       "null",
		MatchFunc:  isNullFSUri,
		CreateFunc: newNullFS,
		Priority:   1,
		Standard:   true,
		ReadWrite:  false,
	})
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
		size = emptyDirSize
	}
	return file.New(file.Params{
		Name:    path.Base(n.name),
		Size:    size,
		Mode:    mode,
		ModTime: unixEpochTime,
		IsDir:   isDir,
		Sys:     nil,
	}), nil
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

func (fsys *nullFS) getDeviceInfo() device.Map {
	return nullDeviceInfoMap
}

func (fsys *nullFS) URI() *url.URL {
	u, _ := url.Parse(fsys.name)
	v := u.Query()
	v.Set("ro", "true")
	u.RawQuery = v.Encode()
	return u
}

func (fsys *nullFS) String() string {
	return fmt.Sprintf("nullFS(%s)", fsys.URI())
}

func (fsys *nullFS) Open(name string) (fs.File, error) {
	if err := pathutil.ValidPath("open", name); err != nil {
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
	if err := pathutil.ValidPath("create", name); err != nil {
		return nil, err
	}

	return newNullFile(name), nil
}

func (fsys *nullFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := pathutil.ValidPath("mkdir", name); err != nil {
		return err
	}
	return nil
}

func (fsys *nullFS) ReadFile(name string) ([]byte, error) {
	if err := pathutil.ValidPath("readfile", name); err != nil {
		return nil, err
	}
	return []byte{}, nil
}

func (fsys *nullFS) ReadLink(name string) (string, error) {
	if err := pathutil.ValidPath("readlink", name); err != nil {
		return "", err
	}
	return "", pathutil.PathError("readlink", name, fs.ErrInvalid)
}

func (fsys *nullFS) Stat(name string) (fs.FileInfo, error) {
	if err := pathutil.ValidPath("stat", name); err != nil {
		return nil, err
	}
	return nullDirStat, nil
}

func (fsys *nullFS) Lstat(name string) (fs.FileInfo, error) {
	if err := pathutil.ValidPath("lstat", name); err != nil {
		return nil, err
	}
	isDir := pathutil.IsDirName(name)
	mode := fs.ModePerm
	size := int64(0)
	if isDir {
		mode = fs.ModeDir | fs.ModePerm
		size = emptyDirSize
	}
	return file.New(file.Params{
		Name:    name,
		Size:    size,
		Mode:    mode,
		ModTime: unixEpochTime,
		IsDir:   isDir,
		Sys:     nil,
	}), nil
}

func (fsys *nullFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := pathutil.ValidPath("readdir", name); err != nil {
		return nil, err
	}
	return []fs.DirEntry{}, nil
}

func (fsys *nullFS) Glob(_ string) ([]string, error) {
	return []string{}, nil
}

func (fsys *nullFS) Remove(name string) error {
	if err := pathutil.ValidPath("remove", name); err != nil {
		return err
	}
	return nil
}

func (fsys *nullFS) RemoveAll(name string) error {
	if err := pathutil.ValidPath("removeall", name); err != nil {
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
