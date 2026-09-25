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

// go.etcd.io/bbolt has no support for GOARCH=wasm (it has no MaxAllocSize
// constant for that architecture, and its mmap-based storage model has no
// wasm implementation regardless); see boltfs_wasm.go for the stub used on
// that platform.
//go:build !wasm

package boltfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/common/buffile"
	"github.com/cloudfra/ufs/internal/notifybus"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var (
	_ ufs.File       = (*boltFile)(nil)
	_ ufs.WriteFS    = (*boltFS)(nil)
	_ fs.GlobFS      = (*boltFS)(nil)
	_ fs.ReadDirFile = (*boltDirFile)(nil)

	// selfKey serves two roles that happen to share the same value: it is the
	// name of the top-level bolt bucket that represents the root ("." /
	// pathutil.CwdPath) directory of the file system, and it is the reserved
	// key, stored inside every directory bucket (including the root), that
	// holds that directory's own mode/modTime record. It can never collide
	// with a real file or directory name because fs.ValidPath forbids path
	// elements named "." or "..".
	selfKey = []byte(pathutil.CwdPath)

	errIsDirectory = errors.New("is a directory")
)

func init() {
	ufs.Register(ufs.Driver{
		Name:       "bolt",
		CreateFunc: newBoltFS,
		MatchFunc:  isBoltFSUri,
		Priority:   1,
		Standard:   true,
		ReadWrite:  true,
	})
}

// boltFS is a file system backed by a single BoltDB (bbolt) file. Directories
// are represented as nested buckets and files are stored as a key/value pair
// within their parent directory's bucket. Each directory bucket carries its
// own metadata (mode, modTime) under the reserved selfKey.
type boltFS struct {
	mu      sync.RWMutex
	name    string
	absPath string
	db      *bolt.DB

	notifyBus *notifybus.Bus
}

// boltFile is an open read-write handle for a regular file. Writes are
// buffered in memory (via the embedded buffile.File) and only persisted back
// to the bolt database when the file is closed, so that a sequence of
// Write/WriteString calls costs a single bolt transaction instead of one per
// call.
type boltFile struct {
	buffile.File
	fsys *boltFS
}

// Close persists any buffered writes to the bolt database, if the file was
// modified since it was opened, and fires a NotifyWrite event. It is safe to
// call multiple times; if persisting fails the content stays dirty so a later
// Close retries it.
func (f *boltFile) Close() error {
	if f.fsys == nil {
		return nil
	}
	modTime := time.Now()
	content, mode, ok := f.TakeDirty(modTime)
	if !ok {
		return nil
	}
	name := f.Path()
	if err := f.fsys.writeFileContent(name, mode, modTime, content); err != nil {
		f.MarkDirty()
		return ufserrors.NewPathError("close", name, err)
	}
	f.fsys.notify(ufs.NotifyWrite, name)
	return nil
}

// writeFileContent persists content (with the given mode/modTime) to the
// record stored at name, creating any missing ancestor directories.
func (fsys *boltFS) writeFileContent(name string, mode fs.FileMode, modTime time.Time, content []byte) error {
	record, err := encodeBoltRecord(mode, modTime, content)
	if err != nil {
		return err
	}
	return fsys.update(func(tx *bolt.Tx) error {
		bkt, key, err := parentBucket(tx, name)
		if err != nil {
			return err
		}
		if bkt.Bucket(key) != nil {
			return fmt.Errorf("%q %w: %w", name, errIsDirectory, fs.ErrInvalid)
		}
		return bkt.Put(key, record)
	})
}

// boltDirFile is an open directory handle. It snapshots the directory entries
// at Open time so that paginated ReadDir calls are stable even if the
// filesystem is modified concurrently.
type boltDirFile struct {
	path    string
	entries []fs.DirEntry
	offset  int
	mode    fs.FileMode
	modTime time.Time
}

func (d *boltDirFile) Stat() (fs.FileInfo, error) {
	return ufs.NewFileInfo(path.Base(d.path), osutil.EmptyDirSize, d.mode, d.modTime), nil
}

func (d *boltDirFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.path, Err: errIsDirectory}
}

func (d *boltDirFile) Close() error {
	return nil
}

