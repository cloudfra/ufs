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

// Package tempmountfs provides a file system backed by a temporary local
// directory that is removed when the file system is closed. Drivers use it to
// serve content they first have to download or unpack.
package tempmountfs

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/localfs"
	"github.com/cloudfra/ufs/internal/globutil"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var (
	_ ufs.WriteFS    = (*tempMountFS)(nil)
	_ fs.GlobFS      = (*tempMountFS)(nil)
	_ ufs.AbsPathGet = (*tempMountFS)(nil)
)

type tempMountFS struct {
	lfs    ufs.WriteFS
	uri    string
	name   string
	closer func() error
}

func (fsys *tempMountFS) GetDeviceInfo() ufs.DeviceMap {
	return fsys.lfs.GetDeviceInfo()
}

func (fsys *tempMountFS) URI() (*url.URL, error) {
	return url.Parse(fsys.uri)
}

func (fsys *tempMountFS) String() string {
	return fmt.Sprintf("tempMountFS(%s, tmpDir=%s)", ufs.URIOrDefault(fsys, fsys.uri), pathutil.CoerceUnix(fsys.name))
}

func (fsys *tempMountFS) GetAbsPath(name string) (string, error) {
	return ufs.AbsPath(fsys.lfs, name)
}

func (fsys *tempMountFS) Open(name string) (fs.File, error) {
	return fsys.lfs.Open(name)
}

func (fsys *tempMountFS) Close() error {
	closeErr := fsys.lfs.Close()
	cleanupErr := fsys.closer()
	return ufserrors.Join(closeErr, cleanupErr)
}

func (fsys *tempMountFS) Create(name string) (ufs.File, error) {
	return fsys.lfs.Create(name)
}

func (fsys *tempMountFS) MkdirAll(name string, perm fs.FileMode) error {
	return fsys.lfs.MkdirAll(name, perm)
}

func (fsys *tempMountFS) ReadFile(name string) ([]byte, error) {
	return fsys.lfs.ReadFile(name)
}

func (fsys *tempMountFS) ReadLink(name string) (string, error) {
	return fsys.lfs.ReadLink(name)
}

func (fsys *tempMountFS) Lstat(name string) (fs.FileInfo, error) {
	return fsys.lfs.Lstat(name)
}

func (fsys *tempMountFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return fsys.lfs.ReadDir(name)
}

func (fsys *tempMountFS) Stat(name string) (fs.FileInfo, error) {
	return fsys.lfs.Stat(name)
}

func (fsys *tempMountFS) Glob(pattern string) ([]string, error) {
	return globutil.GlobFS(fsys, pattern)
}

func (fsys *tempMountFS) Remove(name string) error {
	return fsys.lfs.Remove(name)
}

func (fsys *tempMountFS) RemoveAll(name string) error {
	return fsys.lfs.RemoveAll(name)
}

// New returns a file system for uri backed by a temporary local directory;
// prepare is called with the directory path to populate it.
func New(ctx context.Context, uri string, prepare func(string) error) (ufs.WriteFS, error) {
	return newTempMountFS(ctx, uri, prepare)
}

// Wrap returns a file system for uri that serves inner and, when closed,
// closes inner and then calls closer to remove the temporary directory name
// that inner reads from.
func Wrap(inner ufs.WriteFS, uri string, name string, closer func() error) ufs.WriteFS {
	return makeTempMountFS(inner, uri, name, closer)
}

func newTempMountFS(_ context.Context, uri string, prepare func(string) error) (ufs.WriteFS, error) {
	tempDir, cleanup, err := osutil.CreateTempDirectory()
	if err != nil {
		cleanupErr := cleanup()
		return nil, ufserrors.Join(fmt.Errorf("cannot create temp directory, %w", err), cleanupErr)
	}

	if err := prepare(tempDir); err != nil {
		cleanupErr := cleanup()
		return nil, ufserrors.Join(fmt.Errorf("cannot prepare temp directory %s, %w", uri, err), cleanupErr)
	}

	lfs, err := localfs.New(tempDir)
	if err != nil {
		cleanupErr := cleanup()
		return nil, ufserrors.Join(fmt.Errorf("cannot create local fs for temp directory %s, %w", uri, err), cleanupErr)
	}

	return makeTempMountFS(lfs, uri, tempDir, cleanup), nil
}

func makeTempMountFS(lfs ufs.WriteFS, uri string, name string, closer func() error) *tempMountFS {
	return &tempMountFS{
		lfs:    lfs,
		uri:    uri,
		name:   name,
		closer: closer,
	}
}
