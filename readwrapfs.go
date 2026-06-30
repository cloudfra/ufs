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
	"io"
	"io/fs"
	"net/url"
)

var _ ReadFS = (*readWrapFS)(nil)

type readWrapFS struct {
	fsys fs.FS
}

func (fsys *readWrapFS) getDeviceInfo() map[string]deviceInfo {
	return getDeviceInfoOrDefault(fsys.fsys)
}

func (s *readWrapFS) URI() *url.URL {
	return nil
}

func (s *readWrapFS) String() string {
	return fmt.Sprintf("readWrapFS(%T)", s.fsys)
}

func (s *readWrapFS) Open(name string) (fs.File, error) {
	if err := validPath("open", name); err != nil {
		return nil, err
	}
	return s.fsys.Open(name)
}

func (s *readWrapFS) Close() error {
	if c, ok := s.fsys.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func (s *readWrapFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := validPath("readdir", name); err != nil {
		return nil, err
	}
	if rdfs, ok := s.fsys.(fs.ReadDirFS); ok {
		return rdfs.ReadDir(name)
	}
	return fs.ReadDir(s.fsys, name)
}

func (s *readWrapFS) ReadFile(name string) ([]byte, error) {
	if err := validPath("readfile", name); err != nil {
		return nil, err
	}
	if rffs, ok := s.fsys.(fs.ReadFileFS); ok {
		return rffs.ReadFile(name)
	}
	return fs.ReadFile(s.fsys, name)
}

func (s *readWrapFS) Stat(name string) (fs.FileInfo, error) {
	if err := validPath("stat", name); err != nil {
		return nil, err
	}
	if sfs, ok := s.fsys.(fs.StatFS); ok {
		return sfs.Stat(name)
	}
	return fs.Stat(s.fsys, name)
}

func (s *readWrapFS) Lstat(name string) (fs.FileInfo, error) {
	if err := validPath("lstat", name); err != nil {
		return nil, err
	}
	if rlfs, ok := s.fsys.(fs.ReadLinkFS); ok {
		return rlfs.Lstat(name)
	}
	return s.Stat(name)
}

func (s *readWrapFS) ReadLink(name string) (string, error) {
	if err := validPath("readlink", name); err != nil {
		return "", err
	}
	if rlfs, ok := s.fsys.(fs.ReadLinkFS); ok {
		return rlfs.ReadLink(name)
	}
	return "", pathError("readlink", name, fs.ErrInvalid)
}

// FromFS wraps a standard library [fs.FS] as a read-only [ReadFS].
func FromFS(fsys fs.FS) ReadFS {
	return &readWrapFS{fsys: fsys}
}