func (d *boltDirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if n <= 0 {
		batch := d.entries[d.offset:]
		d.offset = len(d.entries)
		return batch, nil
	}
	if d.offset >= len(d.entries) {
		return nil, io.EOF
	}
	end := min(d.offset+n, len(d.entries))
	batch := d.entries[d.offset:end]
	d.offset = end
	return batch, nil
}

// rootBucket returns the top-level bucket representing the file system root.
// makeBoltFS creates it (with its selfKey record) when the database is
// opened, so a missing root bucket means the database is not a boltFS.
func rootBucket(tx *bolt.Tx) (*bolt.Bucket, error) {
	bkt := tx.Bucket(selfKey)
	if bkt == nil {
		return nil, fmt.Errorf("missing root bucket: %w", fs.ErrNotExist)
	}
	return bkt, nil
}

// createDirBucket returns the child directory bucket key of parent, creating
// it with a selfKey record of mode/modTime if it does not exist. created
// reports whether the bucket was newly created. It must only be called on a
// writable transaction.
func createDirBucket(parent *bolt.Bucket, key []byte, mode fs.FileMode, modTime time.Time) (bkt *bolt.Bucket, created bool, err error) {
	if bkt = parent.Bucket(key); bkt != nil {
		return bkt, false, nil
	}
	if parent.Get(key) != nil {
		return nil, false, fmt.Errorf("%q is not a directory: %w", key, fs.ErrExist)
	}
	bkt, err = parent.CreateBucket(key)
	if err != nil {
		return nil, false, err
	}
	record, err := encodeBoltRecord(mode, modTime, nil)
	if err != nil {
		return nil, false, err
	}
	if err := bkt.Put(selfKey, record); err != nil {
		return nil, false, err
	}
	return bkt, true, nil
}

// lookupParent walks name's directory components without creating anything,
// returning the bucket that should directly contain name's final path
// element together with that element's key. A missing (or non-directory)
// ancestor results in fs.ErrNotExist. name must not be the root.
func lookupParent(tx *bolt.Tx, name string) (*bolt.Bucket, []byte, error) {
	bkt, err := rootBucket(tx)
	if err != nil {
		return nil, nil, err
	}
	dir, last := path.Split(name)
	if dir != "" {
		for part := range strings.SplitSeq(dir[:len(dir)-1], pathutil.UnixSeparator) {
			if bkt = bkt.Bucket([]byte(part)); bkt == nil {
				return nil, nil, fs.ErrNotExist
			}
		}
	}
	return bkt, []byte(last), nil
}

// parentBucket is like lookupParent but creates any missing ancestor
// directories (with default metadata). It must only be called on a writable
// transaction.
func parentBucket(tx *bolt.Tx, name string) (*bolt.Bucket, []byte, error) {
	bkt, err := rootBucket(tx)
	if err != nil {
		return nil, nil, err
	}
	dir, last := path.Split(name)
	if dir != "" {
		now := time.Now()
		for part := range strings.SplitSeq(dir[:len(dir)-1], pathutil.UnixSeparator) {
			if bkt, _, err = createDirBucket(bkt, []byte(part), fs.ModeDir|fs.ModePerm, now); err != nil {
				return nil, nil, err
			}
		}
	}
	return bkt, []byte(last), nil
}

// lookupDir resolves the bucket representing the directory at name (which may
// be the root) without creating anything. It returns fs.ErrNotExist if name
// does not exist and an error wrapping fs.ErrInvalid if it is a file.
func lookupDir(tx *bolt.Tx, name string) (*bolt.Bucket, error) {
	if name == pathutil.CwdPath {
		return rootBucket(tx)
	}
	parent, key, err := lookupParent(tx, name)
	if err != nil {
		return nil, err
	}
	if bkt := parent.Bucket(key); bkt != nil {
		return bkt, nil
	}
	if parent.Get(key) != nil {
		return nil, fmt.Errorf("%q is not a directory: %w", name, fs.ErrInvalid)
	}
	return nil, fs.ErrNotExist
}

// dirMeta returns the mode and modTime stored in a directory bucket's selfKey
// record.
func dirMeta(bkt *bolt.Bucket) (fs.FileMode, time.Time, error) {
	rec, err := decodeBoltRecord(bkt.Get(selfKey))
	if err != nil {
		return 0, time.Time{}, err
	}
	return rec.mode, rec.modTime, nil
}

