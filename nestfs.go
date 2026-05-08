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
	"fmt"
	"io/fs"
)

var (
	_ FS             = (*nestFS)(nil)
	_ fs.ReadDirFS   = (*nestFS)(nil)
)

// nestFS is a wrapper for a base FS that supports automatic mounting of archives.
// This means that any archive can opened and read automatically. Archives are revealed via $filename.d name pattern.
type nestFS struct {
	fsys FS
}

func (fsys *nestFS) Open(name string) (fs.File, error) {
	if err := validPath("open", name); err != nil {
		return nil, err
	}

	return fsys.fsys.Open(name)
}

func (fsys *nestFS) Close() error {
	return fsys.fsys.Close()
}

func (fsys *nestFS) Create(name string) (File, error) {
	if err := validPath("create", name); err != nil {
		return nil, err
	}

	// TODO:
	return fsys.fsys.Create(name)
}

func (fsys *nestFS) MkdirAll(name string, perm fs.FileMode) error {
	// TODO:
	return fsys.fsys.MkdirAll(name, perm)
}

func (fsys *nestFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if cFsys, ok := fsys.fsys.(fs.ReadDirFS); ok {
		return cFsys.ReadDir(name)
	}

	f, err := fsys.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	readDirFile, ok := f.(fs.ReadDirFile)
	if !ok {
		return nil, &fs.PathError{
			Op:   "readDir",
			Path: name,
			Err:  fmt.Errorf("%s is not a directory", name),
		}
	}
	return readDirFile.ReadDir(-1)
}

func (fsys *nestFS) Stat(name string) (fs.FileInfo, error) {
	if cFsys, ok := fsys.fsys.(fs.StatFS); ok {
		return cFsys.Stat(name)
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

func (fsys *nestFS) Read(data []byte) (int, error) {
	if cFsys, ok := fsys.fsys.(fs.File); ok {
		return cFsys.Read(data)
	}

	return 0, nil
}

func newNestFS(name string) (FS, error) {
	fsys, err := newBaseFS(name)
	if err != nil {
		return nil, err
	}
	return &nestFS{
		fsys: fsys,
	}, nil
}
