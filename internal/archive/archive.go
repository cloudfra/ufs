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

// Package archive opens archives (zip, tar, 7z, rar and compressed tar) as
// read-only file systems. It is the only package that imports
// github.com/mholt/archives.
package archive

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mholt/archives"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var extList = []string{".tar", ".tar.gz", ".tar.bz2", ".tar.xz", ".tar.lz4", ".tar.br", ".tar.zst", ".rar", ".zip", ".7z"}

// FS is a read-only file system over the contents of an archive. Close
// releases the archive file, after which the FS must not be used.
type FS interface {
	fs.FS
	fs.StatFS
	fs.ReadFileFS
	fs.ReadDirFS
	io.Closer
}

// IsMountablePath reports whether name has the extension of an archive format
// that New can open.
func IsMountablePath(name string) bool {
	lowerPath := strings.ToLower(name)
	for _, suffix := range extList {
		if strings.HasSuffix(lowerPath, suffix) {
			return true
		}
	}
	return false
}

// New opens the archive at the local path name. A directory is opened as a
// file system over its contents.
func New(ctx context.Context, name string) (FS, error) {
	info, err := osutil.Stat(name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		fsys, err := archives.FileSystem(ctx, name, nil)
		if err != nil {
			return nil, err
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
		return nil, err
	}
	fsys, err := fileSystem(ctx, name, file)
	if err != nil {
		return nil, ufserrors.Join(err, file.Close())
	}
	return makeArchiveFS(fsys, name, file), nil
}

// NewFromFile opens the archive held by the open file, which must support
// seeking and random reads. name is the archive's file name and is used to
// help identify its format. The returned FS owns file and closes it on Close;
// on error file is left open.
func NewFromFile(ctx context.Context, name string, file fs.File) (FS, error) {
	readerAtSeeker, ok := file.(archives.ReaderAtSeeker)
	if !ok {
		return nil, fmt.Errorf("cannot mount archive %q: file does not support seek and random read", name)
	}
	fsys, err := fileSystem(ctx, name, readerAtSeeker)
	if err != nil {
		return nil, err
	}
	return makeArchiveFS(fsys, name, file), nil
}

type archiveFS struct {
	fsys    fs.FS
	name    string
	closer  io.Closer
	indexed sync.Once
	// isIndexed is set once ensureIndexed has successfully built the
	// underlying archive's implicit-directory index, letting Open skip
	// straight to fsys.fsys.Open on every later call instead of repeating the
	// detect-mismatch-then-retry dance. It stays false if indexing failed, so
	// a failed attempt keeps falling back to the slow path (which still works
	// for every explicit entry; only unindexed implicit directories need the
	// index).
	isIndexed atomic.Bool
}

func makeArchiveFS(fsys fs.FS, name string, closer io.Closer) *archiveFS {
	return &archiveFS{
		fsys:   fsys,
		name:   name,
		closer: closer,
	}
}

// ensureIndexed triggers the underlying archives.ArchiveFS's implicit-directory
// index build, which requires a full pass over every entry: the mholt/archives
// library has no mode to index directory structure alone, so this is the
// cheapest correct option without bypassing the library to parse archives
// ourselves. It runs at most once per archiveFS (sync.Once) and only when
// Open has already detected that the library returned the wrong entry for an
// implicit directory, so well-formed archives and plain file access never pay
// this cost.
func (fsys *archiveFS) ensureIndexed() {
	fsys.indexed.Do(func() {
		rdfs, ok := fsys.fsys.(fs.ReadDirFS)
		if !ok {
			return
		}
		if _, err := rdfs.ReadDir("."); err != nil {
			slog.Warn("failed to index archive", "name", fsys.name, "error", err)
			return
		}
		fsys.isIndexed.Store(true)
	})
}

// Open opens name in the underlying FS. If the archive returns an entry
// whose name doesn't match (implicit directory bug in non-indexed archives),
// it triggers an index build and retries once. Once the archive is known to be
// indexed, name always resolves correctly on the first try, so later calls
// skip the detect-and-retry dance entirely.
func (fsys *archiveFS) Open(name string) (fs.File, error) {
	if fsys.isIndexed.Load() {
		return fsys.fsys.Open(name)
	}

	f, err := fsys.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	if name == "." {
		return f, nil
	}
	info, statErr := f.Stat()
	if statErr != nil || info.Name() == path.Base(name) {
		return f, nil
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("cannot close %q, %w", name, err)
	}
	fsys.ensureIndexed()
	return fsys.fsys.Open(name)
}

func (fsys *archiveFS) Stat(name string) (fs.FileInfo, error) {
	// archives.ArchiveFS.Stat resolves implicit directories correctly on its
	// own (it compares the full in-archive path, not just the base name), so
	// unlike Open it never needs ensureIndexed. Using fs.Stat here also
	// avoids opening (and decompressing into) a content stream just to read
	// metadata.
	return fs.Stat(fsys.fsys, name)
}

func (fsys *archiveFS) ReadFile(name string) ([]byte, error) {
	return fs.ReadFile(fsys.fsys, name)
}

func (fsys *archiveFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return fs.ReadDir(fsys.fsys, name)
}

func (fsys *archiveFS) Close() error {
	fsys.fsys = nil
	if fsys.closer != nil {
		err := fsys.closer.Close()
		fsys.closer = nil
		return err
	}
	return nil
}