// listBucket returns a sorted snapshot of the immediate children of bkt.
func listBucket(bkt *bolt.Bucket) ([]fs.DirEntry, error) {
	var entries []fs.DirEntry
	err := bkt.ForEach(func(k, v []byte) error {
		if bytes.Equal(k, selfKey) {
			return nil
		}
		if v == nil {
			mode, modTime, err := dirMeta(bkt.Bucket(k))
			if err != nil {
				return err
			}
			entries = append(entries, fs.FileInfoToDirEntry(ufs.NewFileInfo(string(k), osutil.EmptyDirSize, mode, modTime)))
			return nil
		}
		rec, err := decodeBoltRecord(v)
		if err != nil {
			return err
		}
		entries = append(entries, fs.FileInfoToDirEntry(ufs.NewFileInfo(string(k), int64(len(rec.content)), rec.mode, rec.modTime)))
		return nil
	})
	if err != nil {
		return nil, err
	}
	// bbolt iterates keys in byte order, which is exactly the lexical order
	// fs.ReadDir promises, so entries are already sorted; selfKey (".") is
	// skipped above.
	return entries, nil
}

func (fsys *boltFS) URI() (*url.URL, error) {
	return url.Parse(fsys.name)
}

func (fsys *boltFS) String() string {
	name := fsys.name
	if u, err := fsys.URI(); err == nil && u != nil {
		name = u.String()
	}
	return fmt.Sprintf("boltFS(%s)", name)
}

func (fsys *boltFS) isClosed() bool {
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()
	return fsys.db == nil
}

func (fsys *boltFS) liveDB() (*bolt.DB, error) {
	fsys.mu.RLock()
	db := fsys.db
	fsys.mu.RUnlock()
	if db == nil {
		return nil, fs.ErrClosed
	}
	return db, nil
}

// view runs fn in a read-only transaction against the live bolt database,
// returning fs.ErrClosed if the file system has already been closed.
func (fsys *boltFS) view(fn func(tx *bolt.Tx) error) error {
	db, err := fsys.liveDB()
	if err != nil {
		return err
	}
	return db.View(fn)
}

// update runs fn in a read-write transaction against the live bolt
// database, returning fs.ErrClosed if the file system has already been
// closed.
func (fsys *boltFS) update(fn func(tx *bolt.Tx) error) error {
	db, err := fsys.liveDB()
	if err != nil {
		return err
	}
	return db.Update(fn)
}

func (fsys *boltFS) Open(name string) (fs.File, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("open", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}

	var file fs.File
	err := fsys.view(func(tx *bolt.Tx) error {
		if name == pathutil.CwdPath {
			dir, err := openDirBucket(name, tx.Bucket(selfKey))
			file = dir
			return err
		}
		bkt, key, err := lookupParent(tx, name)
		if err != nil {
			return err
		}
		if sub := bkt.Bucket(key); sub != nil {
			dir, err := openDirBucket(name, sub)
			file = dir
			return err
		}
		data := bkt.Get(key)
		if data == nil {
			return fs.ErrNotExist
		}
		rec, err := decodeBoltRecord(data)
		if err != nil {
			return err
		}
		file = &boltFile{
			fsys: fsys,
			File: buffile.New(name, bytes.Clone(rec.content), rec.mode, rec.modTime),
		}
		return nil
	})
	if err != nil {
		return nil, ufserrors.NewPathError("open", name, err)
	}
	return file, nil
}

// openDirBucket snapshots bkt (the directory at name) into a boltDirFile.
func openDirBucket(name string, bkt *bolt.Bucket) (*boltDirFile, error) {
	if bkt == nil {
		return nil, fs.ErrNotExist
	}
	mode, modTime, err := dirMeta(bkt)
	if err != nil {
		return nil, err
	}
	entries, err := listBucket(bkt)
	if err != nil {
		return nil, err
	}
	return &boltDirFile{
		path:    name,
		entries: entries,
		mode:    mode,
		modTime: modTime,
	}, nil
}

func (fsys *boltFS) Close() error {
	fsys.notifyBus.CloseAll()

	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	if fsys.db == nil {
		return nil
	}
	err := fsys.db.Close()
	fsys.db = nil
	return err
}

