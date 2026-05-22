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
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/mholt/archives"
)

const (
	archiveDirExt = ".d"
)

var (
	_ FS = (*archiveFS)(nil)

	archiveExtList = []string{".tar", ".tar.gz", ".tar.bz2", ".tar.xz", ".tar.lz4", ".tar.br", ".tar.zst", ".rar", ".zip", ".7z"}
)

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
	fsys fs.FS
	name string
}

func (fsys *archiveFS) String() string {
	return fsys.name
}

func (fsys *archiveFS) Open(name string) (fs.File, error) {
	if err := validPath("open", name); err != nil {
		return nil, err
	}
	return fsys.fsys.Open(name)
}

func (fsys *archiveFS) Close() error {
	fsys.fsys = nil
	return nil
}

func (fsys *archiveFS) Stat(name string) (fs.FileInfo, error) {
	if err := validPath("stat", name); err != nil {
		return nil, err
	}
	f, err := fsys.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	stat, statErr := f.Stat()
	closeErr := f.Close()
	if statErr != nil {
		return nil, joinErrors(statErr, closeErr)
	}
	return stat, closeErr
}

func (fsys *archiveFS) Create(name string) (File, error) {
	if err := validPath("create", name); err != nil {
		return nil, err
	}
	return nil, pathError("create", name, fmt.Errorf("archiveFS mounts are read-only, cannot create file, %q, %w", name, fs.ErrPermission))
}

func (fsys *archiveFS) MkdirAll(name string, perm fs.FileMode) error {
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

func newArchiveFSFromLocalFS(ctx context.Context, name string) (*archiveFS, error) {
	localPath := localFSNormalizePath(name)
	fsys, err := archives.FileSystem(ctx, localPath, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot mount %q as archiveFS, %w", name, err)
	}
	return makeArchiveFS(fsys, name), nil
}

func coerceToReaderAt(file fs.File) (io.ReaderAt, error) {
	readerAt, ok := file.(io.ReaderAt)
	if ok {
		return readerAt, nil
	} else {
		// TODO: This is very inefficient because it's reading a nested zip file into memory.
		data, err := io.ReadAll(file)
		if err != nil {
			return nil, err
		}
		return bytes.NewReader(data), err
	}
}

func newArchiveFSFromFile(file fs.File) (*archiveFS, error) {
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	name := stat.Name()
	if readerAtSeeker, ok := file.(archives.ReaderAtSeeker); ok {
		afs, err := archives.FileSystem(ctx, name, readerAtSeeker)
		if err != nil {
			return nil, err
		}
		return makeArchiveFS(afs, name), nil
	}

	readerAt, err := coerceToReaderAt(file)
	if err != nil {
		return nil, err
	}
	r := io.NewSectionReader(readerAt, 0, stat.Size())
	afs, err := archives.FileSystem(ctx, name, r)
	if err != nil {
		return nil, fmt.Errorf("cannot create archiveFS from file %q, %w", name, err)
	}
	return makeArchiveFS(afs, name), nil
}

func makeArchiveFS(fsys fs.FS, name string) *archiveFS {
	return &archiveFS{
		fsys: fsys,
		name: name,
	}
}

func newTempMountRemoteArchiveFS(name string) (FS, error) {
	tempDir, cleanup, err := createOSTempDirectory()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("cannot create temp directory, %w", err)
	}

	filename, err := downloadFile(tempDir, name)
	if err != nil {
		cleanup()
		return nil, err
	}

	ctx := context.Background()
	fsys, err := newArchiveFSFromLocalFS(ctx, filename)
	if err != nil {
		cleanup()
		return nil, err
	}
	return makeTempMountFS(fsys, tempDir, cleanup), nil
}
