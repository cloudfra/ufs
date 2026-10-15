// Copyright 2026 Cloudfra
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
	"io/fs"
	"log/slog"
	"net/url"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/archive"
	"github.com/cloudfra/ufs/internal/globutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/ops"
)

// bufferMode selects how a file opened through a nestFS keeps the content that
// has to be buffered to make it seekable. The buffering itself lives in the
// polyfills package.
type bufferMode = polyfills.BufferMode

const (
	// archiveDirExt is the suffix of the virtual directory that holds the
	// content of an archive: a.zip is listed at a.zip.d.
	archiveDirExt = ".d"

	// archiveURIPrefix is put in front of the host path of an archive to open
	// it through the archive driver.
	archiveURIPrefix = "archive://"

	// renameOp is the operation name recorded in the errors that Rename
	// returns.
	renameOp = "rename"
)

// FSArgs holds optional parameters for constructing a nestFS.
type FSArgs struct {
	BufMode bufferMode
}

var (
	_ FS                        = (*nestFS)(nil)
	_ fs.GlobFS                 = (*nestFS)(nil)
	_ AbsPathGetter             = (*nestFS)(nil)
	_ types.MountedArchiveDirFS = (*nestFS)(nil)
	_ types.DeviceInfoGet       = (*mountMap)(nil)
)

func getPotentialArchives(name string) []string {
	components := strings.Split(name, pathutil.UnixSeparator)
	potentials := []string{}
	for idx, component := range components {
		if strings.HasSuffix(component, archiveDirExt) {
			potentials = append(potentials, strings.Join(components[0:idx+1], pathutil.UnixSeparator))
		}
	}
	return potentials
}

type mountMap struct {
	mu       sync.RWMutex
	m        map[string]*nestFS
	baseName string
}

func (m *mountMap) DeviceInfo() types.DeviceMap {
	combined := types.DeviceMap{}
	m.mu.RLock()
	for mountPoint, fsys := range m.m {
		combined = combined.Combine(mountPoint, fsys.DeviceInfo())
	}
	m.mu.RUnlock()
	return combined
}

func (m *mountMap) put(name string, fsys *nestFS) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for mountPoint := range m.m {
		if _, ok := pathutil.RemovePrefix(mountPoint, name); ok {
			return ufserrors.NewPathError("mount", name, fmt.Errorf("mount %q conflicts with %q. You must change the order so that mounting is properly nested. mounts: %s, %+v", name, mountPoint, m.baseName, m.m))
		}
		if subName, ok := pathutil.RemovePrefix(name, mountPoint); ok {
			return ufserrors.NewPathError("mount", name, fmt.Errorf("mount %q is nested within %q. To correct, mount %q as %q within %q. mounts: %s, %+v", name, mountPoint, name, subName, mountPoint, m.baseName, m.m))
		}
	}
	m.m[path.Clean(name)] = fsys
	return nil
}

func (m *mountMap) getDirectoryList(name string) []string {
	name = path.Clean(name)
	m.mu.RLock()
	// If there's a mount that should handle the directory listing then we should defer to that.
	if _, ok := m.m[name]; ok {
		m.mu.RUnlock()
		return []string{}
	}
	dirSet := map[string]any{}
	for mountPath := range m.m {
		subPath, ok := pathutil.RemovePrefix(mountPath, name)
		if ok {
			dirSet[pathutil.Split(subPath)[0]] = nil
		}
	}
	m.mu.RUnlock()

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
	if fsys, ok := m.m[name]; ok {
		m.mu.RUnlock()
		return map[string]*nestFS{
			"": fsys,
		}
	}
	matches := map[string]*nestFS{}
	for mountPath, subFS := range m.m {
		subPath, ok := pathutil.RemovePrefix(mountPath, name)
		if ok {
			matches[subPath] = subFS
		}
	}
	m.mu.RUnlock()

	return matches
}

// mountsUnder returns the paths of the mounts at or below name.
func (m *mountMap) mountsUnder(name string) []string {
	name = path.Clean(name)
	var found []string
	m.mu.RLock()
	for mountPath := range m.m {
		if _, ok := pathutil.RemovePrefix(mountPath, name); ok {
			found = append(found, mountPath)
		}
	}
	m.mu.RUnlock()
	return found
}

func (m *mountMap) getMount(name string) *nestFS {
	name = path.Clean(name)
	m.mu.RLock()
	nFS, ok := m.m[name]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	return nFS
}

