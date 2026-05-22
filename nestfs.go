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
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
)

var (
	_ FS        = (*nestFS)(nil)
	_ fs.GlobFS = (*nestFS)(nil)
)

func getPotentialArchives(name string) []string {
	components := strings.Split(name, unixPathSeparator)
	potentials := []string{}
	for idx, component := range components {
		if strings.HasSuffix(component, archiveDirExt) {
			potentials = append(potentials, strings.Join(components[0:idx+1], unixPathSeparator))
		}
	}
	return potentials
}

type mountMap struct {
	m        map[string]*nestFS
	baseName string
}

func (m *mountMap) put(name string, fsys *nestFS) error {
	for mountPoint := range m.m {
		if _, ok := removePathPrefix(mountPoint, name); ok {
			return pathError("mount", name, fmt.Errorf("mount %q conflicts with %q. You must change the order so that mounting is properly nested. mounts: %s, %+v", name, mountPoint, m.baseName, m.m))
		}
		if subName, ok := removePathPrefix(name, mountPoint); ok {
			return pathError("mount", name, fmt.Errorf("mount %q is nested within %q. To correct, mount %q as %q within %q. mounts: %s, %+v", name, mountPoint, name, subName, mountPoint, m.baseName, m.m))
		}
	}
	m.m[path.Clean(name)] = fsys
	return nil
}

func (m *mountMap) getDirectoryList(name string) []string {
	name = path.Clean(name)
	// If there's a mount that should handle the directory listing then we should defer to that.
	if _, ok := m.m[name]; ok {
		return []string{}
	}
	dirSet := map[string]any{}
	for mountPath := range m.m {
		subPath, ok := removePathPrefix(mountPath, name)
		if ok {
			dirSet[splitPath(subPath)[0]] = nil
		}
	}

	dirs := make([]string, 0, len(dirSet))
	for dir := range dirSet {
		dirs = append(dirs, dir)
	}

	sort.Strings(dirs)
	return dirs
}

func (m *mountMap) getMatchesBySubPath(name string) map[string]*nestFS {
	name = path.Clean(name)
	if fsys, ok := m.m[name]; ok {
		return map[string]*nestFS{
			"": fsys,
		}
	}
	matches := map[string]*nestFS{}
	for mountPath, subFS := range m.m {
		subPath, ok := removePathPrefix(mountPath, name)
		if ok {
			matches[subPath] = subFS
		}
	}

	return matches
}

func (m *mountMap) getMount(name string) *nestFS {
	name = path.Clean(name)
	nFS, ok := m.m[name]
	if !ok {
		return nil
	}
	return nFS
}

func (m *mountMap) getMountX(name string) (string, string, *nestFS, bool) {
	name = path.Clean(name)
	if isCwd(name) {
		return "", "", nil, false
	}
	targetMount := ""
	targetSubPath := ""
	var targetFS *nestFS
	for mountPath, subFS := range m.m {
		subPath, ok := removePathPrefix(name, mountPath)
		if ok && (len(targetMount) < len(mountPath) || targetMount == "") {
			targetMount = mountPath
			targetSubPath = subPath
			targetFS = subFS
		}
	}

	return targetMount, targetSubPath, targetFS, targetFS != nil
}

func (m *mountMap) Close() error {
	if m.m != nil {
		for mountPath, nfs := range m.m {
			if err := nfs.Close(); err != nil {
				return fmt.Errorf("cannot close mount %q, %w", mountPath, err)
			}
		}
		m.m = nil
	}
	return nil
}

func makeMountMap(baseName string) *mountMap {
	return &mountMap{
		m:        map[string]*nestFS{},
		baseName: baseName,
	}
}

// nestFS is a wrapper for a base FS that supports automatic mounting of archives.
// This means that any archive can opened and read automatically. Archives are revealed via $filename.d name pattern.
type nestFS struct {
	fsys   FS
	mounts *mountMap
}

func (fsys *nestFS) String() string {
	return fmt.Sprintf("nestFS(%s)", fsys.fsys.String())
}

func (fsys *nestFS) appendDirEntry(name string, entries []fs.DirEntry, err error) ([]fs.DirEntry, error) {
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	appendEntry := map[string]fs.DirEntry{}

	dirs := fsys.mounts.getDirectoryList(name)
	for _, dir := range dirs {
		appendEntry[dir] = makeVirtualDirEntry(dir)
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

func (fsys *nestFS) addMount(name string, mountedFS *nestFS) error {
	return fsys.mounts.put(name, mountedFS)
}

func (fsys *nestFS) mountArchive(name string) (*nestFS, error) {
	if maybeFS := fsys.mounts.getMount(name + archiveDirExt); maybeFS != nil {
		return maybeFS, nil
	}
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
	if err := fsys.addMount(name+archiveDirExt, wrapped); err != nil {
		return nil, err
	}
	return wrapped, nil
}

func (fsys *nestFS) getFSAndSubpath(name string) (*nestFS, string, error) {
	targetFS := fsys
	targetName := name
	for mountPath, subFS := range fsys.mounts.m {
		subPath, ok := removePathPrefix(name, mountPath)
		if ok && len(subPath) < len(targetName) {
			targetName = subPath
			targetFS = subFS
		}
	}

	archiveDirNames := getPotentialArchives(targetName)
	for _, archiveDirName := range archiveDirNames {
		archiveName := strings.TrimSuffix(archiveDirName, archiveDirExt)
		info, err := targetFS.Stat(archiveName)
		if info != nil && err == nil {
			subPath, ok := removePathPrefix(targetName, archiveDirName)
			if !ok {
				continue
			}
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

	if rdf, ok := f.(fs.ReadDirFile); ok {
		info, statErr := f.Stat()
		if statErr == nil && info.IsDir() {
			return makeNestReadDirFile(mountFS, subName, rdf), nil
		}
	}

	return f, nil
}

func (fsys *nestFS) Close() error {
	if err := fsys.mounts.Close(); err != nil {
		return err
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

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	if cFsys, ok := mountFS.fsys.(fs.ReadDirFS); ok {
		dirs, err := cFsys.ReadDir(subName)
		return mountFS.appendDirEntry(subName, dirs, err)
	}

	f, err := mountFS.fsys.Open(subName)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	readDirFile, ok := f.(fs.ReadDirFile)
	if !ok {
		return nil, pathError("readdir", name, fmt.Errorf("%s is not a directory", name))
	}

	dirs, err := readDirFile.ReadDir(-1)
	return mountFS.appendDirEntry(subName, dirs, err)
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
		return pathError(op, name, fs.ErrClosed)
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
		fsys:   fsys,
		mounts: makeMountMap(fsys.String()),
	}
}

type nestReadDirFile struct {
	fsys *nestFS
	name string
	rdf  fs.ReadDirFile
}

func (rdf *nestReadDirFile) Stat() (fs.FileInfo, error) {
	return rdf.rdf.Stat()
}

func (rdf *nestReadDirFile) Read(p []byte) (int, error) {
	return rdf.rdf.Read(p)
}

func (rdf *nestReadDirFile) Close() error {
	return rdf.rdf.Close()
}

func (rdf *nestReadDirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := rdf.rdf.ReadDir(n)
	return rdf.fsys.appendDirEntry(rdf.name, entries, err)
}

func makeNestReadDirFile(fsys *nestFS, name string, rdf fs.ReadDirFile) *nestReadDirFile {
	return &nestReadDirFile{
		fsys: fsys,
		name: name,
		rdf:  rdf,
	}
}
