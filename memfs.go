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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	memFSPrefix = "memory:"
)

var (
	_ File           = (*memFile)(nil)
	_ FS             = (*memFS)(nil)
	_ Remover        = (*memFS)(nil)
	_ fs.GlobFS      = (*memFS)(nil)
	_ fs.ReadDirFile = (*memDirFile)(nil)
)

var errDirNotEmpty = errors.New("directory not empty")

// memNode holds the stored state for one file or directory.
type memNode struct {
	name    string // base name (path.Base of the full path key)
	content []byte
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

func (n *memNode) size() int64 {
	if n.isDir {
		return emptyDirSize
	}
	return int64(len(n.content))
}

func (n *memNode) info() fs.FileInfo {
	return &fsInfo{
		name:    n.name,
		size:    n.size(),
		mode:    n.mode,
		modTime: n.modTime,
		isDir:   n.isDir,
	}
}

// memFS is an in-memory file system. All nodes are stored in a flat map keyed
// by their clean path without trailing slashes (e.g. "." for root, "a/b" for a
// nested file or directory).
type memFS struct {
	mu    sync.RWMutex
	name  string
	nodes map[string]*memNode

	watchersMu sync.RWMutex
	watchers   []*memWatcher
}

// memFile is an open read-write handle for a regular file. Writes are
// immediately synced back to the filesystem node so that subsequent Open calls
// observe the latest content.
type memFile struct {
	mu      sync.Mutex
	fsys    *memFS
	path    string
	content []byte
	offset  int64
	mode    fs.FileMode
	modTime time.Time
}

func (f *memFile) Stat() (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &fsInfo{
		name:    path.Base(f.path),
		size:    int64(len(f.content)),
		mode:    f.mode,
		modTime: f.modTime,
		isDir:   false,
	}, nil
}

func (f *memFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offset >= int64(len(f.content)) {
		return 0, io.EOF
	}
	n := copy(p, f.content[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *memFile) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if off >= int64(len(f.content)) {
		return 0, io.EOF
	}
	n := copy(p, f.content[off:])
	if off+int64(n) >= int64(len(f.content)) {
		return n, io.EOF
	}
	return n, nil
}

func (f *memFile) Write(p []byte) (int, error) {
	return f.WriteString(string(p))
}

func (f *memFile) WriteString(s string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.content = append(f.content, s...)
	now := time.Now()
	f.modTime = now

	if f.fsys != nil {
		f.fsys.mu.Lock()
		if node, ok := f.fsys.nodes[f.path]; ok {
			node.content = bytes.Clone(f.content)
			node.modTime = now
		}
		f.fsys.mu.Unlock()
		f.fsys.notify(NotifyWrite, f.path)
	}

	return len(s), nil
}

func (f *memFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = f.offset + offset
	case io.SeekEnd:
		newOffset = int64(len(f.content)) + offset
	default:
		return 0, pathError("seek", f.path, fmt.Errorf("offset=%d whence=%d: invalid whence: %w", offset, whence, fs.ErrInvalid))
	}
	if newOffset < 0 {
		return 0, pathError("seek", f.path, fmt.Errorf("offset=%d whence=%d: position %d is before start of file: %w", offset, whence, newOffset, fs.ErrInvalid))
	}
	f.offset = newOffset
	return f.offset, nil
}

func (f *memFile) Close() error {
	return nil
}

// memDirFile is an open directory handle. It snapshots the directory entries at
// Open time so that paginated ReadDir calls are stable even if the filesystem is
// modified concurrently.
type memDirFile struct {
	path    string
	entries []fs.DirEntry
	offset  int
	mode    fs.FileMode
	modTime time.Time
}

func (d *memDirFile) Stat() (fs.FileInfo, error) {
	return &fsInfo{
		name:    path.Base(d.path),
		size:    emptyDirSize,
		mode:    d.mode,
		modTime: d.modTime,
		isDir:   true,
	}, nil
}

func (d *memDirFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.path, Err: fmt.Errorf("is a directory")}
}

func (d *memDirFile) Close() error {
	return nil
}

func (d *memDirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if n <= 0 {
		batch := d.entries[d.offset:]
		d.offset = len(d.entries)
		return batch, nil
	}
	if d.offset >= len(d.entries) {
		return nil, io.EOF
	}
	end := d.offset + n
	if end > len(d.entries) {
		end = len(d.entries)
	}
	batch := d.entries[d.offset:end]
	d.offset = end
	return batch, nil
}

func (fsys *memFS) getDeviceInfo() map[string]deviceInfo {
	return newDeviceInfoMap(deviceInfo{
		name:        fsys.name,
		deviceType:  "memory",
		threadCount: 2,
	})
}

func (fsys *memFS) URI() *url.URL {
	u, _ := url.Parse(fsys.name)
	return u
}

func (fsys *memFS) String() string {
	return fmt.Sprintf("memFS(%s)", fsys.URI())
}

func (fsys *memFS) isClosed() bool {
	fsys.mu.RLock()
	closed := fsys.nodes == nil
	fsys.mu.RUnlock()
	return closed
}