func (m *mountMap) getClosestMount(name string) (string, string, *nestFS, bool) {
	name = path.Clean(name)
	if pathutil.IsCwd(name) {
		return "", "", nil, false
	}
	targetMount := ""
	targetSubPath := ""
	var targetFS *nestFS
	m.mu.RLock()
	for mountPath, subFS := range m.m {
		subPath, ok := pathutil.RemovePrefix(name, mountPath)
		if ok && (len(targetMount) < len(mountPath) || targetMount == "") {
			targetMount = mountPath
			targetSubPath = subPath
			targetFS = subFS
		}
	}
	m.mu.RUnlock()

	return targetMount, targetSubPath, targetFS, targetFS != nil
}

// releaseAll releases every mount, holding the mount lock for the whole
// operation. See [mountMap.releaseAllNoLock] for what releasing a mount means.
func (m *mountMap) releaseAll() error {
	m.mu.Lock()
	err := m.releaseAllNoLock()
	m.mu.Unlock()
	return err
}

// releaseAllNoLock releases every mount; the caller must hold m.mu for writing.
//
// A mount that ufs opened for itself is closed and dropped from the map: ufs
// opened it to serve the paths inside it rather than to hold a file system the
// caller gave it, so closing it releases the handle it keeps on the file it was
// opened from, along with any local copy of that file. In practice such a mount
// is the content directory of an archive that nestFS mounts automatically, which
// is mounted again from the file that holds it on next access. A mount that the
// caller configured, whether it built the file system itself or gave a URI for
// ufs to open, is the caller's to keep: it stays mounted and releases the mounts
// below it, recursively.
//
// Every mount is attempted. The errors are joined into the result, and a mount
// that could not be released is left mounted, so that a later release or close
// can try again.
func (m *mountMap) releaseAllNoLock() error {
	var errs []error
	for name, nfs := range m.m {
		if nfs.closeOnRelease() {
			if closeErr := m.removeNoLock(name, nfs); closeErr != nil {
				errs = append(errs, closeErr)
			}
		} else {
			if releaseErr := nfs.Release(); releaseErr != nil {
				errs = append(errs, releaseErr)
			}
		}
	}
	return ufserrors.Join(errs...)
}

// remove closes the mount at name and drops it from the map. A name that is not
// mounted is not an error. If the mount cannot be closed it stays in the map, so
// that it can be closed again later.
func (m *mountMap) remove(name string) error {
	m.mu.Lock()
	var err error
	if nfs, ok := m.m[name]; ok {
		err = m.removeNoLock(name, nfs)
	}
	m.mu.Unlock()
	return err
}

// removeNoLock closes and drops a mount that the caller has already looked up;
// the caller must hold m.mu for writing. A mount that cannot be closed is left
// in the map.
func (m *mountMap) removeNoLock(name string, nfs *nestFS) error {
	if closeErr := nfs.Close(); closeErr != nil {
		return fmt.Errorf("failed to close mount %q, %w", name, closeErr)
	}
	delete(m.m, name)
	return nil
}

func (m *mountMap) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	if m.m != nil {
		for mountPath, nfs := range m.m {
			if err := nfs.Close(); err != nil {
				errs = append(errs, fmt.Errorf("cannot close mount %q, %w", mountPath, err))
			}
		}
		m.m = nil
	}
	return ufserrors.Join(errs...)
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
	fsys   types.WriteFS
	ctx    context.Context
	mounts *mountMap
	args   FSArgs

	// inArchive is set on the mount of an archive's content, which cannot be
	// changed.
	inArchive bool

	// autoMounted is set on the content directory of an archive that nestFS
	// mounts automatically, which is a mount ufs opened for itself rather than
	// one the caller configured. ufs does not need such a mount to stay open
	// for the caller, so releasing lets go of it; see [nestFS.closeOnRelease].
	// A mount the caller configured, by handing over a file system or by giving
	// a URI, is not marked, so releasing keeps it.
	autoMounted bool
}

func (fsys *nestFS) GetAbsPath(name string) (string, error) {
	if err := fsys.validPath(absPathOp, name); err != nil {
		return "", err
	}
	if rfs, ok := fsys.fsys.(AbsPathGetter); ok {
		return rfs.GetAbsPath(name)
	}
	return "", realAbsPathNotSupported(fsys, name)
}

