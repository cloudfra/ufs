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
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/mholt/archives"
)

const (
	archiveDirExt   = ".d"
	archiveFSPrefix = "archive:"
)

var (
	_ FS = (*archiveFS)(nil)

	archiveExtList = []string{".tar", ".tar.gz", ".tar.bz2", ".tar.xz", ".tar.lz4", ".tar.br", ".tar.zst", ".rar", ".zip", ".7z"}

	archiveDeviceInfo = deviceInfo{
		name:        "archive",
		deviceType:  "archive",
		threadCount: 1,
	}
	archiveDeviceInfoMap = newDeviceInfoMap(archiveDeviceInfo)
)

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

type archiveFS struct {
	fsys    fs.FS
	name    string
	closer  io.Closer
	indexed sync.Once
}

func (fsys *archiveFS) getDeviceInfo() map[string]deviceInfo {
	return archiveDeviceInfoMap
}

// ensureIndexed forces the underlying archive FS to build its internal index
// by triggering a ReadDir on the root. This is needed for archives with
// implicit directories (directories inferred from file paths rather than
// stored as explicit entries).
func (fsys *archiveFS) ensureIndexed() {
	fsys.indexed.Do(func() {
		if rdfs, ok := fsys.fsys.(fs.ReadDirFS); ok {
			_, _ = rdfs.ReadDir(".")
		}
	})
}

func (fsys *archiveFS) URI() *url.URL {
	p := fsys.name
	if len(p) > 0 && p[0] != '/' {
		p = "/" + p
	}
	return &url.URL{Scheme: "archive", Path: p, RawQuery: "ro=true"}
}

func (fsys *archiveFS) String() string {
	return fmt.Sprintf("archiveFS(%s)", fsys.URI())
}