func (fsys *memFS) Open(name string) (fs.File, error) {
	if fsys.isClosed() {
		return nil, pathError("open", name, fs.ErrClosed)
	}
	if name == cwdPath {
		return fsys.openDir(cwdPath)
	}
	if err := validPath("open", name); err != nil {
		return nil, err
	}

	fsys.mu.RLock()
	node, ok := fsys.nodes[name]
	fsys.mu.RUnlock()

	if !ok {
		return nil, pathError("open", name, fs.ErrNotExist)
	}
	if node.isDir {
		return fsys.openDir(name)
	}
	return &memFile{
		fsys:    fsys,
		path:    name,
		content: bytes.Clone(node.content),
		mode:    node.mode,
		modTime: node.modTime,
	}, nil
}

func (fsys *memFS) openDir(name string) (*memDirFile, error) {
	entries, err := fsys.listDir(name)
	if err != nil {
		return nil, err
	}
	mode := fs.ModeDir | fs.ModePerm
	var modTime time.Time
	fsys.mu.RLock()
	if node, ok := fsys.nodes[name]; ok {
		mode = node.mode
		modTime = node.modTime
	}
	fsys.mu.RUnlock()
	return &memDirFile{
		path:    name,
		entries: entries,
		mode:    mode,
		modTime: modTime,
	}, nil
}