func (fsys *nestFS) DeviceInfo() types.DeviceMap {
	base := fsys.fsys.DeviceInfo()
	return base.Combine("", fsys.mounts.DeviceInfo())
}

func (fsys *nestFS) URI() (*url.URL, error) {
	base, err := fsys.fsys.URI()
	if base == nil || err != nil {
		return nil, nil
	}
	u := *base
	vals := u.Query()
	fsys.mounts.mu.RLock()
	defer fsys.mounts.mu.RUnlock()
	for p, mfs := range fsys.mounts.m {
		if strings.HasSuffix(p, archiveDirExt) {
			continue
		}
		if mu, err := mfs.URI(); mu != nil && err == nil {
			vals.Set(p, mu.String())
		}
	}
	u.RawQuery = vals.Encode()
	return &u, nil
}

func (fsys *nestFS) String() string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "nestFS(%s)", fsys.fsys.String())
	fsys.mounts.mu.RLock()
	defer fsys.mounts.mu.RUnlock()
	mountPaths := make([]string, 0, len(fsys.mounts.m))
	for p := range fsys.mounts.m {
		mountPaths = append(mountPaths, p)
	}
	sort.Strings(mountPaths)
	for _, p := range mountPaths {
		fmt.Fprintf(&b, "\n  %s -> %s", p, fsys.mounts.m[p].String())
	}
	return b.String()
}

