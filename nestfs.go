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
	"sync"
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
	mu       sync.RWMutex
	m        map[string]*nestFS
	baseName string
}

func (m *mountMap) put(name string, fsys *nestFS) error {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.RLock()
	defer m.mu.RUnlock()
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
	dirs := []string{}
	for dir := range dirSet {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

func (m *mountMap) getMatchesBySubPath(name string) map[string]*nestFS {
	name = path.Clean(name)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if fsys, ok := m.m[name]; ok {
		return map[string]*nestFS{"": fsys}
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.m[path.Clean(name)]
}

func (m *mountMap) getMountX(name string) (string, string, *nestFS, bool) {
	name = path.Clean(name)
	if isCwd(name) {
		return "", "", nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
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

// Close closes all mounted sub-filesystems. On error it still closes the
// remaining mounts and collects all errors.
func (m *mountMap) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		return nil
	}
	var errs []error
	for mountPath, nfs := range m.m {
		if err := nfs.Close(); err != nil {
			errs = append(errs, fmt.Errorf("cannot close mount %q: %w", mountPath, err))
		}
	}
	m.m = nil
	return errors.Join(errs...)
}

func makeMountMap(baseName string) *mountMap {
	return &mountMap{
		m:        map[string]*nestFS{},
		baseName: baseName,
	}
}

// nestFS is a wrapper for a base FS that supports automatic mounting of archives.
// This means that any archive can opened and read automatically. Archives are revealed via $filename.d name pattern.
// nestFS is safe for concurrent use by multiple goroutines.
type nestFS struct {
	mu     sync.RWMutex
	fsys   FS
	mounts *mountMap
}

func (fsys *nestFS) String() string {
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()
	if fsys.fsys == nil {
		return "nestFS(closed)"
	}
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

// mountArchive lazily mounts an archive file under name+archiveDirExt.
// Uses double-checked locking so concurrent calls for the same archive are safe.
// Must NOT be called while holding fsys.mu (it is called from getFSAndSubpath,
// which is called from exported methods that hold fsys.mu.RLock).
func (fsys *nestFS) mountArchive(name string) (*nestFS, error) {
	key := name + archiveDirExt

	// Fast path: already mounted.
	if maybeFS := fsys.mounts.getMount(key); maybeFS != nil {
		return maybeFS, nil
	}

	// Create the archive FS outside any lock — this may be slow.
	ctx := context.Background()
	lfs, ok := fsys.fsys.(*localFS)
	var newFS *archiveFS
	var err error
	if ok {
		absName, absErr := lfs.getAbsPath(name)
		if absErr != nil {
			return nil, pathError("mount", name, absErr)
		}
		newFS, err = newArchiveFSFromLocalFS(ctx, absName)
	} else {
		// Use the base FS directly (not nestFS.Open) to avoid re-entrant locking.
		f, openErr := fsys.fsys.Open(name)
		if openErr != nil {
			return nil, pathError("mount", name, openErr)
		}
		newFS, err = newArchiveFSFromFile(f)
	}
	if err != nil {
		return nil, pathError("mount", name, err)
	}

	wrapped := makeNestFS(newFS)

	// Slow path: store under write lock with double-check to handle races.
	fsys.mounts.mu.Lock()
	if existing := fsys.mounts.m[key]; existing != nil {
		fsys.mounts.mu.Unlock()
		wrapped.Close() // discard the duplicate we just created
		return existing, nil
	}
	fsys.mounts.m[key] = wrapped
	fsys.mounts.mu.Unlock()
	return wrapped, nil
}

// getFSAndSubpath resolves a path to the responsible nestFS and the sub-path
// within it, following explicit mounts and lazily mounting archives as needed.
// Must be called with fsys.mu.RLock held.
func (fsys *nestFS) getFSAndSubpath(name string) (*nestFS, string, error) {
	targetFS := fsys
	targetName := name

	// Phase 1: find the deepest explicit mount that covers name.
	// Read mounts under the mountMap's own lock (separate from nestFS.mu).
	fsys.mounts.mu.RLock()
	for mountPath, subFS := range fsys.mounts.m {
		subPath, ok := removePathPrefix(name, mountPath)
		if ok && len(subPath) < len(targetName) {
			targetName = subPath
			targetFS = subFS
		}
	}
	fsys.mounts.mu.RUnlock()

	// Phase 2: check for archive virtual paths and auto-mount them.
	archiveDirNames := getPotentialArchives(targetName)
	for _, archiveDirName := range archiveDirNames {
		archiveName := strings.TrimSuffix(archiveDirName, archiveDirExt)
		// Use the base FS's Stat directly to avoid re-acquiring nestFS.mu.
		info, err := targetFS.fsys.Stat(archiveName)
		if info != nil && err == nil {
			subPath, ok := removePathPrefix(targetName, archiveDirName)
			if !ok {
				continue
			}
			subFS, err := targetFS.mountArchive(archiveName)
			if err != nil {
				return nil, "", pathError("mount", name, fmt.Errorf("cannot mount archive %s, %w", archiveName, err))
			}
			// Recurse into the archive's nestFS. It has its own RLock so we
			// must not hold any lock from the parent nestFS here — we don't
			// because mountArchive handles its own locking.
			targetFS, targetName, err = subFS.getFSAndSubpath(subPath)
			if err != nil {
				return nil, "", pathError("mount", name, fmt.Errorf("cannot mount archive %s, %w", archiveName, err))
			}
			return targetFS, targetName, nil
		}
	}

	return targetFS, targetName, nil
}

func (fsys *nestFS) Open(name string) (fs.File, error) {
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

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
	fsys.mu.Lock()
	defer fsys.mu.Unlock()

	if err := fsys.mounts.Close(); err != nil {
		return err
	}

	if fsys.fsys != nil {
		name := fsys.fsys.String()
		if err := fsys.fsys.Close(); err != nil {
			return fmt.Errorf("cannot close file system %q, %w", name, err)
		}
		fsys.fsys = nil
	}
	return nil
}

func (fsys *nestFS) Create(name string) (File, error) {
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

	if err := fsys.validPath("create", name); err != nil {
		return nil, err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	// Ensure parent directory exists so behaviour is consistent across backends.
	if dir := path.Dir(subName); !isCwd(dir) {
		if err := mountFS.fsys.MkdirAll(dir, fs.ModePerm); err != nil {
			return nil, err
		}
	}

	return mountFS.fsys.Create(subName)
}

func (fsys *nestFS) MkdirAll(name string, perm fs.FileMode) error {
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

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
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

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
		return nil, pathError("readDir", name, fmt.Errorf("%s is not a directory", name))
	}

	dirs, err := readDirFile.ReadDir(-1)
	return mountFS.appendDirEntry(subName, dirs, err)
}

func (fsys *nestFS) Stat(name string) (fs.FileInfo, error) {
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

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
	f, err := mountFS.fsys.Open(subName)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

func (fsys *nestFS) ReadFile(name string) ([]byte, error) {
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

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
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

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
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

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
	if cFsys, ok := mountFS.fsys.(fs.StatFS); ok {
		return cFsys.Stat(subName)
	}
	f, err := mountFS.fsys.Open(subName)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

// Glob always walks through the nestFS layer so patterns match across nested
// mounts and auto-mounted archive paths.
func (fsys *nestFS) Glob(pattern string) ([]string, error) {
	return globFS(fsys, pattern)
}

// validPath checks both the path format and that the FS is not closed.
// Must be called with fsys.mu held (at minimum RLock).
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
