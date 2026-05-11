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
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var (
	_ FS             = (*nestFS)(nil)
	_ fs.ReadDirFS   = (*nestFS)(nil)
	_ fs.ReadFileFS  = (*nestFS)(nil)
	_ fs.ReadLinkFS  = (*nestFS)(nil)
	_ fs.GlobFS      = (*nestFS)(nil)
	_ fs.StatFS      = (*nestFS)(nil)
	_ fs.ReadDirFile = (*virtualReadDirFile)(nil)
)

func removePathComponent(name string, mountPath string) string {
	if isCwd(name) {
		return mountPath
	}
	return strings.TrimLeft(strings.TrimPrefix(name, mountPath), "\\/")
}

func getPotentialArchives(name string) []string {
	components := strings.Split(name, string(os.PathSeparator))
	potentials := []string{}
	for idx, component := range components {
		if strings.HasSuffix(component, archiveDirExt) {
			potentials = append(potentials, filepath.Join(components[0:idx+1]...))
		}
	}
	return potentials
}

// nestFS is a wrapper for a base FS that supports automatic mounting of archives.
// This means that any archive can opened and read automatically. Archives are revealed via $filename.d name pattern.
type nestFS struct {
	fsys  FS
	fsMap map[string]*nestFS
}

func (fsys *nestFS) appendDirEntry(name string, entries []fs.DirEntry, err error) ([]fs.DirEntry, error) {
	if err != nil {
		return nil, err
	}
	appendEntry := map[string]fs.DirEntry{}
	for mountPoint, _ := range fsys.fsMap {
		dir := removePathComponent(name, mountPoint)
		if dir != "" {
			parts := splitPath(dir)
			mountName := parts[0]
			appendEntry[mountName] = &virtualDirEntry{
				name: mountName,
			}
		}
	}
	for _, entry := range entries {
		if isMountableArchivePath(entry.Name()) {
			mountName := entry.Name() + ".d"
			appendEntry[mountName] = &virtualDirEntry{
				name: mountName,
			}
		}
	}
	if len(appendEntry) == 0 {
		return entries, nil
	}
	for _, entry := range entries {
		delete(appendEntry, entry.Name())
	}

	for _, entry := range appendEntry {
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(left fs.DirEntry, right fs.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})

	return entries, nil
}

func (fsys *nestFS) addMount(name string, mountedFS *nestFS) {
	fsys.fsMap[name] = mountedFS
}

func (fsys *nestFS) mountArchive(name string) (*nestFS, error) {
	ctx := context.Background()
	lfs, ok := fsys.fsys.(*localFS)
	var newFS *archiveFS
	if ok {
		absName, err := lfs.getAbsPath(name)
		if err != nil {
			return nil, pathError("mount", name, err)
		}
		newFS, err = newArchiveFSFromLocalFS(ctx, absName)
		if err != nil {
			return nil, pathError("mount", name, err)
		}
	} else {
		f, err := fsys.Open(name)
		if err != nil {
			return nil, pathError("mount", name, err)
		}
		newFS, err = newArchiveFSFromFile(f)
		if err != nil {
			return nil, pathError("mount", name, err)
		}
	}

	wrapped := makeNestFS(newFS)
	fsys.fsMap[name+archiveDirExt] = wrapped
	return wrapped, nil
}

func (fsys *nestFS) getFSAndSubpath(name string) (*nestFS, string, error) {
	targetFS := fsys
	targetName := name
	for mountPath, subFS := range fsys.fsMap {
		subPath := removePathComponent(name, mountPath)
		if len(subPath) < len(targetName) {
			targetName = subPath
			targetFS = subFS
		}
	}

	archiveDirNames := getPotentialArchives(targetName)
	for _, archiveDirName := range archiveDirNames {
		archiveName := strings.TrimSuffix(archiveDirName, archiveDirExt)
		info, err := targetFS.Stat(archiveName)
		if info != nil && err == nil {
			subPath := removePathComponent(targetName, archiveDirName)
			subFS, err := targetFS.mountArchive(archiveName)
			if err != nil {
				return nil, "", pathError("mount", name, fmt.Errorf("cannot mount archive %s, %w", archiveName, err))
			}
			targetFS, targetName, err := subFS.getFSAndSubpath(subPath)
			if err != nil {
				return nil, "", pathError("mount", name, fmt.Errorf("cannot mount archive %s, %w", archiveName, err))
			}
			return targetFS, targetName, nil
		}
	}

	return targetFS, targetName, nil
}

type virtualReadDirFile struct {
	fsys *nestFS
	name string
	base fs.ReadDirFile
}

func (vrd *virtualReadDirFile) Stat() (fs.FileInfo, error) {
	return vrd.base.Stat()
}