func (fsys *nestFS) appendDirEntry(name string, entries []fs.DirEntry, err error) ([]fs.DirEntry, error) {
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	notExist := err != nil
	appendEntry := map[string]fs.DirEntry{}

	dirs := fsys.mounts.getDirectoryList(name)
	for _, dir := range dirs {
		if strings.HasSuffix(dir, archiveDirExt) && archive.IsMountablePath(strings.TrimSuffix(dir, archiveDirExt)) {
			archivePath := path.Join(name, strings.TrimSuffix(dir, archiveDirExt))
			if _, statErr := fs.Stat(fsys.fsys, archivePath); errors.Is(statErr, fs.ErrNotExist) {
				if removeErr := fsys.mounts.remove(path.Join(name, dir)); removeErr != nil {
					slog.Warn("cannot unmount the content directory of a missing archive", "path", path.Join(name, dir), "error", removeErr)
				}
				continue
			}
		}
		appendEntry[dir] = makeVirtualDirEntry(dir)
	}

	if notExist && len(appendEntry) == 0 {
		// The directory is missing from the base file system and no mount
		// lives below it, so it does not exist here either.
		return nil, err
	}

	for _, entry := range entries {
		if archive.IsMountablePath(entry.Name()) {
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

	// The base file system may hand out a slice that it keeps, as archives
	// do with their index, so the virtual entries go into a copy: appending
	// to and sorting the original would change the next listing.
	entries = slices.Grow(slices.Clone(entries), len(appendEntry))
	for _, entry := range appendEntry {
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(left fs.DirEntry, right fs.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})

	return entries, nil
}

// isMountParent reports whether a file system is mounted somewhere below
// name. Such a directory exists in this file system even when the base file
// system has no entry for it.
func (fsys *nestFS) isMountParent(name string) bool {
	return len(fsys.mounts.getDirectoryList(name)) > 0
}

func (fsys *nestFS) addMount(name string, mountedFS *nestFS) error {
	return fsys.mounts.put(name, mountedFS)
}

// addAutoMount mounts a file system that ufs opened for itself, which today
// means the content directory of an archive found in the tree. Releasing such a
// mount lets go of it; see [nestFS.autoMounted]. A mount the caller configures,
// from a file system it built or from a URI, is added with [nestFS.addMount]
// instead, so that releasing keeps it.
func (fsys *nestFS) addAutoMount(name string, mountedFS *nestFS) error {
	mountedFS.autoMounted = true
	return fsys.addMount(name, mountedFS)
}

// IsMountedArchiveDir reports whether name (a full path within this FS) is a
// virtual directory backed by a mounted archive. It returns true only when:
//   - name ends with archiveDirExt
//   - the trimmed name satisfies archive.IsMountablePath
//   - the archive file is not confirmed absent; any Stat error other than
//     ErrNotExist is treated as "file likely exists" so that a permission-denied
//     error does not cause Walk to descend and trigger a mount failure
//
// Note: if a real directory named "foo.zip.d" coexists with "foo.zip" in the
// same FS, nestFS's path routing always redirects access through the archive
// (see getFSAndSubpath). The real directory is unreachable via this FS
// regardless of what this method returns; that is a nestFS limitation.
func (fsys *nestFS) IsMountedArchiveDir(name string) bool {
	if !strings.HasSuffix(name, archiveDirExt) || !fs.ValidPath(name) {
		return false
	}
	archiveName := strings.TrimSuffix(name, archiveDirExt)
	if !archive.IsMountablePath(archiveName) {
		return false
	}
	_, err := fsys.Stat(archiveName)
	return !errors.Is(err, fs.ErrNotExist)
}

func (fsys *nestFS) mountArchive(name string) (*nestFS, error) {
	if maybeFS := fsys.mounts.getMount(name + archiveDirExt); maybeFS != nil {
		return maybeFS, nil
	}
	ctx := fsys.ctx
	// A base file system that has a host path for the archive opens it by that
	// path, any other one through an open file.
	var absName string
	var onHost bool
	if hostFS, ok := fsys.fsys.(AbsPathGetter); ok {
		hostPath, err := hostFS.GetAbsPath(name)
		if err == nil {
			absName = hostPath
			onHost = true
		}
	}
	var newFS types.WriteFS
	if onHost {
		var err error
		newFS, err = newBaseFS(ctx, archiveURIPrefix+absName)
		if err != nil {
			return nil, ufserrors.NewPathError("mount", name, err)
		}
	} else {
		f, err := fsys.Open(name)
		if err != nil {
			return nil, ufserrors.NewPathError("mount", name, err)
		}
		newFS, err = newBaseFSFromFile(ctx, archiveURIPrefix+name, f)
		if err != nil {
			return nil, ufserrors.Join(ufserrors.NewPathError("mount", name, err), f.Close())
		}
	}

	wrapped := makeNestFS(ctx, newFS)
	wrapped.inArchive = true
	if err := fsys.addAutoMount(name+archiveDirExt, wrapped); err != nil {
		return nil, err
	}
	return wrapped, nil
}

func (fsys *nestFS) getFSAndSubpath(name string) (*nestFS, string, error) {
	_, targetName, targetFS, ok := fsys.mounts.getClosestMount(name)
	if !ok {
		targetFS = fsys
		targetName = name
	}

	archiveDirNames := getPotentialArchives(targetName)
	for _, archiveDirName := range archiveDirNames {
		archiveName := strings.TrimSuffix(archiveDirName, archiveDirExt)
		info, err := targetFS.Stat(archiveName)
		if info != nil && err == nil {
			subPath, ok := pathutil.RemovePrefix(targetName, archiveDirName)
			if !ok {
				continue
			}
			subFS, err := targetFS.mountArchive(archiveName)
			if err != nil {
				return nil, "", ufserrors.NewPathError("mount", name, fmt.Errorf("cannot mount archive %s, %w", archiveName, err))
			}
			targetFS, targetName, err := subFS.getFSAndSubpath(subPath)
			if err != nil {
				return nil, "", ufserrors.NewPathError("mount", name, fmt.Errorf("cannot mount archive %s, %w", archiveName, err))
			}
			return targetFS, targetName, nil
		}
	}

	return targetFS, targetName, nil
}

// rewritePathError rewrites the path of an error that a file system mounted
// below name reported for subName, so that the error names the path the caller
// used instead of the path relative to that file system's root. It is the
// error counterpart of [mountPrefix], which does the same for watch events.
// Only an error that is an [*fs.PathError] by itself is rewritten: the message
// of a wrapped error may quote the path, and an absolute path names a place
// outside ufs, so neither is a mount-relative path. The replacement keeps Op
// and Err, so errors.Is and errors.As keep working.
func rewritePathError(err error, name, subName string) error {
	if err == nil {
		return nil
	}
	prefix := mountPrefix(name, subName)
	if prefix == "" {
		return err
	}
	pathErr, ok := err.(*fs.PathError)
	if !ok || path.IsAbs(pathErr.Path) {
		return err
	}
	return &fs.PathError{
		Op:   pathErr.Op,
		Path: path.Join(prefix, pathErr.Path),
		Err:  pathErr.Err,
	}
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
		if errors.Is(err, fs.ErrNotExist) && mountFS.isMountParent(subName) {
			return makeReadDirFile(mountFS, subName), nil
		}
		return nil, rewritePathError(err, name, subName)
	}

	if rdf, ok := f.(fs.ReadDirFile); ok {
		info, statErr := f.Stat()
		if statErr == nil && info.IsDir() {
			return makeNestReadDirFile(mountFS, subName, rdf), nil
		}
	}

	return polyfills.ToBufferedFile(f, true, mountFS.args.BufMode)
}

func (fsys *nestFS) Close() error {
	mountErr := fsys.mounts.Close()

	var baseErr error
	if fsys.fsys != nil {
		if err := fsys.fsys.Close(); err != nil {
			baseErr = fmt.Errorf("cannot close file system %q, %w", fsys.fsys, err)
		}
		fsys.fsys = nil
	}
	return ufserrors.Join(mountErr, baseErr)
}

func (fsys *nestFS) Create(name string) (types.File, error) {
	if err := fsys.validPath("create", name); err != nil {
		return nil, err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	f, err := mountFS.fsys.Create(subName)
	if err != nil {
		return nil, rewritePathError(err, name, subName)
	}
	return polyfills.ToBufferedFile(f, false, mountFS.args.BufMode)
}

func (fsys *nestFS) MkdirAll(name string, perm fs.FileMode) error {
	if err := fsys.validPath("mkdir", name); err != nil {
		return err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return err
	}

	if err := mountFS.fsys.MkdirAll(subName, perm); err != nil {
		return rewritePathError(err, name, subName)
	}
	return nil
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
		return mountFS.appendDirEntry(subName, dirs, rewritePathError(err, name, subName))
	}

	f, err := mountFS.fsys.Open(subName)
	if err != nil {
		return nil, rewritePathError(err, name, subName)
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("failed to close file during ReadDir", "path", name, "error", err)
		}
	}()

	readDirFile, ok := f.(fs.ReadDirFile)
	if !ok {
		return nil, ufserrors.NewPathError("readdir", name, fmt.Errorf("%s is not a directory", name))
	}

	dirs, err := readDirFile.ReadDir(-1)
	return mountFS.appendDirEntry(subName, dirs, rewritePathError(err, name, subName))
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
		info, err := cFsys.Stat(subName)
		if errors.Is(err, fs.ErrNotExist) && mountFS.isMountParent(subName) {
			return makeVirtualDirEntry(path.Base(subName)), nil
		}
		return info, rewritePathError(err, name, subName)
	}
	f, err := mountFS.Open(subName)
	if err != nil {
		return nil, rewritePathError(err, name, subName)
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("failed to close file during Stat", "path", name, "error", err)
		}
	}()
	info, err := f.Stat()
	return info, rewritePathError(err, name, subName)
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
		data, err := cFsys.ReadFile(subName)
		return data, rewritePathError(err, name, subName)
	}
	data, err := fs.ReadFile(mountFS.fsys, subName)
	return data, rewritePathError(err, name, subName)
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
		target, err := cFsys.ReadLink(subName)
		return target, rewritePathError(err, name, subName)
	}

	return "", ufserrors.NewPathError("readlink", name, fs.ErrInvalid)
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
		info, err := cFsys.Lstat(subName)
		if errors.Is(err, fs.ErrNotExist) && mountFS.isMountParent(subName) {
			return makeVirtualDirEntry(path.Base(subName)), nil
		}
		return info, rewritePathError(err, name, subName)
	}
	info, err := mountFS.Stat(subName)
	return info, rewritePathError(err, name, subName)
}