// listDir returns a sorted snapshot of the immediate children of dir.
func (fsys *memFS) listDir(dir string) ([]fs.DirEntry, error) {
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

	prefix := dir + "/"
	if dir == cwdPath {
		prefix = ""
	}

	var entries []fs.DirEntry
	seen := make(map[string]struct{})
	for key, node := range fsys.nodes {
		if key == cwdPath {
			continue
		}
		rest, ok := strings.CutPrefix(key, prefix)
		if !ok || strings.Contains(rest, "/") {
			continue
		}
		if _, exists := seen[rest]; exists {
			continue
		}
		seen[rest] = struct{}{}
		entries = append(entries, fs.FileInfoToDirEntry(node.info()))
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries, nil
}

func (fsys *memFS) Close() error {
	fsys.watchersMu.Lock()
	for _, mw := range fsys.watchers {
		mw.cancel()
	}
	fsys.watchers = nil
	fsys.watchersMu.Unlock()

	fsys.mu.Lock()
	fsys.nodes = nil
	fsys.mu.Unlock()
	return nil
}

func (fsys *memFS) Create(name string) (File, error) {
	if fsys.isClosed() {
		return nil, pathError("create", name, fs.ErrClosed)
	}
	if err := validPath("create", name); err != nil {
		return nil, err
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()

	now := time.Now()
	_, existed := fsys.nodes[name]
	node := &memNode{
		name:    path.Base(name),
		mode:    fs.ModePerm,
		modTime: now,
	}
	fsys.nodes[name] = node
	fsys.ensureParentsLocked(name, now)

	if existed {
		fsys.notify(NotifyWrite, name)
	} else {
		fsys.notify(NotifyCreate, name)
	}

	return &memFile{
		fsys:    fsys,
		path:    name,
		mode:    node.mode,
		modTime: node.modTime,
	}, nil
}

func (fsys *memFS) MkdirAll(name string, perm fs.FileMode) error {
	if fsys.isClosed() {
		return pathError("mkdir", name, fs.ErrClosed)
	}
	if err := validPath("mkdir", name); err != nil {
		return err
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()

	now := time.Now()
	parts := splitPath(name)
	accum := ""
	var created []string
	for i, part := range parts {
		if part == "" {
			continue
		}
		if i > 0 {
			accum += "/"
		}
		accum += part
		if _, ok := fsys.nodes[accum]; !ok {
			fsys.nodes[accum] = &memNode{
				name:    part,
				mode:    fs.ModeDir | perm,
				modTime: now,
				isDir:   true,
			}
			created = append(created, accum)
		}
	}
	for _, p := range created {
		fsys.notify(NotifyCreate, p)
	}
	return nil
}

// ensureParentsLocked creates any missing ancestor directories for the given
// path. Must be called with fsys.mu held for writing.
func (fsys *memFS) ensureParentsLocked(name string, now time.Time) {
	dir := path.Dir(name)
	if dir == cwdPath {
		return
	}
	parts := splitPath(dir)
	accum := ""
	for i, part := range parts {
		if part == "" {
			continue
		}
		if i > 0 {
			accum += "/"
		}
		accum += part
		if _, ok := fsys.nodes[accum]; !ok {
			fsys.nodes[accum] = &memNode{
				name:    part,
				mode:    fs.ModeDir | fs.ModePerm,
				modTime: now,
				isDir:   true,
			}
		}
	}
}

func (fsys *memFS) ReadFile(name string) ([]byte, error) {
	if fsys.isClosed() {
		return nil, pathError("readfile", name, fs.ErrClosed)
	}
	if err := validPath("readfile", name); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()
	node, ok := fsys.nodes[name]
	if !ok {
		return nil, pathError("readfile", name, fs.ErrNotExist)
	}
	return bytes.Clone(node.content), nil
}

func (fsys *memFS) ReadLink(name string) (string, error) {
	if fsys.isClosed() {
		return "", pathError("readlink", name, fs.ErrClosed)
	}
	if err := validPath("readlink", name); err != nil {
		return "", err
	}
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()
	if _, ok := fsys.nodes[name]; !ok {
		return "", pathError("readlink", name, fs.ErrNotExist)
	}
	// memFS has no symlinks; every extant path is a regular file or directory.
	return "", pathError("readlink", name, fs.ErrInvalid)
}

func (fsys *memFS) Stat(name string) (fs.FileInfo, error) {
	if fsys.isClosed() {
		return nil, pathError("stat", name, fs.ErrClosed)
	}
	if name == cwdPath {
		fsys.mu.RLock()
		node := fsys.nodes[cwdPath]
		fsys.mu.RUnlock()
		return node.info(), nil
	}
	if err := validPath("stat", name); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()
	node, ok := fsys.nodes[name]
	if !ok {
		return nil, pathError("stat", name, fs.ErrNotExist)
	}
	return node.info(), nil
}

func (fsys *memFS) Lstat(name string) (fs.FileInfo, error) {
	if fsys.isClosed() {
		return nil, pathError("lstat", name, fs.ErrClosed)
	}
	if name == cwdPath {
		fsys.mu.RLock()
		node := fsys.nodes[cwdPath]
		fsys.mu.RUnlock()
		return node.info(), nil
	}
	if err := validPath("lstat", name); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()
	node, ok := fsys.nodes[name]
	if !ok {
		return nil, pathError("lstat", name, fs.ErrNotExist)
	}
	return node.info(), nil
}

func (fsys *memFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if fsys.isClosed() {
		return nil, pathError("readdir", name, fs.ErrClosed)
	}
	if name == cwdPath {
		return fsys.listDir(cwdPath)
	}
	if err := validPath("readdir", name); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	node, ok := fsys.nodes[name]
	fsys.mu.RUnlock()
	if !ok {
		return nil, pathError("readdir", name, fs.ErrNotExist)
	}
	if !node.isDir {
		return nil, pathError("readdir", name, fs.ErrInvalid)
	}
	return fsys.listDir(name)
}

func (fsys *memFS) Glob(pattern string) ([]string, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

	var matches []string
	for key := range fsys.nodes {
		if key == cwdPath {
			continue
		}
		matched, err := path.Match(pattern, key)
		if err != nil {
			return nil, err
		}
		if matched {
			matches = append(matches, key)
		}
	}
	sort.Strings(matches)
	return matches, nil
}

func (fsys *memFS) Remove(name string) error {
	if fsys.isClosed() {
		return pathError("remove", name, fs.ErrClosed)
	}
	if err := validPath("remove", name); err != nil {
		return err
	}
	if name == cwdPath {
		return pathError("remove", name, fs.ErrPermission)
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	node, ok := fsys.nodes[name]
	if !ok {
		return pathError("remove", name, fs.ErrNotExist)
	}
	if node.isDir {
		prefix := name + "/"
		for key := range fsys.nodes {
			if strings.HasPrefix(key, prefix) {
				return pathError("remove", name, errDirNotEmpty)
			}
		}
	}
	delete(fsys.nodes, name)
	fsys.notify(NotifyRemove, name)
	return nil
}

func (fsys *memFS) RemoveAll(name string) error {
	if fsys.isClosed() {
		return pathError("removeall", name, fs.ErrClosed)
	}
	if name == cwdPath {
		fsys.mu.Lock()
		var removed []string
		for key := range fsys.nodes {
			if key != cwdPath {
				removed = append(removed, key)
				delete(fsys.nodes, key)
			}
		}
		fsys.mu.Unlock()
		for _, p := range removed {
			fsys.notify(NotifyRemove, p)
		}
		return nil
	}
	if err := validPath("removeall", name); err != nil {
		return err
	}
	fsys.mu.Lock()
	var removed []string
	prefix := name + "/"
	for key := range fsys.nodes {
		if key == name || strings.HasPrefix(key, prefix) {
			removed = append(removed, key)
			delete(fsys.nodes, key)
		}
	}
	fsys.mu.Unlock()
	for _, p := range removed {
		fsys.notify(NotifyRemove, p)
	}
	return nil
}

func newMemFS(name string) (FS, error) {
	return makeMemFS(name), nil
}

func makeMemFS(name string) *memFS {
	now := time.Now()
	return &memFS{
		name: name,
		nodes: map[string]*memNode{
			cwdPath: {
				name:    cwdPath,
				mode:    fs.ModeDir | fs.ModePerm,
				modTime: now,
				isDir:   true,
			},
		},
	}
}

func isMemFSUri(name string) bool {
	return strings.HasPrefix(name, memFSPrefix)
}
