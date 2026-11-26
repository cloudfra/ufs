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

package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/common/buffile"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/notify"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const (
	memFSPrefix = "memory:"
)

var (
	_ types.File       = (*memFile)(nil)
	_ types.File       = (*memOpenFile)(nil)
	_ types.WriteFS    = (*memFS)(nil)
	_ types.OpenFileFS = (*memFS)(nil)
	_ fs.GlobFS        = (*memFS)(nil)
	_ fs.ReadDirFile   = (*memDirFile)(nil)
	_ types.Watcher    = (*memFS)(nil)
)

func init() {
	ufs.Register(ufs.NewDriver("memory", newMemFS, isMemFSUri, 1, true, true))
}

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
		return osutil.EmptyDirSize
	}
	return int64(len(n.content))
}

func (n *memNode) info() fs.FileInfo {
	return types.NewFileInfo(n.name, n.size(), n.mode, n.modTime)
}

// memFS is an in-memory file system. All nodes are stored in a flat map keyed
// by their clean path without trailing slashes (e.g. "." for root, "a/b" for a
// nested file or directory).
type memFS struct {
	mu    sync.RWMutex
	name  string
	nodes map[string]*memNode

	notifyBus *notify.Bus
}

// memFile is an open read-write handle for a regular file. Writes are
// immediately synced back to the filesystem node so that subsequent Open calls
// observe the latest content.
type memFile struct {
	buffile.File
	fsys *memFS

	// syncMu serializes a write with its sync to the node, so concurrent
	// writes on one handle reach the node in the order they were buffered.
	syncMu sync.Mutex
}

func (f *memFile) Write(p []byte) (int, error) {
	f.syncMu.Lock()
	n, err := f.File.Write(p)
	if err == nil {
		f.syncToFS()
	}
	f.syncMu.Unlock()
	if err != nil {
		return n, err
	}

	f.notifyWrite()
	return n, err
}

func (f *memFile) WriteString(s string) (int, error) {
	f.syncMu.Lock()
	n, err := f.File.WriteString(s)
	if err == nil {
		f.syncToFS()
	}
	f.syncMu.Unlock()
	if err != nil {
		return n, err
	}

	f.notifyWrite()
	return n, err
}

// syncToFS stamps modTime and, if the file is backed by a live memFS, pushes
// a snapshot of content to the corresponding node so that other open handles
// and future Opens observe the write. f.syncMu must be held.
func (f *memFile) syncToFS() {
	now := time.Now()
	f.MarkDirty()
	content, _, _ := f.TakeDirty(now)
	if f.fsys != nil {
		f.fsys.mu.Lock()
		if node, ok := f.fsys.nodes[f.Path()]; ok {
			node.content = content
			node.modTime = now
		}
		f.fsys.mu.Unlock()
	}
}

func (f *memFile) notifyWrite() {
	if f.fsys != nil {
		f.fsys.notify(types.NotifyWrite, f.Path())
	}
}

// Close marks the handle closed so that its later reads and writes are
// refused. A memFS handle pushes each write to its node as it lands, so Close
// has nothing left to persist.
func (f *memFile) Close() error {
	return f.File.Close()
}

// memOpenFile is the handle OpenFile gives back, so it knows the access mode
// the flags asked for and whether a write belongs at the end of the file.
//
// A file system that answers OpenFile itself enforces the mode on its own
// handle. The access wrapper in the polyfills package stands in only for the
// ones whose handle comes back from Open or Create without a mode attached.
type memOpenFile struct {
	*memFile
	reads   bool
	writes  bool
	appends bool
}

// checkRead refuses a read the access mode does not allow. Package os reports
// the bare EBADF here, which matches no io/fs sentinel; memFS reports
// [fs.ErrPermission] the way the polyfill wrapper does, which is a choice issue
// #548 still holds open. A closed handle is refused first, because a closed
// handle of any mode answers with [fs.ErrClosed] rather than claiming the mode
// was the reason.
func (f *memOpenFile) checkRead(op string) error {
	if f.Closed() {
		return ufserrors.NewPathError(op, f.Path(), fs.ErrClosed)
	}
	if !f.reads {
		return ufserrors.NewPathError(op, f.Path(), fs.ErrPermission)
	}
	return nil
}

// checkWrite refuses a write the access mode does not allow, and moves an
// appending handle to the end of its own content first. The error sentinel is
// the same choice checkRead documents, and a closed handle is refused ahead of
// the mode for the same reason.
func (f *memOpenFile) checkWrite() error {
	if f.Closed() {
		return ufserrors.NewPathError("write", f.Path(), fs.ErrClosed)
	}
	if !f.writes {
		return ufserrors.NewPathError("write", f.Path(), fs.ErrPermission)
	}
	if f.appends {
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			return err
		}
	}
	return nil
}

func (f *memOpenFile) Read(p []byte) (int, error) {
	if err := f.checkRead("read"); err != nil {
		return 0, err
	}
	return f.memFile.Read(p)
}

// ReadAt reports the read operation, the name package os gives a failed read
// through any method of an open file.
func (f *memOpenFile) ReadAt(p []byte, off int64) (int, error) {
	if err := f.checkRead("read"); err != nil {
		return 0, err
	}
	return f.memFile.ReadAt(p, off)
}

func (f *memOpenFile) Write(p []byte) (int, error) {
	if err := f.checkWrite(); err != nil {
		return 0, err
	}
	return f.memFile.Write(p)
}