func (fsys *boltFS) Create(name string) (ufs.File, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("create", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("create", name); err != nil {
		return nil, err
	}
	if name == pathutil.CwdPath {
		return nil, ufserrors.NewPathError("create", name, fmt.Errorf("%w: %w", errIsDirectory, fs.ErrInvalid))
	}

	now := time.Now()
	mode := fs.ModePerm
	record, err := encodeBoltRecord(mode, now, nil)
	if err != nil {
		return nil, ufserrors.NewPathError("create", name, err)
	}
	var existed bool
	err = fsys.update(func(tx *bolt.Tx) error {
		bkt, key, err := parentBucket(tx, name)
		if err != nil {
			return err
		}
		if bkt.Bucket(key) != nil {
			return fmt.Errorf("%w: %w", errIsDirectory, fs.ErrInvalid)
		}
		existed = bkt.Get(key) != nil
		return bkt.Put(key, record)
	})
	if err != nil {
		return nil, ufserrors.NewPathError("create", name, err)
	}

	if existed {
		fsys.notify(ufs.NotifyWrite, name)
	} else {
		fsys.notify(ufs.NotifyCreate, name)
	}

	return &boltFile{
		fsys: fsys,
		File: buffile.New(name, nil, mode, now),
	}, nil
}

func (fsys *boltFS) MkdirAll(name string, perm fs.FileMode) error {
	if fsys.isClosed() {
		return ufserrors.NewPathError("mkdir", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("mkdir", name); err != nil {
		return err
	}
	if name == pathutil.CwdPath {
		// The root always exists.
		return nil
	}

	now := time.Now()
	var created []string
	err := fsys.update(func(tx *bolt.Tx) error {
		bkt, err := rootBucket(tx)
		if err != nil {
			return err
		}
		end := 0
		for part := range strings.SplitSeq(name, pathutil.UnixSeparator) {
			if end > 0 {
				end++
			}
			end += len(part)
			var isNew bool
			if bkt, isNew, err = createDirBucket(bkt, []byte(part), fs.ModeDir|perm, now); err != nil {
				return err
			}
			if isNew {
				created = append(created, name[:end])
			}
		}
		return nil
	})
	if err != nil {
		return ufserrors.NewPathError("mkdir", name, err)
	}
	for _, p := range created {
		fsys.notify(ufs.NotifyCreate, p)
	}
	return nil
}

func (fsys *boltFS) ReadFile(name string) ([]byte, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("readfile", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("readfile", name); err != nil {
		return nil, err
	}
	if name == pathutil.CwdPath {
		return nil, ufserrors.NewPathError("readfile", name, fmt.Errorf("%w: %w", errIsDirectory, fs.ErrInvalid))
	}
	var content []byte
	err := fsys.view(func(tx *bolt.Tx) error {
		bkt, key, err := lookupParent(tx, name)
		if err != nil {
			return err
		}
		if bkt.Bucket(key) != nil {
			return fmt.Errorf("%w: %w", errIsDirectory, fs.ErrInvalid)
		}
		data := bkt.Get(key)
		if data == nil {
			return fs.ErrNotExist
		}
		rec, err := decodeBoltRecord(data)
		if err != nil {
			return err
		}
		content = bytes.Clone(rec.content)
		return nil
	})
	if err != nil {
		return nil, ufserrors.NewPathError("readfile", name, err)
	}
	return content, nil
}

func (fsys *boltFS) ReadLink(name string) (string, error) {
	if fsys.isClosed() {
		return "", ufserrors.NewPathError("readlink", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("readlink", name); err != nil {
		return "", err
	}
	if _, err := fsys.statPath("readlink", name); err != nil {
		return "", err
	}
	// boltFS has no symlinks; every extant path is a regular file or directory.
	return "", ufserrors.NewPathError("readlink", name, fs.ErrInvalid)
}

func (fsys *boltFS) Stat(name string) (fs.FileInfo, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("stat", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("stat", name); err != nil {
		return nil, err
	}
	return fsys.statPath("stat", name)
}

func (fsys *boltFS) Lstat(name string) (fs.FileInfo, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("lstat", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("lstat", name); err != nil {
		return nil, err
	}
	return fsys.statPath("lstat", name)
}

func (fsys *boltFS) statPath(op, name string) (fs.FileInfo, error) {
	var info fs.FileInfo
	err := fsys.view(func(tx *bolt.Tx) error {
		if name == pathutil.CwdPath {
			bkt, err := rootBucket(tx)
			if err != nil {
				return err
			}
			mode, modTime, err := dirMeta(bkt)
			if err != nil {
				return err
			}
			info = ufs.NewFileInfo(pathutil.CwdPath, osutil.EmptyDirSize, mode, modTime)
			return nil
		}
		bkt, key, err := lookupParent(tx, name)
		if err != nil {
			return err
		}
		if sub := bkt.Bucket(key); sub != nil {
			mode, modTime, err := dirMeta(sub)
			if err != nil {
				return err
			}
			info = ufs.NewFileInfo(string(key), osutil.EmptyDirSize, mode, modTime)
			return nil
		}
		data := bkt.Get(key)
		if data == nil {
			return fs.ErrNotExist
		}
		rec, err := decodeBoltRecord(data)
		if err != nil {
			return err
		}
		info = ufs.NewFileInfo(string(key), int64(len(rec.content)), rec.mode, rec.modTime)
		return nil
	})
	if err != nil {
		return nil, ufserrors.NewPathError(op, name, err)
	}
	return info, nil
}

func (fsys *boltFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("readdir", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("readdir", name); err != nil {
		return nil, err
	}
	var entries []fs.DirEntry
	err := fsys.view(func(tx *bolt.Tx) error {
		bkt, err := lookupDir(tx, name)
		if err != nil {
			return err
		}
		entries, err = listBucket(bkt)
		return err
	})
	if err != nil {
		return nil, ufserrors.NewPathError("readdir", name, err)
	}
	return entries, nil
}

func (fsys *boltFS) Glob(pattern string) ([]string, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	var matches []string
	err := fsys.view(func(tx *bolt.Tx) error {
		bkt, err := rootBucket(tx)
		if err != nil {
			return err
		}
		return boltGlobWalk(bkt, pathutil.CwdPath, pattern, &matches)
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(matches)
	return matches, nil
}

// boltGlobWalk recursively matches pattern against every path under bkt
// (whose own path is prefix), appending matches to *matches.
func boltGlobWalk(bkt *bolt.Bucket, prefix, pattern string, matches *[]string) error {
	return bkt.ForEach(func(k, v []byte) error {
		if bytes.Equal(k, selfKey) {
			return nil
		}
		full := joinPath(prefix, string(k))
		matched, err := path.Match(pattern, full)
		if err != nil {
			return err
		}
		if matched {
			*matches = append(*matches, full)
		}
		if v == nil {
			return boltGlobWalk(bkt.Bucket(k), full, pattern, matches)
		}
		return nil
	})
}

func (fsys *boltFS) Remove(name string) error {
	if fsys.isClosed() {
		return ufserrors.NewPathError("remove", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("remove", name); err != nil {
		return err
	}
	if name == pathutil.CwdPath {
		return ufserrors.NewPathError("remove", name, fs.ErrPermission)
	}
	err := fsys.update(func(tx *bolt.Tx) error {
		bkt, key, err := lookupParent(tx, name)
		if err != nil {
			return err
		}
		if sub := bkt.Bucket(key); sub != nil {
			if !isEmptyDir(sub) {
				return ufserrors.ErrDirNotEmpty
			}
			return bkt.DeleteBucket(key)
		}
		if bkt.Get(key) == nil {
			return fs.ErrNotExist
		}
		return bkt.Delete(key)
	})
	if err != nil {
		return ufserrors.NewPathError("remove", name, err)
	}
	fsys.notify(ufs.NotifyRemove, name)
	return nil
}

func (fsys *boltFS) RemoveAll(name string) error {
	if fsys.isClosed() {
		return ufserrors.NewPathError("removeall", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("removeall", name); err != nil {
		return err
	}

	var removed []string
	err := fsys.update(func(tx *bolt.Tx) error {
		if name == pathutil.CwdPath {
			bkt, err := rootBucket(tx)
			if err != nil {
				return err
			}
			return removeAllChildren(bkt, pathutil.CwdPath, &removed)
		}
		bkt, key, err := lookupParent(tx, name)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if sub := bkt.Bucket(key); sub != nil {
			if err := collectPaths(sub, name, &removed); err != nil {
				return err
			}
			removed = append(removed, name)
			return bkt.DeleteBucket(key)
		}
		if bkt.Get(key) == nil {
			return nil
		}
		removed = append(removed, name)
		return bkt.Delete(key)
	})
	if err != nil {
		return ufserrors.NewPathError("removeall", name, err)
	}
	for _, p := range removed {
		fsys.notify(ufs.NotifyRemove, p)
	}
	return nil
}

// isEmptyDir reports whether the directory bucket bkt has no children. Every
// directory bucket holds its own selfKey record, so it is empty when that is
// the only key; at most two keys are inspected.
func isEmptyDir(bkt *bolt.Bucket) bool {
	c := bkt.Cursor()
	k, _ := c.First()
	if bytes.Equal(k, selfKey) {
		k, _ = c.Next()
	}
	return k == nil
}

// joinPath returns name joined under prefix, treating the root as empty.
func joinPath(prefix, name string) string {
	if prefix == pathutil.CwdPath {
		return name
	}
	return prefix + pathutil.UnixSeparator + name
}

// collectPaths appends the full path of every descendant (file or directory)
// under bkt (whose own path is prefix) into *removed, depth-first. It is
// read-only and safe to call while enumerating bkt.
func collectPaths(bkt *bolt.Bucket, prefix string, removed *[]string) error {
	return bkt.ForEach(func(k, v []byte) error {
		if bytes.Equal(k, selfKey) {
			return nil
		}
		full := joinPath(prefix, string(k))
		if v == nil {
			if err := collectPaths(bkt.Bucket(k), full, removed); err != nil {
				return err
			}
		}
		*removed = append(*removed, full)
		return nil
	})
}

// removeAllChildren deletes every child of bkt (whose own path is prefix),
// leaving bkt itself (and its selfKey) intact, appending the full path of
// everything removed to *removed.
func removeAllChildren(bkt *bolt.Bucket, prefix string, removed *[]string) error {
	// Keys are collected first because bbolt does not allow mutating a bucket
	// while iterating it with ForEach.
	var keys [][]byte
	if err := bkt.ForEach(func(k, _ []byte) error {
		if !bytes.Equal(k, selfKey) {
			keys = append(keys, bytes.Clone(k))
		}
		return nil
	}); err != nil {
		return err
	}
	for _, k := range keys {
		full := joinPath(prefix, string(k))
		if sub := bkt.Bucket(k); sub != nil {
			if err := collectPaths(sub, full, removed); err != nil {
				return err
			}
			*removed = append(*removed, full)
			if err := bkt.DeleteBucket(k); err != nil {
				return err
			}
			continue
		}
		*removed = append(*removed, full)
		if err := bkt.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

func newBoltFS(_ context.Context, name string) (ufs.FS, error) {
	return makeBoltFS(name)
}

func makeBoltFS(name string) (*boltFS, error) {
	localPath, ok := strings.CutPrefix(name, boltFSPrefix)
	if !ok {
		return nil, fmt.Errorf("%q does not contain the scheme, %q", name, boltFSPrefix)
	}
	if localPath == "" {
		return nil, fmt.Errorf("%q does not name a bolt database file", name)
	}
	absPath, err := filepath.Abs(filepath.Clean(localPath))
	if err != nil {
		return nil, fmt.Errorf("cannot resolve %q, %w", localPath, err)
	}
	// TODO: plumb bolt.Options (Timeout, ReadOnly, NoSync, NoFreelistSync,
	// etc.) and the file mode through query parameters on the bolt: URI
	// instead of hardcoding them here, mirroring how other backends (e.g.
	// nestFS mounts) take configuration via the URI.
	db, err := bolt.Open(absPath, osutil.DefaultFilePermissions, &bolt.Options{
		Timeout: time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot open bolt file %q, %w", name, err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		bkt, err := tx.CreateBucketIfNotExists(selfKey)
		if err != nil {
			return err
		}
		if bkt.Get(selfKey) != nil {
			return nil
		}
		record, err := encodeBoltRecord(fs.ModeDir|fs.ModePerm, time.Now(), nil)
		if err != nil {
			return err
		}
		return bkt.Put(selfKey, record)
	}); err != nil {
		return nil, ufserrors.Join(fmt.Errorf("cannot initialize bolt root bucket for %q, %w", name, err), db.Close())
	}
	return &boltFS{
		name:      name,
		absPath:   absPath,
		db:        db,
		notifyBus: notifybus.New(),
	}, nil
}
