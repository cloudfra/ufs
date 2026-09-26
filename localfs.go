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
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudfra/ufs/internal/globutil"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
)

const (
	localFSPrefix = "file:"
)

var (
	_ File             = (*os.File)(nil)
	_ localFSInterface = (*localFS)(nil)
)

func init() {
	Register(NewDriver("local", newLocalFS, isLocalFSUri, 10000, true, true))
}

type localFSInterface interface {
	WriteFS
	fs.GlobFS
	realAbsPathGet
}

type localFS struct {
	osFS *os.Root
}

func (fsys *localFS) URI() (*url.URL, error) {
	return &url.URL{Scheme: "file", Path: pathutil.CoerceUnix(fsys.osFS.Name())}, nil
}

func (fsys *localFS) String() string {
	return fmt.Sprintf("localFS(%s)", URIOrDefault(fsys, fsys.osFS.Name()))
}

func (fsys *localFS) getAbsPath(name string) (string, error) {
	return filepath.Abs(filepath.Join(fsys.osFS.Name(), name))
}

func (fsys *localFS) Open(name string) (fs.File, error) {
	if err := validLocalPath("open", name); err != nil {
		return nil, err
	}
	f, err := fsys.osFS.Open(name)
	if err != nil {
		return nil, err
	}
	return localFSWrapFile(f), nil
}

func (fsys *localFS) Close() error {
	return fsys.osFS.Close()
}

func (fsys *localFS) Create(name string) (File, error) {
	if err := validLocalPath("create", name); err != nil {
		return nil, err
	}
	return fsys.osFS.Create(name)
}

func (fsys *localFS) MkdirAll(name string, perm fs.FileMode) error {
	if err := validLocalPath("mkdir", name); err != nil {
		return err
	}
	return fsys.osFS.MkdirAll(name, perm)
}

func (fsys *localFS) ReadFile(name string) ([]byte, error) {
	if err := validLocalPath("readfile", name); err != nil {
		return nil, err
	}
	return fsys.osFS.ReadFile(name)
}

func (fsys *localFS) ReadLink(name string) (string, error) {
	if err := validLocalPath("readlink", name); err != nil {
		return "", err
	}
	return fsys.osFS.Readlink(name)
}

func (fsys *localFS) Stat(name string) (fs.FileInfo, error) {
	if err := validLocalPath("stat", name); err != nil {
		return nil, err
	}
	fi, err := fsys.osFS.Stat(name)
	if err != nil {
		return nil, err
	}
	return localFSNormalizeDirInfo(fi), nil
}

func (fsys *localFS) Lstat(name string) (fs.FileInfo, error) {
	if err := validLocalPath("lstat", name); err != nil {
		return nil, err
	}
	fi, err := fsys.osFS.Lstat(name)
	if err != nil {
		return nil, err
	}
	return localFSNormalizeDirInfo(fi), nil
}

func (fsys *localFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := validLocalPath("readdir", name); err != nil {
		return nil, err
	}
	f, err := fsys.osFS.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("failed to close directory during ReadDir", "path", name, "error", err)
		}
	}()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries, nil
}

func (fsys *localFS) Remove(name string) error {
	if err := validLocalPath("remove", name); err != nil {
		return err
	}
	return fsys.osFS.Remove(name)
}

func (fsys *localFS) RemoveAll(name string) error {
	if err := validLocalPath("removeall", name); err != nil {
		return err
	}
	return fsys.osFS.RemoveAll(name)
}

func (fsys *localFS) Glob(pattern string) ([]string, error) {
	return globutil.GlobFS(fsys, pattern)
}

func makeLocalFS(name string) (*localFS, error) {
	name = localFSNormalizePath(name)
	absPath, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	osFS, err := osutil.OpenRoot(absPath)
	if err != nil {
		return nil, err
	}
	return &localFS{
		osFS: osFS,
	}, nil
}

func newLocalFS(ctx context.Context, name string) (FS, error) {
	if isMountableArchivePath(name) {
		return newArchiveFSFromLocalFS(ctx, name)
	}
	return makeLocalFS(name)
}

func isLocalFSUri(name string) bool {
	if strings.HasPrefix(name, localFSPrefix) || !strings.Contains(name, "://") {
		return true
	}
	stat, err := osutil.Stat(name)
	if err == nil && stat != nil {
		return true
	}
	return false
}
