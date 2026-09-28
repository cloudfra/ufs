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

//go:build !plan9

package ufs

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/mholt/archives"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// archiveExtList holds the file name suffixes that isMountableArchivePath
// treats as archives.
var archiveExtList = []string{".tar", ".tar.gz", ".tar.bz2", ".tar.xz", ".tar.lz4", ".tar.br", ".tar.zst", ".rar", ".zip", ".7z"}

func newArchiveFSFromLocalFS(ctx context.Context, name string) (*archiveFS, error) {
	info, err := osutil.Stat(name)
	if err != nil {
		return nil, fmt.Errorf("cannot mount %q as archiveFS, %w", name, err)
	}
	if info.IsDir() {
		fsys, err := archives.FileSystem(ctx, name, nil)
		if err != nil {
			return nil, fmt.Errorf("cannot mount %q as archiveFS, %w", name, err)
		}
		return makeArchiveFS(fsys, name, nil), nil
	}

	// Open the file ourselves and hand archives.FileSystem a stream rather than
	// a bare path. archives.ArchiveFS.Open re-opens the file with os.Open on every
	// call when given only a Path, and leaks that handle whenever the opened name
	// is a directory within the archive (its dirFile.Close is a no-op that never
	// references the opened file). Passing a Stream makes ArchiveFS reuse this
	// single file instead, so the only handle to close is the one we own here.
	file, err := osutil.Open(filepath.Clean(name))
	if err != nil {
		return nil, fmt.Errorf("cannot mount %q as archiveFS, %w", name, err)
	}
	fsys, err := archives.FileSystem(ctx, name, file)
	if err != nil {
		return nil, ufserrors.Join(fmt.Errorf("cannot mount %q as archiveFS, %w", name, err), file.Close())
	}
	return makeArchiveFS(fsys, name, file), nil
}

func newArchiveFSFromFile(ctx context.Context, file fs.File) (*archiveFS, error) {
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	readerAtSeeker, ok := file.(archives.ReaderAtSeeker)
	if !ok {
		return nil, fmt.Errorf("cannot mount archive %q: file does not support seek and random read", stat.Name())
	}
	afs, err := archives.FileSystem(ctx, stat.Name(), readerAtSeeker)
	if err != nil {
		return nil, err
	}
	result := makeArchiveFS(afs, stat.Name(), file)
	return result, nil
}
