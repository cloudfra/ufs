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
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/archive"
	"github.com/cloudfra/ufs/internal/osutil"
)

const (
	localFSPrefix = "file:"
)

var (
	_ types.File       = (*os.File)(nil)
	_ localFSInterface = (*localFS)(nil)

	// TODO: Fold these into localFSInterface
	_ types.ChmodFS     = (*localFS)(nil)
	_ types.ChtimesFS   = (*localFS)(nil)
	_ types.LinkFS      = (*localFS)(nil)
	_ types.WriteFileFS = (*localFS)(nil)
)

func init() {
	ufs.Register(ufs.NewDriver("local", newLocalFS, isLocalFSUri, 10000, true, true))
}

type localFSInterface interface {
	types.WriteFS
	fs.GlobFS
	ufs.AbsPathGetter
}

type localFS struct {
	osFS *os.Root
}

func (fsys *localFS) Open(name string) (fs.File, error) {
	if err := validLocalPath("open", name); err != nil {
		return nil, err
	}
	f, err := fsys.osFS.Open(name)
	if err != nil {
		return nil, err
	}
	return localFSWrapFile(f), nil
}

func (fsys *localFS) Close() error {
	return fsys.osFS.Close()
}

// MakeLocalFS returns a local disk file system rooted at the file:// URI name.
//
// Deprecated: MakeLocalFS exists only to support the shared driver tests in
// drivers/testing and will be removed soon. Use New instead.
func MakeLocalFS(name string) (types.WriteFS, error) {
	fsys, err := makeLocalFS(name)
	if err != nil {
		// Not "return makeLocalFS(name)": a nil *localFS in the WriteFS
		// result would be a non-nil interface.
		return nil, err
	}
	return fsys, nil
}

func makeLocalFS(name string) (*localFS, error) {
	name = localFSNormalizePath(name)
	absPath, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	osFS, err := osutil.OpenRoot(absPath)
	if err != nil {
		return nil, err
	}
	return &localFS{
		osFS: osFS,
	}, nil
}

func newLocalFS(ctx context.Context, name string) (types.WriteFS, error) {
	if archive.IsMountablePath(name) {
		// The archive is opened by host path, so drop any file: prefix.
		return newArchiveFSFromLocalFS(ctx, localFSNormalizePath(name))
	}
	fsys, err := makeLocalFS(name)
	if err != nil {
		return nil, err
	}
	return fsys, nil
}

func isLocalFSUri(name string) bool {
	if strings.HasPrefix(name, localFSPrefix) || !strings.Contains(name, "://") {
		return true
	}
	stat, err := osutil.Stat(name)
	if err == nil && stat != nil {
		return true
	}
	return false
}
