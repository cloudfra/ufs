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
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/mholt/archives"
)

var (
	_ FS            = (*archiveFS)(nil)
	_ fs.ReadFileFS = (*archiveFS)(nil)
	_ fs.ReadDirFS  = (*archiveFS)(nil)
)

type archiveFS struct {
	fsys fs.FS
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

func newArchiveFSFromLocalFS(ctx context.Context, name string) (*archiveFS, error) {
	fsys, err := archives.FileSystem(ctx, name, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot mount %q as archiveFS, %w", name, err)
	}
	return makeArchiveFS(fsys), nil
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

	readerAt, err := coerceToReaderAt(file)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	format, _, err := archives.Identify(ctx, stat.Name(), io.NewSectionReader(readerAt, 0, stat.Size()))
	if err != nil && !errors.Is(err, archives.NoMatch) {
		return nil, err
	}
	if format != nil {
		// TODO: we only really need Extractor and Decompressor here, not the combined interfaces...
		if af, ok := format.(archives.Archival); ok {
			r := io.NewSectionReader(readerAt, 0, stat.Size())
			afs := archives.ArchiveFS{
				Stream: r,
				Format: af,
			}
			return makeArchiveFS(afs), nil
		}
	}
	return nil, fmt.Errorf("archive not recognized")
}

func makeArchiveFS(fsys fs.FS) *archiveFS {
	return &archiveFS{
		fsys: fsys,
	}
}
