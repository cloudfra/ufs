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
	"strings"

	"github.com/cloudfra/ufs/internal/httputil"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// archivefs.go holds the archive driver code shared by every platform: URI
// matching, driver registration and remote-archive downloads. The archiveFS
// implementation itself is platform-specific; each of these files defines
// archiveExtList, newArchiveFSFromLocalFS and newArchiveFSFromFile:
//
//   - archivefs_supported.go: archiveFS backed by github.com/mholt/archives.
//   - archivefs_unsupported.go: GOOS=plan9, where mholt/archives does not
//     build; no archive extensions match and every constructor fails with
//     errors.ErrUnsupported.

const (
	archiveDirExt   = ".d"
	archiveFSPrefix = "archive:"
)

func init() {
	Register(NewDriver("archive", func(ctx context.Context, name string) (FS, error) {
		return newArchiveFSFromLocalFS(ctx, strings.TrimPrefix(name, "archive://"))
	}, isArchiveFSUri, 1, true, false))
	Register(NewDriver("http-archive", newTempMountRemoteArchiveFS, isTempMountRemoteArchiveURI, 10000, true, false))
}

func isArchiveFSUri(name string) bool {
	return strings.HasPrefix(name, archiveFSPrefix)
}

func isMountableArchivePath(name string) bool {
	lowerPath := strings.ToLower(name)
	for _, suffix := range archiveExtList {
		if strings.HasSuffix(lowerPath, suffix) {
			return true
		}
	}
	return false
}

func isTempMountRemoteArchiveURI(name string) bool {
	return strings.HasPrefix(name, "http://") || strings.HasPrefix(name, "https://")
}

func newTempMountRemoteArchiveFS(ctx context.Context, name string) (FS, error) {
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
