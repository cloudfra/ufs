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

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var (
	_ localFSInterface = (*tempMountFS)(nil)
	_ types.WrapperFS  = (*tempMountFS)(nil)
)

type tempMountFS struct {
	lfs    types.WriteFS
	uri    string
	name   string
	closer func() error
}

func (fsys *tempMountFS) Open(name string) (fs.File, error) {
	return fsys.lfs.Open(name)
}

func (fsys *tempMountFS) Close() error {
	closeErr := fsys.lfs.Close()
	cleanupErr := fsys.closer()
	return ufserrors.Join(closeErr, cleanupErr)
}

// NewTempMountFS returns a file system for uri backed by a temporary local
// directory; prepare is called with the directory path to populate it.
func NewTempMountFS(ctx context.Context, uri string, prepare func(string) error) (types.WriteFS, error) {
	return newTempMountFS(ctx, uri, prepare)
}

func newTempMountFS(ctx context.Context, uri string, prepare func(string) error) (types.WriteFS, error) {
	tempDir, cleanup, err := osutil.CreateTempDirectory()
	if err != nil {
		cleanupErr := cleanup()
		return nil, ufserrors.Join(fmt.Errorf("cannot create temp directory, %w", err), cleanupErr)
	}

	if err := prepare(tempDir); err != nil {
		cleanupErr := cleanup()
		return nil, ufserrors.Join(fmt.Errorf("cannot prepare temp directory %s, %w", uri, err), cleanupErr)
	}

	lfs, err := newLocalFS(ctx, tempDir)
	if err != nil {
		cleanupErr := cleanup()
		return nil, ufserrors.Join(fmt.Errorf("cannot create local fs for temp directory %s, %w", uri, err), cleanupErr)
	}

	return makeTempMountFS(lfs, uri, tempDir, cleanup), nil
}

func makeTempMountFS(lfs types.WriteFS, uri string, name string, closer func() error) *tempMountFS {
	return &tempMountFS{
		lfs:    lfs,
		uri:    uri,
		name:   name,
		closer: closer,
	}
}
