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

// Package httparchivefs provides a file system for accessing archive files over HTTP.
package httparchivefs

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/tempmountfs"
	"github.com/cloudfra/ufs/internal/httputil"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func init() {
	ufs.Register(ufs.NewDriver("http-archive", newTempMountRemoteArchiveFS, isTempMountRemoteArchiveURI, 10000, true, false))
}

func isTempMountRemoteArchiveURI(name string) bool {
	return strings.HasPrefix(name, "http://") || strings.HasPrefix(name, "https://")
}

func New(ctx context.Context, name string) (ufs.FS, error) {
	return newTempMountRemoteArchiveFSWithClient(ctx, nil, name)
}

func NewWithClient(ctx context.Context, httpClient *http.Client, name string) (ufs.FS, error) {
	return newTempMountRemoteArchiveFSWithClient(ctx, httpClient, name)
}

func newTempMountRemoteArchiveFS(ctx context.Context, name string) (ufs.FS, error) {
	return newTempMountRemoteArchiveFSWithClient(ctx, nil, name)
}

func newTempMountRemoteArchiveFSWithClient(ctx context.Context, httpClient *http.Client, name string) (ufs.FS, error) {
	tempDir, cleanup, err := osutil.CreateTempDirectory()
	if err != nil {
		cleanupErr := cleanup()
		return nil, fmt.Errorf("cannot create temp directory, %w", ufserrors.Join(err, cleanupErr))
	}

	filename, err := httputil.DownloadFileWith(ctx, httpClient, tempDir, name)
	if err != nil {
		cleanupErr := cleanup()
		return nil, ufserrors.Join(err, cleanupErr)
	}

	fsys, err := ufs.NewArchiveFSFromLocalFS(ctx, filename)
	if err != nil {
		cleanupErr := cleanup()
		return nil, fmt.Errorf("cannot create archive FS from local file, %w", ufserrors.Join(err, cleanupErr))
	}
	return tempmountfs.MakeTempMountFS(fsys, name, tempDir, cleanup), nil
}
