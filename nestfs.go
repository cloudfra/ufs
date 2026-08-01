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
	"net/url"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
)

type bufferMode int

const (
	bufferMemory bufferMode = iota
	bufferDisk
)

// FSArgs holds optional parameters for constructing a nestFS.
type FSArgs struct {
	BufMode bufferMode
}

var (
	_ FS             = (*nestFS)(nil)
	_ fs.GlobFS      = (*nestFS)(nil)
	_ realAbsPathGet = (*nestFS)(nil)
	_ deviceInfoGet  = (*mountMap)(nil)
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

func (m *mountMap) getDeviceInfo() map[string]deviceInfo {
	combined := map[string]deviceInfo{}
	m.mu.RLock()
	for mountPoint, fsys := range m.m {
		combined = combineDeviceInfo(combined, mountPoint, fsys.getDeviceInfo())
	}
	m.mu.RUnlock()
	return combined
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
	// If there's a mount that should handle the directory listing then we should defer to that.
	if _, ok := m.m[name]; ok {
		m.mu.RUnlock()
		return []string{}
	}
	dirSet := map[string]any{}
	for mountPath := range m.m {
		subPath, ok := removePathPrefix(mountPath, name)
		if ok {
			dirSet[splitPath(subPath)[0]] = nil
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
		subPath, ok := removePathPrefix(mountPath, name)
		if ok {
			matches[subPath] = subFS
		}
	}
	m.mu.RUnlock()

	return matches
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
	if isCwd(name) {
		return "", "", nil, false
	}
	targetMount := ""
	targetSubPath := ""
	var targetFS *nestFS
	m.mu.RLock()
	for mountPath, subFS := range m.m {
		subPath, ok := removePathPrefix(name, mountPath)
		if ok && (len(targetMount) < len(mountPath) || targetMount == "") {
			targetMount = mountPath
			targetSubPath = subPath
			targetFS = subFS
		}
	}
	m.mu.RUnlock()

	return targetMount, targetSubPath, targetFS, targetFS != nil
}

func (m *mountMap) remove(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if nfs, ok := m.m[name]; ok {
		if err := nfs.Close(); err != nil {
			fmt.Printf("failed to close mount %q, %v\n", name, err)
		}
		delete(m.m, name)
	}
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
	return joinErrors(errs...)
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
	ctx    context.Context
	mounts *mountMap
	args   FSArgs
}

func (fsys *nestFS) getAbsPath(name string) (string, error) {
	if rfs, ok := fsys.fsys.(*localFS); ok {
		return rfs.getAbsPath(name)
	}
	return "", realAbsPathNotSupported(fsys, name)
}

func (fsys *nestFS) getDeviceInfo() map[string]deviceInfo {
	base := fsys.fsys.getDeviceInfo()
	return combineDeviceInfo(base, "", fsys.mounts.getDeviceInfo())
}

func (fsys *nestFS) URI() *url.URL {
	base := fsys.fsys.URI()
	if base == nil {
		return nil
	}
	u := *base
	vals := u.Query()
	fsys.mounts.mu.RLock()
	defer fsys.mounts.mu.RUnlock()
	for p, mfs := range fsys.mounts.m {
		if strings.HasSuffix(p, archiveDirExt) {
			continue
		}
		if mu := mfs.URI(); mu != nil {
			vals.Set(p, mu.String())
		}
	}
	u.RawQuery = vals.Encode()
	return &u
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
	appendEntry := map[string]fs.DirEntry{}

	dirs := fsys.mounts.getDirectoryList(name)
	for _, dir := range dirs {
		if strings.HasSuffix(dir, archiveDirExt) && isMountableArchivePath(strings.TrimSuffix(dir, archiveDirExt)) {
			archivePath := path.Join(name, strings.TrimSuffix(dir, archiveDirExt))
			if _, statErr := fs.Stat(fsys.fsys, archivePath); errors.Is(statErr, fs.ErrNotExist) {
				fsys.mounts.remove(path.Join(name, dir))
				continue
			}
		}
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

// isMountedArchiveDir reports whether name (a full path within this FS) is a
// virtual directory backed by a mounted archive. It returns true only when:
//   - name ends with archiveDirExt
//   - the trimmed name satisfies isMountableArchivePath
//   - the archive file is not confirmed absent; any Stat error other than
//     ErrNotExist is treated as "file likely exists" so that a permission-denied
//     error does not cause Walk to descend and trigger a mount failure
//
// Note: if a real directory named "foo.zip.d" coexists with "foo.zip" in the
// same FS, nestFS's path routing always redirects access through the archive
// (see getFSAndSubpath). The real directory is unreachable via this FS
// regardless of what this method returns; that is a nestFS limitation.
func (fsys *nestFS) isMountedArchiveDir(name string) bool {
	if !strings.HasSuffix(name, archiveDirExt) {
		return false
	}
	archiveName := strings.TrimSuffix(name, archiveDirExt)
	if !isMountableArchivePath(archiveName) {
		return false
	}
	if fsys.mounts.getMount(name) != nil {
		return true
	}
	_, err := fsys.Stat(archiveName)
	return !errors.Is(err, fs.ErrNotExist)
}

func (fsys *nestFS) mountArchive(name string) (*nestFS, error) {
	if maybeFS := fsys.mounts.getMount(name + archiveDirExt); maybeFS != nil {
		return maybeFS, nil
	}
	ctx := fsys.ctx
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
		newFS, err = newArchiveFSFromFile(ctx, f)
		if err != nil {
			return nil, joinErrors(pathError("mount", name, err), f.Close())
		}
	}

	wrapped := makeNestFS(ctx, newFS)
	if err := fsys.addMount(name+archiveDirExt, wrapped); err != nil {
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

	return wrapFile(f, true, mountFS.args.BufMode)
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
	return joinErrors(mountErr, baseErr)
}

func (fsys *nestFS) Create(name string) (File, error) {
	if err := fsys.validPath("create", name); err != nil {
		return nil, err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	f, err := mountFS.fsys.Create(subName)
	if err != nil {
		return nil, err
	}
	return wrapFile(f, false, mountFS.args.BufMode)
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

func (fsys *nestFS) Remove(name string) error {
	if err := fsys.validPath("remove", name); err != nil {
		return err
	}
	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return err
	}
	return mountFS.fsys.Remove(subName)
}

func (fsys *nestFS) RemoveAll(name string) error {
	if err := fsys.validPath("removeall", name); err != nil {
		return err
	}
	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return err
	}
	return mountFS.fsys.RemoveAll(subName)
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

func newNestFS(ctx context.Context, name string) (FS, error) {
	fsys, err := newBaseFS(ctx, name)
	if err != nil {
		return nil, err
	}
	return makeNestFS(ctx, fsys), nil
}

func makeNestFS(ctx context.Context, fsys FS, args ...FSArgs) *nestFS {
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

var _ File = (*nestFile)(nil)

// nestFile wraps an fs.File and polyfills any methods from the File interface
// that the underlying implementation does not provide.
//
// When Seek or ReadAt is absent, the file content is buffered so that both
// operations work correctly at any position. In memory mode, content is read
// eagerly into a bytes.Reader. In disk mode, content is streamed to a temporary
// file. The buffer is released on Close. Read is also redirected through the
// buffer so that the file position stays consistent across Read/Seek/ReadAt.
type nestFile struct {
	fs.File
	buf             *bytes.Reader // non-nil when content is buffered in memory
	tmpFile         *os.File      // non-nil when content is buffered on disk
	writeFunc       func([]byte) (int, error)
	seekFunc        func(int64, int) (int64, error)
	readAtFunc      func([]byte, int64) (int, error)
	writeStringFunc func(string) (int, error)
}

func (f *nestFile) Read(p []byte) (int, error) {
	if f.buf != nil {
		return f.buf.Read(p)
	}
	if f.tmpFile != nil {
		return f.tmpFile.Read(p)
	}
	return f.File.Read(p)
}

func (f *nestFile) Close() error {
	f.buf = nil
	if f.tmpFile != nil {
		name := f.tmpFile.Name()
		if err := f.tmpFile.Close(); err != nil {
			return err
		}
		if err := os.Remove(name); err != nil {
			return err
		}
		f.tmpFile = nil
	}
	return f.File.Close()
}

func (f *nestFile) Write(p []byte) (int, error) {
	return f.writeFunc(p)
}

func (f *nestFile) Seek(off int64, whence int) (int64, error) {
	return f.seekFunc(off, whence)
}

func (f *nestFile) ReadAt(p []byte, off int64) (int, error) {
	return f.readAtFunc(p, off)
}

func (f *nestFile) WriteString(s string) (int, error) {
	return f.writeStringFunc(s)
}

// polyfillSeekReadAt populates nf.seekFunc and nf.readAtFunc for the underlying
// file f. If f already provides both io.Seeker and io.ReaderAt the native
// implementations are used directly. Otherwise the file content is buffered so
// that Seek and ReadAt work at any position. mode selects in-memory (bufferMemory)
// or temp-file (bufferDisk) buffering. On failure f is closed and the error
// returned.
func polyfillSeekReadAt(nf *nestFile, f fs.File, mode bufferMode) error {
	_, hasSeek := f.(io.Seeker)
	_, hasReadAt := f.(io.ReaderAt)
	if hasSeek && hasReadAt {
		nf.seekFunc = f.(io.Seeker).Seek
		nf.readAtFunc = f.(io.ReaderAt).ReadAt
		return nil
	}
	if mode == bufferDisk {
		return polyfillSeekReadAtDisk(nf, f)
	}
	return polyfillSeekReadAtMemory(nf, f)
}

func polyfillSeekReadAtMemory(nf *nestFile, f fs.File) error {
	data, err := io.ReadAll(f)
	if err != nil {
		f.Close()
		return err
	}
	nf.buf = bytes.NewReader(data)
	nf.seekFunc = nf.buf.Seek
	nf.readAtFunc = nf.buf.ReadAt
	return nil
}

func polyfillSeekReadAtDisk(nf *nestFile, f fs.File) error {
	tmp, err := os.CreateTemp("", "ufs-polyfill-*.tmp")
	if err != nil {
		fCloseErr := f.Close()
		return joinErrors(err, fCloseErr)
	}
	if _, err := io.Copy(tmp, f); err != nil {
		closeErr := tmp.Close()
		removeErr := os.Remove(tmp.Name())
		fCloseErr := f.Close()
		return joinErrors(err, closeErr, removeErr, fCloseErr)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		closeErr := tmp.Close()
		removeErr := os.Remove(tmp.Name())
		fCloseErr := f.Close()
		return joinErrors(err, closeErr, removeErr, fCloseErr)
	}
	nf.tmpFile = tmp
	nf.seekFunc = tmp.Seek
	nf.readAtFunc = tmp.ReadAt
	return nil
}

// wrapFile returns f unchanged if it already satisfies File. Otherwise it wraps
// f, polyfilling any missing methods. When readOnly is true, Write and
// WriteString always return fs.ErrInvalid. mode selects in-memory or disk-backed
// buffering for Seek/ReadAt polyfills.
func wrapFile(f fs.File, readOnly bool, mode bufferMode) (File, error) {
	if full, ok := f.(File); ok {
		return full, nil
	}
	nf := &nestFile{File: f}
	if err := polyfillSeekReadAt(nf, f, mode); err != nil {
		return nil, err
	}
	if readOnly {
		nf.writeFunc = func(_ []byte) (int, error) {
			return 0, fs.ErrInvalid
		}
		nf.writeStringFunc = func(_ string) (int, error) {
			return 0, fs.ErrInvalid
		}
		return nf, nil
	}
	if w, ok := f.(io.Writer); ok {
		nf.writeFunc = w.Write
	} else {
		nf.writeFunc = func(_ []byte) (int, error) {
			return 0, fs.ErrInvalid
		}
	}
	if sw, ok := f.(io.StringWriter); ok {
		nf.writeStringFunc = sw.WriteString
	} else if _, ok := f.(io.Writer); ok {
		nf.writeStringFunc = func(s string) (int, error) {
			return nf.writeFunc([]byte(s))
		}
	} else {
		nf.writeStringFunc = func(_ string) (int, error) {
			return 0, fs.ErrInvalid
		}
	}
	return nf, nil
}

// wrapReadOnlyFSFile returns f unchanged if it already satisfies File.
// Otherwise it wraps f for read-only use with in-memory buffering.
func wrapReadOnlyFSFile(f fs.File) (File, error) {
	return wrapFile(f, true, bufferMemory)
}

// wrapFSFile returns f unchanged if it already satisfies File. Otherwise it
// wraps f for read-write use with in-memory buffering.
func wrapFSFile(f fs.File) (File, error) {
	return wrapFile(f, false, bufferMemory)
}
