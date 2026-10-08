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

	"github.com/cloudfra/ufs/internal/archive"
	"github.com/cloudfra/ufs/internal/httputil"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const (
	archiveDirExt   = ".d"
	archiveFSPrefix = "archive:"
)

var (
	_ WriteFS = (*archiveFS)(nil)

	archiveDeviceInfo    = NewDeviceInfo("archive", "archive", 1, false)
	archiveDeviceInfoMap = NewDeviceMap(archiveDeviceInfo)
)

func init() {
	Register(NewDriver("archive", func(ctx context.Context, name string) (WriteFS, error) {
		return newArchiveFSFromLocalFS(ctx, strings.TrimPrefix(name, "archive://"))
	}, isArchiveFSUri, 1, true, false))
	Register(NewDriver("http-archive", newTempMountRemoteArchiveFS, isTempMountRemoteArchiveURI, 10000, true, false))
}

func isArchiveFSUri(name string) bool {
	return strings.HasPrefix(name, archiveFSPrefix)
}

type archiveFS struct {
	fsys archive.FS
	name string
}

func (fsys *archiveFS) GetDeviceInfo() DeviceMap {
	return archiveDeviceInfoMap
}

func (fsys *archiveFS) URI() (*url.URL, error) {
	p := fsys.name
	if len(p) > 0 && p[0] != '/' {
		p = "/" + p
	}
	return &url.URL{Scheme: "archive", Path: p, RawQuery: "ro=true"}, nil
}

func (fsys *archiveFS) String() string {
	return fmt.Sprintf("archiveFS(%s)", URIOrDefault(fsys, fsys.name))
}

func (fsys *archiveFS) Open(name string) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	return fsys.fsys.Open(name)
}

func (fsys *archiveFS) Close() error {
	inner := fsys.fsys
	fsys.fsys = nil
	if inner == nil {
		return nil
	}
	return inner.Close()
}

func (fsys *archiveFS) Stat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("stat", name); err != nil {
		return nil, err
	}
	return fsys.fsys.Stat(name)
}

func (fsys *archiveFS) Create(name string) (File, error) {
	if err := pathutil.Validate("create", name); err != nil {
		return nil, err
	}
	return nil, ufserrors.NewPathError("create", name, fmt.Errorf("archiveFS mounts are read-only, cannot create file, %q, %w", name, fs.ErrPermission))
}

func (fsys *archiveFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := pathutil.Validate("mkdir", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("mkdir", name, fmt.Errorf("archiveFS mounts are read-only, cannot create directory, %q, %w", name, fs.ErrPermission))
}

func (fsys *archiveFS) ReadFile(name string) ([]byte, error) {
	if err := pathutil.Validate("readfile", name); err != nil {
		return nil, err
	}
	return fs.ReadFile(fsys.fsys, name)
}

func (fsys *archiveFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := pathutil.Validate("readdir", name); err != nil {
		return nil, err
	}
	return fsys.fsys.ReadDir(name)
}

func (fsys *archiveFS) ReadLink(name string) (string, error) {
	if err := pathutil.Validate("readlink", name); err != nil {
		return "", err
	}
	// Archives contain no symlinks; every path is a regular file or directory.
	return "", ufserrors.NewPathError("readlink", name, fs.ErrInvalid)
}

func (fsys *archiveFS) Lstat(name string) (fs.FileInfo, error) {
	// Archives contain no symlinks, so Lstat == Stat.
	return fsys.Stat(name)
}

func (fsys *archiveFS) Remove(name string) error {
	if err := pathutil.Validate("remove", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("remove", name, fmt.Errorf("archiveFS mounts are read-only, cannot remove %q, %w", name, fs.ErrPermission))
}

func (fsys *archiveFS) RemoveAll(name string) error {
	if err := pathutil.Validate("removeall", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("removeall", name, fmt.Errorf("archiveFS mounts are read-only, cannot remove %q, %w", name, fs.ErrPermission))
}

func newArchiveFSFromLocalFS(ctx context.Context, name string) (*archiveFS, error) {
	fsys, err := archive.New(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("cannot mount %q as archiveFS, %w", name, err)
	}
	return makeArchiveFS(fsys, name), nil
}

// newArchiveFSFromFile mounts the archive held by the open file. The returned
// archiveFS owns file and closes it on Close; on error file is left open.
func newArchiveFSFromFile(ctx context.Context, file fs.File) (*archiveFS, error) {
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	fsys, err := archive.NewFromFile(ctx, stat.Name(), file)
	if err != nil {
		return nil, err
	}
	return makeArchiveFS(fsys, stat.Name()), nil
}

func makeArchiveFS(fsys archive.FS, name string) *archiveFS {
	return &archiveFS{
		fsys: fsys,
		name: name,
	}
}

func isTempMountRemoteArchiveURI(name string) bool {
	return strings.HasPrefix(name, "http://") || strings.HasPrefix(name, "https://")
}

func newTempMountRemoteArchiveFS(ctx context.Context, name string) (WriteFS, error) {
	tempDir, cleanup, err := osutil.CreateTempDirectory()
	if err != nil {
		cleanupErr := cleanup()
		return nil, fmt.Errorf("cannot create temp directory, %w", ufserrors.Join(err, cleanupErr))
	}

	filename, err := httputil.DownloadFile(ctx, tempDir, name)
	if err != nil {
		cleanupErr := cleanup()
		return nil, ufserrors.Join(err, cleanupErr)
	}

	fsys, err := newArchiveFSFromLocalFS(ctx, filename)
	if err != nil {
		cleanupErr := cleanup()
		return nil, fmt.Errorf("cannot create archive FS from local file, %w", ufserrors.Join(err, cleanupErr))
	}
	return makeTempMountFS(fsys, name, tempDir, cleanup), nil
}
