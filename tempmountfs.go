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
	"io/fs"
	"log/slog"
	"net/url"
)

var _ localFSInterface = (*tempMountFS)(nil)

type tempMountFS struct {
	lfs    FS
	uri    string
	name   string
	closer func() error
}

func (fsys *tempMountFS) getDeviceInfo() map[string]deviceInfo {
	return fsys.lfs.getDeviceInfo()
}

func (fsys *tempMountFS) URI() *url.URL {
	u, err := url.Parse(fsys.uri)
	if err != nil {
		slog.Warn("tempMountFS: failed to parse URI, using opaque fallback", "uri", fsys.uri, "error", err)
		return &url.URL{Opaque: fsys.uri}
	}
	return u
}

func (fsys *tempMountFS) String() string {
	return fmt.Sprintf("tempMountFS(%s, tmpDir=%s)", fsys.URI(), coerceUnix(fsys.name))
}

func (fsys *tempMountFS) getAbsPath(name string) (string, error) {
	return AbsPath(fsys.lfs, name)
}

func (fsys *tempMountFS) Open(name string) (fs.File, error) {
	return fsys.lfs.Open(name)
}

func (fsys *tempMountFS) Close() error {
	closeErr := fsys.lfs.Close()
	cleanupErr := fsys.closer()
	return joinErrors(closeErr, cleanupErr)
}

func (fsys *tempMountFS) Create(name string) (File, error) {
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
	return globFS(fsys, pattern)
}

func (fsys *tempMountFS) Remove(name string) error {
	return fsys.lfs.Remove(name)
}

func (fsys *tempMountFS) RemoveAll(name string) error {
	return fsys.lfs.RemoveAll(name)
}

func newTempMountFS(uri string, prepare func(string) error) (FS, error) {
	tempDir, cleanup, err := createOSTempDirectory()
	if err != nil {
		cleanupErr := cleanup()
		return nil, joinErrors(fmt.Errorf("cannot create temp directory, %w", err), cleanupErr)
	}

	if err := prepare(tempDir); err != nil {
		cleanupErr := cleanup()
		return nil, joinErrors(fmt.Errorf("cannot prepare temp directory %s, %w", uri, err), cleanupErr)
	}

	lfs, err := newLocalFS(tempDir)
	if err != nil {
		cleanupErr := cleanup()
		return nil, joinErrors(fmt.Errorf("cannot create local fs for temp directory %s, %w", uri, err), cleanupErr)
	}

	return makeTempMountFS(lfs.(*localFS), uri, tempDir, cleanup), nil
}

func makeTempMountFS(lfs FS, uri string, name string, closer func() error) *tempMountFS {
	return &tempMountFS{
		lfs:    lfs,
		uri:    uri,
		name:   name,
		closer: closer,
	}
}
