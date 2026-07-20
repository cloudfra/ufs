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
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	localFSPrefix = "file:"
)

var (
	_ File             = (*os.File)(nil)
	_ localFSInterface = (*localFS)(nil)
)

type localFSInterface interface {
	FS
	fs.GlobFS
	realAbsPathGet
}

type localFS struct {
	osFS *os.Root
}

func (fsys *localFS) getDeviceInfo() map[string]deviceInfo {
	// TODO: This method will be broken out into OS platform specific configs to determine what the mapping should be. Since linux has multiple mappings in a directory structure we'll need to add the relevant ones here.
	return defaultDeviceMap
}

func (fsys *localFS) URI() *url.URL {
	return &url.URL{Scheme: "file", Path: coerceUnix(fsys.osFS.Name())}
}

func (fsys *localFS) String() string {
	return fmt.Sprintf("localFS(%s)", fsys.URI())
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
	defer f.Close()
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
	return globFS(fsys, pattern)
}

// globFS implements Glob for any FS that satisfies ReadDirFS, walking
// level-by-level so the FS's own ReadDir is used (avoids routing back through
// fs.Glob which would recurse if the FS implements GlobFS).
func globFS(fsys fs.ReadDirFS, pattern string) ([]string, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	return globWalk(fsys, cwdPath, pattern)
}

func globWalk(fsys fs.ReadDirFS, dir, pattern string) ([]string, error) {
	// Split the leftmost path component off the pattern.
	part, rest, _ := strings.Cut(pattern, "/")

	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var matches []string
	for _, e := range entries {
		matched, err := path.Match(part, e.Name())
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		entryPath := e.Name()
		if dir != cwdPath {
			entryPath = dir + "/" + e.Name()
		}
		if rest == "" {
			matches = append(matches, entryPath)
		} else if e.IsDir() {
			sub, err := globWalk(fsys, entryPath, rest)
			if err != nil {
				return nil, err
			}
			matches = append(matches, sub...)
		}
	}
	sort.Strings(matches)
	return matches, nil
}

func makeLocalFS(name string) (*localFS, error) {
	name = localFSNormalizePath(name)
	absPath, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	osFS, err := os.OpenRoot(absPath)
	if err != nil {
		return nil, err
	}
	return &localFS{
		osFS: osFS,
	}, nil
}

func newLocalFS(name string) (FS, error) {
	return makeLocalFS(name)
}

func isLocalFSUri(name string) bool {
	return strings.HasPrefix(name, localFSPrefix) || !strings.Contains(name, "://")
}