func (fsys *nestFS) Remove(name string) error {
	if err := fsys.validPath("remove", name); err != nil {
		return err
	}
	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return err
	}
	if err := mountFS.fsys.Remove(subName); err != nil {
		return rewritePathError(err, name, subName)
	}
	return nil
}

func (fsys *nestFS) RemoveAll(name string) error {
	if err := fsys.validPath("removeall", name); err != nil {
		return err
	}
	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return err
	}
	if err := mountFS.fsys.RemoveAll(subName); err != nil {
		return rewritePathError(err, name, subName)
	}
	return nil
}

// Rename moves oldPath to newPath. Within one mounted file system it uses
// [ops.Rename] on that file system; between two it copies and then removes
// oldPath. Paths are resolved through nested mounts, including a nestFS
// mounted as a file system, down to the file system that holds them, so a
// copy never includes the mounts or archive content below oldPath.
//
// It fails with [fs.ErrPermission] for a mount point, a directory with a
// mount below it, or a path inside a mounted archive, and with [fs.ErrExist]
// when newPath is a directory that only exists because a mount is below it,
// or when newPath is an archive whose content directory (newPath plus ".d")
// already exists, which the archive would hide. Archives mounted at or below
// oldPath, or left mounted from a removed newPath, are unmounted and mounted
// again when next opened.
func (fsys *nestFS) Rename(oldPath, newPath string) error {
	if err := pathutil.ValidateRename(oldPath, newPath); err != nil {
		return err
	}
	if fsys.fsys == nil {
		return ufserrors.NewLinkError(renameOp, oldPath, newPath, fs.ErrClosed)
	}
	linkErr := func(err error) error {
		return ufserrors.NewLinkError(renameOp, oldPath, newPath, err)
	}

	src, err := fsys.mountLevels(oldPath)
	if err != nil {
		return linkErr(err)
	}
	dst, err := fsys.mountLevels(newPath)
	if err != nil {
		return linkErr(err)
	}
	srcLeaf, dstLeaf := src[len(src)-1], dst[len(dst)-1]
	if srcLeaf.fsys.fsys == nil || dstLeaf.fsys.fsys == nil {
		return linkErr(fs.ErrClosed)
	}
	if pathutil.IsCwd(srcLeaf.name) || pathutil.IsCwd(dstLeaf.name) {
		// oldPath or newPath is the root of a mount.
		return linkErr(fs.ErrPermission)
	}
	if srcLeaf.fsys.inArchive || dstLeaf.fsys.inArchive {
		return linkErr(fmt.Errorf("archive content cannot be changed, %w", fs.ErrPermission))
	}

	var unmount []mountLevel
	for _, level := range src {
		for _, mountPath := range level.fsys.mounts.mountsUnder(level.name) {
			if !isArchiveMountPath(mountPath) {
				return linkErr(fmt.Errorf("%q is mounted, %w", mountPath, fs.ErrPermission))
			}
			unmount = append(unmount, mountLevel{level.fsys, mountPath})
		}
		if level.fsys.mounts.getMount(level.name+archiveDirExt) != nil {
			unmount = append(unmount, mountLevel{level.fsys, level.name + archiveDirExt})
		}
	}
	for _, level := range dst {
		for _, mountPath := range level.fsys.mounts.mountsUnder(level.name) {
			if !isArchiveMountPath(mountPath) {
				return linkErr(fmt.Errorf("%q is mounted, %w", mountPath, fs.ErrExist))
			}
			// The rename fails if newPath exists, so on success an archive
			// mounted below it is left from a removed one.
			unmount = append(unmount, mountLevel{level.fsys, mountPath})
		}
		if level.fsys.mounts.getMount(level.name+archiveDirExt) != nil {
			unmount = append(unmount, mountLevel{level.fsys, level.name + archiveDirExt})
		}
	}
	if archive.IsMountablePath(dstLeaf.name) {
		contentDir := dstLeaf.name + archiveDirExt
		if _, err := dstLeaf.fsys.fsys.Lstat(contentDir); err == nil {
			return linkErr(fmt.Errorf("%q would be hidden by the archive, %w", contentDir, fs.ErrExist))
		} else if !errors.Is(err, fs.ErrNotExist) {
			return linkErr(err)
		}
	}

	if srcLeaf.fsys == dstLeaf.fsys {
		err = ops.Rename(srcLeaf.fsys.fsys, srcLeaf.name, dstLeaf.name)
	} else {
		err = ops.RenameByCopy(srcLeaf.fsys.fsys, srcLeaf.name, dstLeaf.fsys.fsys, dstLeaf.name)
	}
	if err != nil {
		return linkErr(err)
	}
	for _, mount := range unmount {
		// The rename itself succeeded, so a mount that cannot be closed here is
		// left for a later close rather than reported as a failed rename.
		if removeErr := mount.fsys.mounts.remove(mount.name); removeErr != nil {
			slog.Warn("cannot unmount after rename", "path", mount.name, "error", removeErr)
		}
	}
	return nil
}