func (f *memOpenFile) WriteString(s string) (int, error) {
	if err := f.checkWrite(); err != nil {
		return 0, err
	}
	return f.memFile.WriteString(s)
}

// Close marks the handle closed so its later reads and writes are refused, and
// reports [fs.ErrClosed] for a second Close the way a closed os.File does. The
// shared buffered handle keeps the closed state and refuses reads and writes on
// its own; only the second Close is refused here, because the handles from Open
// and Create keep Close idempotent. The content a write reached before Close
// stays in the file system.
func (f *memOpenFile) Close() error {
	if f.Closed() {
		return ufserrors.NewPathError("close", f.Path(), fs.ErrClosed)
	}
	return f.memFile.Close()
}

// memDirFile is an open directory handle. It snapshots the directory entries at
// Open time so that paginated ReadDir calls are stable even if the filesystem is
// modified concurrently.
//
// A caller is free to keep one handle and serve more than one reader from it, so
// where a reader has reached in the snapshot is per-handle state that mu guards.
// Two walkers over one handle then divide the snapshot between them: each entry
// is handed to exactly one of them, in order, and neither sees it twice. The
// other fields are written once at Open and only read after that, so they need
// no guard, and ReadDir holds mu over no work the handle did not already own —
// the snapshot is built by openDir before the handle exists.
type memDirFile struct {
	mu      sync.Mutex
	path    string
	entries []fs.DirEntry
	offset  int
	mode    fs.FileMode
	modTime time.Time
}

func (d *memDirFile) Stat() (fs.FileInfo, error) {
	return types.NewFileInfo(path.Base(d.path), osutil.EmptyDirSize, d.mode, d.modTime), nil
}

func (d *memDirFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.path, Err: fmt.Errorf("is a directory")}
}

func (d *memDirFile) Close() error {
	return nil
}

func (d *memDirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	d.mu.Lock()
	if n <= 0 {
		batch := d.entries[d.offset:]
		d.offset = len(d.entries)
		d.mu.Unlock()
		return batch, nil
	}
	if d.offset >= len(d.entries) {
		d.mu.Unlock()
		return nil, io.EOF
	}
	end := d.offset + n
	if end > len(d.entries) {
		end = len(d.entries)
	}
	batch := d.entries[d.offset:end]
	d.offset = end
	d.mu.Unlock()
	return batch, nil
}

func (fsys *memFS) isClosed() bool {
	fsys.mu.RLock()
	closed := fsys.nodes == nil
	fsys.mu.RUnlock()
	return closed
}

func (fsys *memFS) Open(name string) (fs.File, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("open", name, fs.ErrClosed)
	}
	if name == pathutil.CwdPath {
		return fsys.openDir(pathutil.CwdPath)
	}
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}

	// A rewrite of the name resets content and modTime on the node in place, so
	// the node found here is only readable while the lock is still held: the
	// handle is built with a copy taken inside the lock rather than with the
	// node's fields read after it is given back.
	fsys.mu.RLock()
	node, ok := fsys.nodes[name]
	if !ok {
		fsys.mu.RUnlock()
		return nil, ufserrors.NewPathError("open", name, fs.ErrNotExist)
	}
	if node.isDir {
		fsys.mu.RUnlock()
		return fsys.openDir(name)
	}
	file := &memFile{
		fsys: fsys,
		File: buffile.New(name, bytes.Clone(node.content), node.mode, node.modTime),
	}
	fsys.mu.RUnlock()
	return file, nil
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
	if dir == pathutil.CwdPath {
		prefix = ""
	}

	var entries []fs.DirEntry
	seen := make(map[string]struct{})
	for key, node := range fsys.nodes {
		if key == pathutil.CwdPath {
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
	err := fsys.notifyBus.Close()

	fsys.mu.Lock()
	fsys.nodes = nil
	fsys.mu.Unlock()
	return err
}

// checkDirsLocked verifies that name and each of its ancestors, where they
// already exist, are directories, so that a later mutation cannot place a
// child under a regular file. name must be a valid, non-root path.
// fsys.mu must be held.
func (fsys *memFS) checkDirsLocked(name string) error {
	for i := 0; ; {
		j := strings.IndexByte(name[i:], '/')
		p := name
		if j >= 0 {
			p = name[:i+j]
		}
		if node, ok := fsys.nodes[p]; ok && !node.isDir {
			return fmt.Errorf("%q is not a directory: %w", p, fs.ErrExist)
		}
		if j < 0 {
			return nil
		}
		i += j + 1
	}
}

// ensureParentsLocked creates any missing ancestor directories for the given
// path. Must be called with fsys.mu held for writing.
func (fsys *memFS) ensureParentsLocked(name string, now time.Time) {
	dir := path.Dir(name)
	if dir == pathutil.CwdPath {
		return
	}
	parts := pathutil.Split(dir)
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

func newMemFS(_ context.Context, name string) (types.WriteFS, error) {
	return MakeMemFS(name), nil
}

// MakeMemFS creates a memory FS without a context. (Deprecated; use NewDriver with newMemFS instead.)
func MakeMemFS(name string) types.WriteFS {
	return makeMemFS(name)
}

func makeMemFS(name string) *memFS {
	now := time.Now()
	return &memFS{
		name:      name,
		notifyBus: notify.New(),
		nodes: map[string]*memNode{
			pathutil.CwdPath: {
				name:    pathutil.CwdPath,
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
