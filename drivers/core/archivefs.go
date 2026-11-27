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
	"sync"
	"sync/atomic"

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
	// fsys is the archive this file system mounted. It is set when the archive
	// is opened and never changed afterwards, including by Close: Open, ReadDir,
	// ReadFile and Stat all read it without a lock, so writing it is a data race
	// against them, and an operation that read it just before the write kept an
	// archive whose file Close had already released. Closedness lives in
	// [archiveFS.closed] instead.
	fsys archive.FS
	name string

	// closed says Close has been called, after which this file system refuses
	// every operation that would work through fsys. Close claims it once, so
	// closing twice is not an error and only the first call lets go of the
	// archive. The flag alone is a check, not a decision: the decision to use
	// fsys and the use of it are one critical section with respect to Close
	// through [archiveFS.mu].
	closed atomic.Bool

	// mu is the lifetime guard. Every operation that works through fsys holds
	// a read lock across both its closed check and the call into the archive,
	// and Close takes the write lock to release the archive. After the CAS,
	// Close cannot reach the teardown until every operation that already
	// passed its check has finished its use, and an operation that starts
	// later takes the read lock either before or after the teardown and in
	// both cases sees closed and is refused. Readers stay concurrent with
	// each other; only Close waits.
	mu sync.RWMutex
}

// Open is [fs.FS]: it hands out the entry the archive holds for name. A closed
// file system reports [fs.ErrClosed].
func (fsys *archiveFS) Open(name string) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	if fsys.closed.Load() {
		fsys.mu.RUnlock()
		return nil, ufserrors.NewPathError("open", name, fs.ErrClosed)
	}
	file, err := fsys.fsys.Open(name)
	fsys.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return file, nil
}

// Close releases the archive. It is idempotent: only the first call closes the
// archive it mounted and every later call reports nil. Close never changes
// fsys, so an operation that was already running keeps the archive it is using
// instead of meeting a nil one. Having claimed closedness, Close takes mu to
// release the archive, so it waits for every operation that had already
// passed its closed check to finish its use before the archive is let go, and
// an operation that arrives afterwards reports [fs.ErrClosed].
func (fsys *archiveFS) Close() error {
	if !fsys.closed.CompareAndSwap(false, true) {
		return nil
	}
	fsys.mu.Lock()
	err := fsys.fsys.Close()
	fsys.mu.Unlock()
	return err
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