// closeOnRelease reports whether releasing means closing fsys outright rather
// than keeping it and releasing only what it holds. It is true for a mount that
// ufs opened for itself that holds a file another application can use: closing
// it releases the handle it keeps on the file it was opened from, along with any
// local copy of that file. In practice such a mount is an archive.
//
// It is false for a mount the caller configured, which is the caller's to keep
// open, and for a mount that is not backed by a file at all: releasing it would
// let go of nothing, and closing it would throw its content away.
func (fsys *nestFS) closeOnRelease() bool {
	return fsys.autoMounted && fsys.fsys != nil && !holdsNoFile(fsys.fsys)
}

// holdsNoFile reports whether fsys is backed by memory or by nothing at all, so
// that it keeps no file handle and holds no local copy of a remote file. A file
// system that does not name its device is taken to be backed by a file.
func holdsNoFile(fsys types.WriteFS) bool {
	info, ok := fsys.DeviceInfo()["."]
	if !ok {
		info = types.DefaultDeviceInfo
	}
	switch info.DeviceType() {
	case "memory", "null":
		return true
	}
	return false
}

// Release lets go of the resources that the mounts of fsys hold on to without
// closing fsys itself, so that other applications can use them. A mount that ufs
// opened for itself is closed and unmounted, which releases the handle it keeps
// on the file it was opened from; in practice such a mount is the content
// directory of an archive inside the tree. A mount that the caller configured,
// by handing over a file system or by giving a URI for ufs to open, and a mount
// that is not backed by a file, keeps its own file system and releases the
// mounts below it, recursively.
//
// fsys stays usable, and the content directory of an archive inside it is
// mounted again from the file that holds it the next time a path under it is
// used. The base file system of fsys is not released; only [nestFS.Close] lets
// go of it.
//
// Every mount is attempted. Release joins the errors it meets, and a mount that
// could not be released stays mounted, so that a later release or close can try
// again.
func (fsys *nestFS) Release() error {
	return fsys.mounts.releaseAll()
}