func (fsys *archiveFS) Open(name string) (fs.File, error) {
	if err := validPath("open", name); err != nil {
		return nil, err
	}
	f, err := fsys.fsys.Open(name)
	if err != nil {
		// Could be an implicit directory not visible without the index.
		fsys.ensureIndexed()
		f, err = fsys.fsys.Open(name)
		if err != nil {
			return nil, err
		}
		return wrapArchiveFile(f, name), nil
	}
	if name != "." {
		if info, statErr := f.Stat(); statErr == nil && info.Name() != path.Base(name) {
			// The archives library returned the wrong file — this happens
			// when the target is an implicit directory. Build the index
			// and retry.
			_ = f.Close()
			fsys.ensureIndexed()
			f, err = fsys.fsys.Open(name)
			if err != nil {
				return nil, err
			}
			return wrapArchiveFile(f, name), nil
		}
	}
	return wrapArchiveFile(f, name), nil
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

func (fsys *archiveFS) Stat(name string) (fs.FileInfo, error) {
	if err := validPath("stat", name); err != nil {
		return nil, err
	}
	info, err := fs.Stat(fsys.fsys, name)
	if err != nil {
		return nil, err
	}
	return fixArchiveName(info, name), nil
}

func (fsys *archiveFS) Create(name string) (File, error) {
	if err := validPath("create", name); err != nil {
		return nil, err
	}
	return nil, pathError("create", name, fmt.Errorf("archiveFS mounts are read-only, cannot create file, %q, %w", name, fs.ErrPermission))
}

func (fsys *archiveFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := validPath("mkdir", name); err != nil {
		return err
	}
	return pathError("mkdir", name, fmt.Errorf("archiveFS mounts are read-only, cannot create directory, %q, %w", name, fs.ErrPermission))
}

func (fsys *archiveFS) ReadFile(name string) ([]byte, error) {
	if err := validPath("readfile", name); err != nil {
		return nil, err
	}
	return fs.ReadFile(fsys.fsys, name)
}

func (fsys *archiveFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := validPath("readdir", name); err != nil {
		return nil, err
	}
	return fs.ReadDir(fsys.fsys, name)
}

func (fsys *archiveFS) ReadLink(name string) (string, error) {
	if err := validPath("readlink", name); err != nil {
		return "", err
	}
	// Archives contain no symlinks; every path is a regular file or directory.
	return "", pathError("readlink", name, fs.ErrInvalid)
}

func (fsys *archiveFS) Lstat(name string) (fs.FileInfo, error) {
	// Archives contain no symlinks, so Lstat == Stat.
	return fsys.Stat(name)
}

func (fsys *archiveFS) Remove(name string) error {
	if err := validPath("remove", name); err != nil {
		return err
	}
	return pathError("remove", name, fmt.Errorf("archiveFS mounts are read-only, cannot remove %q, %w", name, fs.ErrPermission))
}

func (fsys *archiveFS) RemoveAll(name string) error {
	if err := validPath("removeall", name); err != nil {
		return err
	}
	return pathError("removeall", name, fmt.Errorf("archiveFS mounts are read-only, cannot remove %q, %w", name, fs.ErrPermission))
}

// fixArchiveName returns info with a corrected Name() when the archives
// library returns the full path instead of the base name for implicit
// directories.
func fixArchiveName(info fs.FileInfo, name string) fs.FileInfo {
	want := path.Base(name)
	if info.Name() == want {
		return info
	}
	return fixedNameInfo{FileInfo: info, baseName: want}
}

type fixedNameInfo struct {
	fs.FileInfo
	baseName string
}

func (fi fixedNameInfo) Name() string { return fi.baseName }

// wrapArchiveFile wraps directory files returned by the archives library to
// fix two upstream bugs: incorrect Name() for implicit directories, and
// broken ReadDir paging (ReadDir(n<=0) doesn't advance the read position).
// Regular files are returned as-is.
func wrapArchiveFile(f fs.File, name string) fs.File {
	rdf, ok := f.(fs.ReadDirFile)
	if !ok {
		return f
	}
	return &archiveDirFile{ReadDirFile: rdf, baseName: path.Base(name)}
}

// archiveDirFile wraps a ReadDirFile to fix Name() and ReadDir paging.
type archiveDirFile struct {
	fs.ReadDirFile
	baseName string
	entries  []fs.DirEntry
	pos      int
	cached   bool
}

func (f *archiveDirFile) Stat() (fs.FileInfo, error) {
	info, err := f.ReadDirFile.Stat()
	if err != nil {
		return nil, err
	}
	if info.Name() != f.baseName {
		return fixedNameInfo{FileInfo: info, baseName: f.baseName}, nil
	}
	return info, nil
}

func (f *archiveDirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if !f.cached {
		entries, err := f.ReadDirFile.ReadDir(-1)
		if err != nil && err != io.EOF {
			return nil, err
		}
		f.entries = entries
		f.cached = true
	}
	if n <= 0 {
		entries := f.entries[f.pos:]
		f.pos = len(f.entries)
		return entries, nil
	}
	if f.pos >= len(f.entries) {
		return nil, io.EOF
	}
	end := min(f.pos+n, len(f.entries))
	entries := f.entries[f.pos:end]
	f.pos = end
	if f.pos >= len(f.entries) {
		return entries, io.EOF
	}
	return entries, nil
}

func newArchiveFSFromLocalFS(ctx context.Context, name string) (*archiveFS, error) {
	info, err := os.Stat(name)
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
	file, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("cannot mount %q as archiveFS, %w", name, err)
	}
	fsys, err := archives.FileSystem(ctx, name, file)
	if err != nil {
		return nil, joinErrors(fmt.Errorf("cannot mount %q as archiveFS, %w", name, err), file.Close())
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

func makeArchiveFS(fsys fs.FS, name string, closer io.Closer) *archiveFS {
	return &archiveFS{
		fsys:   fsys,
		name:   name,
		closer: closer,
	}
}

func newTempMountRemoteArchiveFS(ctx context.Context, name string) (FS, error) {
	tempDir, cleanup, err := createOSTempDirectory()
	if err != nil {
		cleanupErr := cleanup()
		return nil, fmt.Errorf("cannot create temp directory, %w", joinErrors(err, cleanupErr))
	}

	filename, err := downloadFile(ctx, tempDir, name)
	if err != nil {
		cleanupErr := cleanup()
		return nil, joinErrors(err, cleanupErr)
	}

	fsys, err := newArchiveFSFromLocalFS(ctx, filename)
	if err != nil {
		cleanupErr := cleanup()
		return nil, fmt.Errorf("cannot create archive FS from local file, %w", joinErrors(err, cleanupErr))
	}
	return makeTempMountFS(fsys, name, tempDir, cleanup), nil
}