func (vrd *virtualReadDirFile) Read(p []byte) (int, error) {
	return vrd.base.Read(p)
}

func (vrd *virtualReadDirFile) Close() error {
	return vrd.base.Close()
}

func (vrd *virtualReadDirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := vrd.base.ReadDir(n)
	return vrd.fsys.appendDirEntry(vrd.name, entries, err)
}

func (fsys *nestFS) Open(name string) (fs.File, error) {
	if err := fsys.validPath("open", name); err != nil {
		return nil, err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	f, err := mountFS.fsys.Open(subName)
	if err != nil {
		return nil, err
	}

	readDirFile, ok := f.(fs.ReadDirFile)
	if ok {
		return &virtualReadDirFile{
			fsys: fsys,
			name: name,
			base: readDirFile,
		}, nil
	}
	return f, nil
}

func (fsys *nestFS) Close() error {
	if fsys.fsMap != nil {
		for mountPath, nfs := range fsys.fsMap {
			if err := nfs.Close(); err != nil {
				return fmt.Errorf("cannot close mount %q, %w", mountPath, err)
			}
		}
		fsys.fsMap = nil
	}

	if fsys.fsys != nil {
		if err := fsys.fsys.Close(); err != nil {
			return fmt.Errorf("cannot close file system %q, %w", fsys.fsys, err)
		}
		fsys.fsys = nil
	}
	return nil
}

func (fsys *nestFS) Create(name string) (File, error) {
	if err := fsys.validPath("create", name); err != nil {
		return nil, err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	return mountFS.fsys.Create(subName)
}

func (fsys *nestFS) MkdirAll(name string, perm fs.FileMode) error {
	if err := fsys.validPath("mkdir", name); err != nil {
		return err
	}
	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return err
	}

	return mountFS.fsys.MkdirAll(subName, perm)
}

func (fsys *nestFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := fsys.validPath("readdir", name); err != nil {
		return nil, err
	}

	if cFsys, ok := fsys.fsys.(fs.ReadDirFS); ok {
		entries, err := cFsys.ReadDir(name)
		return fsys.appendDirEntry(name, entries, err)
	}

	f, err := fsys.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	readDirFile, ok := f.(fs.ReadDirFile)
	if !ok {
		return nil, pathError("readDir", name, fmt.Errorf("%s is not a directory", name))
	}
	entries, err := readDirFile.ReadDir(-1)
	return fsys.appendDirEntry(name, entries, err)
}

func (fsys *nestFS) Stat(name string) (fs.FileInfo, error) {
	if err := fsys.validPath("stat", name); err != nil {
		return nil, err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	if cFsys, ok := mountFS.fsys.(fs.StatFS); ok {
		return cFsys.Stat(subName)
	}

	f, err := mountFS.Open(subName)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

func (fsys *nestFS) ReadFile(name string) ([]byte, error) {
	if err := fsys.validPath("readfile", name); err != nil {
		return nil, err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	if cFsys, ok := mountFS.fsys.(fs.ReadFileFS); ok {
		return cFsys.ReadFile(subName)
	}
	return fs.ReadFile(mountFS.fsys, subName)
}

func (fsys *nestFS) ReadLink(name string) (string, error) {
	if err := fsys.validPath("readlink", name); err != nil {
		return "", err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return "", err
	}

	if cFsys, ok := mountFS.fsys.(fs.ReadLinkFS); ok {
		return cFsys.ReadLink(subName)
	}

	return "", pathError("readlink", name, fs.ErrInvalid)
}

func (fsys *nestFS) Lstat(name string) (fs.FileInfo, error) {
	if err := fsys.validPath("lstat", name); err != nil {
		return nil, err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	if cFsys, ok := mountFS.fsys.(fs.ReadLinkFS); ok {
		return cFsys.Lstat(subName)
	}
	return mountFS.Stat(subName)
}

func (fsys *nestFS) Glob(pattern string) ([]string, error) {
	if cFsys, ok := fsys.fsys.(fs.GlobFS); ok {
		return cFsys.Glob(pattern)
	}
	return globFS(fsys, pattern)
}

func (fsys *nestFS) validPath(op string, name string) error {
	if err := validPath(op, name); err != nil {
		return err
	}
	if fsys.fsys == nil {
		return fmt.Errorf("cannot %s %q, file system is closed", op, name)
	}
	return nil
}

func newNestFS(name string) (FS, error) {
	fsys, err := newBaseFS(name)
	if err != nil {
		return nil, err
	}
	return makeNestFS(fsys), nil
}

func makeNestFS(fsys FS) *nestFS {
	return &nestFS{
		fsys:  fsys,
		fsMap: map[string]*nestFS{},
	}
}