// mountLevel is a nestFS that a path passes through and the path within it.
type mountLevel struct {
	fsys *nestFS
	name string
}

// mountLevels returns each nestFS that name passes through, from fsys to the
// one whose file system holds name. Unlike getFSAndSubpath it also descends
// into a nestFS mounted as a file system (as [FSBuilder.MountFS] allows), so
// the file system of the last level is not a nestFS and has no mounts or
// archive directories of its own.
func (fsys *nestFS) mountLevels(name string) ([]mountLevel, error) {
	levels := []mountLevel{{fsys, name}}
	for current := fsys; ; {
		next, subName, err := current.getFSAndSubpath(name)
		if err != nil {
			return nil, err
		}
		if next != current {
			levels = append(levels, mountLevel{next, subName})
		}
		inner, ok := next.fsys.(*nestFS)
		if !ok {
			return levels, nil
		}
		levels = append(levels, mountLevel{inner, subName})
		current, name = inner, subName
	}
}

// isArchiveMountPath reports whether the mount path mountPath is where nestFS
// mounts an archive, rather than a mount from the URI.
func isArchiveMountPath(mountPath string) bool {
	archiveName, ok := strings.CutSuffix(mountPath, archiveDirExt)
	return ok && archive.IsMountablePath(archiveName)
}

func (fsys *nestFS) Glob(pattern string) ([]string, error) {
	if cFsys, ok := fsys.fsys.(fs.GlobFS); ok {
		return cFsys.Glob(pattern)
	}
	return globutil.GlobFS(fsys, pattern)
}

func (fsys *nestFS) validPath(op string, name string) error {
	if err := pathutil.Validate(op, name); err != nil {
		return err
	}
	if fsys.fsys == nil {
		return ufserrors.NewPathError(op, name, fs.ErrClosed)
	}
	return nil
}

// NewNestFS returns a nested file system described by the URI name.
func NewNestFS(ctx context.Context, name string) (FS, error) {
	return newNestFS(ctx, name)
}

// WrapNestFS wraps fsys in a nested file system with no additional mounts.
func WrapNestFS(ctx context.Context, fsys types.WriteFS) FS {
	return makeNestFS(ctx, fsys)
}

func newNestFS(ctx context.Context, name string) (FS, error) {
	fsys, err := newBaseFS(ctx, name)
	if err != nil {
		return nil, err
	}
	return makeNestFS(ctx, fsys), nil
}

func makeNestFS(ctx context.Context, fsys types.WriteFS, args ...FSArgs) *nestFS {
	var a FSArgs
	if len(args) > 0 {
		a = args[0]
	}
	return &nestFS{
		fsys:   fsys,
		ctx:    ctx,
		mounts: makeMountMap(fsys.String()),
		args:   a,
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
