// Copyright 2026 Cloudfra
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

package core

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/archive"
	"github.com/cloudfra/ufs/internal/httputil"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const (
	archiveFSPrefix = "archive:"
)

var (
	_ types.WriteFS = (*archiveFS)(nil)

	archiveDeviceInfo    = types.NewDeviceInfo("archive", "archive", 1, false)
	archiveDeviceInfoMap = types.NewDeviceMap(archiveDeviceInfo)
)

func init() {
	archiveDriver := ufs.NewDriver("archive", func(ctx context.Context, name string) (types.WriteFS, error) {
		return newArchiveFSFromLocalFS(ctx, strings.TrimPrefix(name, "archive://"))
	}, isArchiveFSUri, 1, true, false)
	archiveDriver.CreateFromFileFunc = newArchiveFSFromFile
	ufs.Register(archiveDriver)
	ufs.Register(ufs.NewDriver("http-archive", newTempMountRemoteArchiveFS, isTempMountRemoteArchiveURI, 10000, true, false))
}

func isArchiveFSUri(name string) bool {
	return strings.HasPrefix(name, archiveFSPrefix)
}

type archiveFS struct {
	fsys archive.FS
	name string
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

// newArchiveFSFromLocalFS mounts the archive at the host path name. The result
// is an interface, so that it is nil, not a nil *archiveFS in an interface,
// when err is not nil.
func newArchiveFSFromLocalFS(ctx context.Context, name string) (types.WriteFS, error) {
	fsys, err := archive.New(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("cannot mount %q as archiveFS, %w", name, err)
	}
	return makeArchiveFS(fsys, name), nil
}

// newArchiveFSFromFile mounts the archive held by the open file. The returned
// archiveFS owns file and closes it on Close; on error file is left open.
func newArchiveFSFromFile(ctx context.Context, file fs.File) (types.WriteFS, error) {
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

func newTempMountRemoteArchiveFS(ctx context.Context, name string) (types.WriteFS, error) {
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
		return nil, fmt.Errorf("cannot create archive ufs.FS from local file, %w", ufserrors.Join(err, cleanupErr))
	}
	return makeTempMountFS(fsys, name, tempDir, cleanup), nil
}
