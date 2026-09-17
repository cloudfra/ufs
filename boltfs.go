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

package ufs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/cloudfra/ufs/internal/deviceinfo"
	"github.com/cloudfra/ufs/internal/fsinfo"
	"github.com/cloudfra/ufs/internal/notifybus"
	"github.com/cloudfra/ufs/internal/pathutil"
	pb "github.com/cloudfra/ufs/proto"
)

const (
	boltFSPrefix = "bolt:"
)

var (
	_ File           = (*boltFile)(nil)
	_ WriteFS        = (*boltFS)(nil)
	_ fs.GlobFS      = (*boltFS)(nil)
	_ fs.ReadDirFile = (*boltDirFile)(nil)

	// selfKey serves two roles that happen to share the same value: it is the
	// name of the top-level bolt bucket that represents the root ("." /
	// pathutil.CwdPath) directory of the file system, and it is the reserved key,
	// stored inside every directory bucket (including the root), that holds
	// that directory's own mode/modTime record. It can never collide with a
	// real file or directory name because fs.ValidPath forbids path elements
	// named "." or "..".
	selfKey = []byte(pathutil.CwdPath)
)

func init() {
	Register(Driver{
		Name:       "bolt",
		MatchFunc:  isBoltFSUri,
		CreateFunc: newBoltFS,
	})
}

// boltFS is a file system backed by a single BoltDB (bbolt) file. Directories
// are represented as nested buckets (see getOrCreateBucket) and files are stored
// as a key/value pair within their parent directory's bucket. Each directory
// bucket carries its own metadata (mode, modTime) under the reserved
// selfKey.
type boltFS struct {
	mu      sync.RWMutex
	name    string
	absPath string
	db      *bolt.DB

	notifyBus *notifybus.Bus
}

// boltFile is an open read-write handle for a regular file. Writes are
// buffered in memory (via the embedded bufFile) and only persisted back to
// the bolt database when the file is closed, so that a sequence of
// Write/WriteString calls costs a single bolt transaction instead of one per
// call.
type boltFile struct {
	bufFile
	fsys *boltFS
}

// Write appends p to the file's in-memory content. Unlike memFile, it does
// not touch the underlying bolt database; the content is only persisted (and
// NotifyWrite fired, with modTime set to the commit time) when Close is
// called.
func (f *boltFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	copy(f.writeAtOffsetLocked(len(p)), p)
	f.dirty = true
	f.mu.Unlock()
	return len(p), nil
}

func (f *boltFile) WriteString(s string) (int, error) {
	f.mu.Lock()
	copy(f.writeAtOffsetLocked(len(s)), s)
	f.dirty = true
	f.mu.Unlock()
	return len(s), nil
}

// Close persists any buffered writes to the bolt database, if the file was
// modified since it was opened, and fires a NotifyWrite event. It is safe to
// call multiple times.
func (f *boltFile) Close() error {
	f.mu.Lock()
	if !f.dirty || f.fsys == nil {
		f.mu.Unlock()
		return nil
	}
	content := bytes.Clone(f.content)
	mode := f.mode
	modTime := time.Now()
	f.modTime = modTime
	fsPath := f.path
	f.dirty = false
	f.mu.Unlock()

	if err := f.fsys.writeFileContent(fsPath, mode, modTime, content); err != nil {
		return pathutil.PathError("close", fsPath, err)
	}
	f.fsys.notify(NotifyWrite, fsPath)
	return nil
}

// writeFileContent persists content (with the given mode/modTime) to the
// record stored at path, creating any missing ancestor directories.
func (fsys *boltFS) writeFileContent(name string, mode fs.FileMode, modTime time.Time, content []byte) error {
	fsys.mu.RLock()
	db := fsys.db
	fsys.mu.RUnlock()
	if db == nil {
		return fs.ErrClosed
	}
	return db.Update(func(tx *bolt.Tx) error {
		bkt, key, err := getOrCreateBucket(tx, name)
		if err != nil {
			return err
		}
		record, err := encodeBoltRecord(mode, modTime, content)
		if err != nil {
			return err
		}
		return bkt.Put([]byte(key), record)
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
	return fsinfo.New(fsinfo.Params{
		Name:    path.Base(d.path),
		Mode:    d.mode,
		ModTime: d.modTime,
		IsDir:   true,
	}), nil
}

func (d *boltDirFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.path, Err: fmt.Errorf("is a directory")}
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
	end := d.offset + n
	if end > len(d.entries) {
		end = len(d.entries)
	}
	batch := d.entries[d.offset:end]
	d.offset = end
	return batch, nil
}

// encodeBoltRecord serializes mode, modTime and content into the protobuf
// wire encoding of pb.BoltFileRecord, the value stored for each file's key
// in the bolt database.
func encodeBoltRecord(mode fs.FileMode, modTime time.Time, content []byte) ([]byte, error) {
	data, err := proto.Marshal(&pb.BoltFileRecord{
		Mode:    uint32(mode),
		ModTime: timestamppb.New(modTime),
		Content: content,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot marshal bolt record: %w", err)
	}
	return data, nil
}

// decodeBoltRecord is the inverse of encodeBoltRecord. The returned content
// aliases the record's internal buffer and must be cloned before data is
// discarded (e.g. once the enclosing bolt transaction ends).
func decodeBoltRecord(data []byte) (fs.FileMode, time.Time, []byte, error) {
	var rec pb.BoltFileRecord
	if err := proto.Unmarshal(data, &rec); err != nil {
		return 0, time.Time{}, nil, fmt.Errorf("corrupt bolt record: %w", err)
	}
	return fs.FileMode(rec.GetMode()), rec.GetModTime().AsTime(), rec.GetContent(), nil
}

// rootBucketTx returns the top-level bucket representing the file system
// root. On a writable transaction it is created (along with its selfKey
// metadata) if missing; on a read-only transaction a missing root bucket
// results in fs.ErrNotExist.
func rootBucketTx(tx *bolt.Tx) (*bolt.Bucket, error) {
	if tx.Writable() {
		bkt, err := tx.CreateBucketIfNotExists(selfKey)
		if err != nil {
			return nil, err
		}
		if err := ensureDirSelf(bkt); err != nil {
			return nil, err
		}
		return bkt, nil
	}
	bkt := tx.Bucket(selfKey)
	if bkt == nil {
		return nil, fs.ErrNotExist
	}
	return bkt, nil
}

// ensureDirSelf stores a default selfKey record in bkt if one is not already
// present. It must only be called on a writable transaction.
func ensureDirSelf(bkt *bolt.Bucket) error {
	if bkt.Get(selfKey) != nil {
		return nil
	}
	record, err := encodeBoltRecord(fs.ModeDir|fs.ModePerm, time.Now(), nil)
	if err != nil {
		return err
	}
	return bkt.Put(selfKey, record)
}

// getOrCreateBucket walks name's directory components, returning the bucket
// that should directly contain name's final path element together with that
// element. On a writable transaction, missing intermediate directory buckets
// are created (with default metadata) as the walk proceeds; on a read-only
// transaction a missing bucket results in fs.ErrNotExist. Callers pass name
// == pathutil.CwdPath's own components only for non-root paths; use rootBucketTx
// directly (or dirBucket) to resolve the root itself.
func getOrCreateBucket(tx *bolt.Tx, name string) (*bolt.Bucket, string, error) {
	parts := pathutil.SplitPath(name)
	lastPartIdx := len(parts) - 1
	lastPart := parts[lastPartIdx]
	dirParts := parts[:lastPartIdx]

	bkt, err := rootBucketTx(tx)
	if err != nil {
		return nil, "", err
	}
	for _, part := range dirParts {
		key := []byte(part)
		if tx.Writable() {
			bkt, err = bkt.CreateBucketIfNotExists(key)
			if err != nil {
				return nil, "", err
			}
			if err := ensureDirSelf(bkt); err != nil {
				return nil, "", err
			}
			continue
		}
		bkt = bkt.Bucket(key)
		if bkt == nil {
			return nil, "", fs.ErrNotExist
		}
	}
	return bkt, lastPart, nil
}

// dirBucket resolves the bucket representing the directory at name (name may
// be pathutil.CwdPath for the root). On a writable transaction missing buckets along
// the way are created; on a read-only transaction a missing bucket results in
// fs.ErrNotExist.
func dirBucket(tx *bolt.Tx, name string) (*bolt.Bucket, error) {
	if name == pathutil.CwdPath {
		return rootBucketTx(tx)
	}
	parent, last, err := getOrCreateBucket(tx, name)
	if err != nil {
		return nil, err
	}
	key := []byte(last)
	if tx.Writable() {
		bkt, err := parent.CreateBucketIfNotExists(key)
		if err != nil {
			return nil, err
		}
		if err := ensureDirSelf(bkt); err != nil {
			return nil, err
		}
		return bkt, nil
	}
	bkt := parent.Bucket(key)
	if bkt == nil {
		return nil, fs.ErrNotExist
	}
	return bkt, nil
}

func (fsys *boltFS) getDeviceInfo() map[string]deviceinfo.Info {
	return deviceinfo.NewMap(deviceinfo.Info{
		Name:        fsys.name,
		DeviceType:  "bolt",
		ThreadCount: 1,
	})
}

func (fsys *boltFS) URI() *url.URL {
	u, _ := url.Parse(fsys.name)
	return u
}

func (fsys *boltFS) String() string {
	return fmt.Sprintf("boltFS(%s)", fsys.URI())
}

func (fsys *boltFS) isClosed() bool {
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()
	return fsys.db == nil
}

// withDB runs fn against the live bolt database, returning fs.ErrClosed if
// the file system has already been closed.
func (fsys *boltFS) withDB(fn func(db *bolt.DB) error) error {
	fsys.mu.RLock()
	db := fsys.db
	fsys.mu.RUnlock()
	if db == nil {
		return fs.ErrClosed
	}
	return fn(db)
}

func (fsys *boltFS) Open(name string) (fs.File, error) {
	if fsys.isClosed() {
		return nil, pathutil.PathError("open", name, fs.ErrClosed)
	}
	if name == pathutil.CwdPath {
		return fsys.openDir(pathutil.CwdPath)
	}
	if err := pathutil.ValidPath("open", name); err != nil {
		return nil, err
	}

	var file *boltFile
	isDir := false
	err := fsys.withDB(func(db *bolt.DB) error {
		return db.View(func(tx *bolt.Tx) error {
			bkt, key, err := getOrCreateBucket(tx, name)
			if err != nil {
				return err
			}
			keyBytes := []byte(key)
			if sub := bkt.Bucket(keyBytes); sub != nil {
				isDir = true
				return nil
			}
			data := bkt.Get(keyBytes)
			if data == nil {
				return fs.ErrNotExist
			}
			mode, modTime, content, err := decodeBoltRecord(data)
			if err != nil {
				return err
			}
			file = &boltFile{
				fsys:    fsys,
				bufFile: newBufFile(name, bytes.Clone(content), mode, modTime),
			}
			return nil
		})
	})
	if err != nil {
		return nil, pathutil.PathError("open", name, err)
	}
	if isDir {
		return fsys.openDir(name)
	}
	return file, nil
}

func (fsys *boltFS) openDir(name string) (*boltDirFile, error) {
	entries, err := fsys.listDir(name)
	if err != nil {
		return nil, pathutil.PathError("open", name, err)
	}
	mode := fs.ModeDir | fs.ModePerm
	var modTime time.Time
	_ = fsys.withDB(func(db *bolt.DB) error {
		return db.View(func(tx *bolt.Tx) error {
			bkt, err := dirBucket(tx, name)
			if err != nil {
				return err
			}
			m, mt, _, err := decodeBoltRecord(bkt.Get(selfKey))
			if err != nil {
				return err
			}
			mode, modTime = m, mt
			return nil
		})
	})
	return &boltDirFile{
		path:    name,
		entries: entries,
		mode:    mode,
		modTime: modTime,
	}, nil
}

// listDir returns a sorted snapshot of the immediate children of dir.
func (fsys *boltFS) listDir(dir string) ([]fs.DirEntry, error) {
	var entries []fs.DirEntry
	err := fsys.withDB(func(db *bolt.DB) error {
		return db.View(func(tx *bolt.Tx) error {
			bkt, err := dirBucket(tx, dir)
			if err != nil {
				return err
			}
			return bkt.ForEach(func(k, v []byte) error {
				if bytes.Equal(k, selfKey) {
					return nil
				}
				name := string(k)
				if v == nil {
					sub := bkt.Bucket(k)
					mode, modTime, _, derr := decodeBoltRecord(sub.Get(selfKey))
					if derr != nil {
						return derr
					}
					entries = append(entries, fs.FileInfoToDirEntry(fsinfo.New(fsinfo.Params{
						Name: name, Mode: mode, ModTime: modTime, IsDir: true,
					})))
					return nil
				}
				mode, modTime, content, derr := decodeBoltRecord(v)
				if derr != nil {
					return derr
				}
				entries = append(entries, fs.FileInfoToDirEntry(fsinfo.New(fsinfo.Params{
					Name: name, Size: int64(len(content)), Mode: mode, ModTime: modTime,
				})))
				return nil
			})
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries, nil
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

func (fsys *boltFS) Create(name string) (File, error) {
	if fsys.isClosed() {
		return nil, pathutil.PathError("create", name, fs.ErrClosed)
	}
	if err := pathutil.ValidPath("create", name); err != nil {
		return nil, err
	}

	now := time.Now()
	mode := fs.ModePerm
	var existed bool
	err := fsys.withDB(func(db *bolt.DB) error {
		return db.Update(func(tx *bolt.Tx) error {
			bkt, key, err := getOrCreateBucket(tx, name)
			if err != nil {
				return err
			}
			keyBytes := []byte(key)
			if bkt.Bucket(keyBytes) != nil {
				return fmt.Errorf("%q is a directory: %w", name, fs.ErrInvalid)
			}
			existed = bkt.Get(keyBytes) != nil
			record, err := encodeBoltRecord(mode, now, nil)
			if err != nil {
				return err
			}
			return bkt.Put(keyBytes, record)
		})
	})
	if err != nil {
		return nil, pathutil.PathError("create", name, err)
	}

	if existed {
		fsys.notify(NotifyWrite, name)
	} else {
		fsys.notify(NotifyCreate, name)
	}

	return &boltFile{
		fsys:    fsys,
		bufFile: newBufFile(name, nil, mode, now),
	}, nil
}

func (fsys *boltFS) MkdirAll(name string, perm fs.FileMode) error {
	if fsys.isClosed() {
		return pathutil.PathError("mkdir", name, fs.ErrClosed)
	}
	if err := pathutil.ValidPath("mkdir", name); err != nil {
		return err
	}
	if name == pathutil.CwdPath {
		// The root always exists; pathutil.SplitPath(pathutil.CwdPath) would otherwise yield a
		// single "." component that collides with selfKey.
		return nil
	}

	now := time.Now()
	var created []string
	err := fsys.withDB(func(db *bolt.DB) error {
		return db.Update(func(tx *bolt.Tx) error {
			bkt, err := rootBucketTx(tx)
			if err != nil {
				return err
			}
			accum := ""
			for i, part := range pathutil.SplitPath(name) {
				if part == "" {
					continue
				}
				if i > 0 {
					accum += "/"
				}
				accum += part
				key := []byte(part)
				existed := bkt.Bucket(key) != nil || bkt.Get(key) != nil
				bkt, err = bkt.CreateBucketIfNotExists(key)
				if err != nil {
					return err
				}
				if !existed {
					record, err := encodeBoltRecord(fs.ModeDir|perm, now, nil)
					if err != nil {
						return err
					}
					if err := bkt.Put(selfKey, record); err != nil {
						return err
					}
					created = append(created, accum)
				}
			}
			return nil
		})
	})
	if err != nil {
		return pathutil.PathError("mkdir", name, err)
	}
	for _, p := range created {
		fsys.notify(NotifyCreate, p)
	}
	return nil
}

func (fsys *boltFS) ReadFile(name string) ([]byte, error) {
	if fsys.isClosed() {
		return nil, pathutil.PathError("readfile", name, fs.ErrClosed)
	}
	if err := pathutil.ValidPath("readfile", name); err != nil {
		return nil, err
	}
	var content []byte
	err := fsys.withDB(func(db *bolt.DB) error {
		return db.View(func(tx *bolt.Tx) error {
			bkt, key, err := getOrCreateBucket(tx, name)
			if err != nil {
				return err
			}
			keyBytes := []byte(key)
			if bkt.Bucket(keyBytes) != nil {
				return nil
			}
			data := bkt.Get(keyBytes)
			if data == nil {
				return fs.ErrNotExist
			}
			_, _, c, derr := decodeBoltRecord(data)
			if derr != nil {
				return derr
			}
			content = bytes.Clone(c)
			return nil
		})
	})
	if err != nil {
		return nil, pathutil.PathError("readfile", name, err)
	}
	return content, nil
}

func (fsys *boltFS) ReadLink(name string) (string, error) {
	if fsys.isClosed() {
		return "", pathutil.PathError("readlink", name, fs.ErrClosed)
	}
	if err := pathutil.ValidPath("readlink", name); err != nil {
		return "", err
	}
	err := fsys.withDB(func(db *bolt.DB) error {
		return db.View(func(tx *bolt.Tx) error {
			bkt, key, err := getOrCreateBucket(tx, name)
			if err != nil {
				return err
			}
			keyBytes := []byte(key)
			if bkt.Bucket(keyBytes) != nil || bkt.Get(keyBytes) != nil {
				return nil
			}
			return fs.ErrNotExist
		})
	})
	if err != nil {
		return "", pathutil.PathError("readlink", name, err)
	}
	// boltFS has no symlinks; every extant path is a regular file or directory.
	return "", pathutil.PathError("readlink", name, fs.ErrInvalid)
}

func (fsys *boltFS) Stat(name string) (fs.FileInfo, error) {
	if fsys.isClosed() {
		return nil, pathutil.PathError("stat", name, fs.ErrClosed)
	}
	if name != pathutil.CwdPath {
		if err := pathutil.ValidPath("stat", name); err != nil {
			return nil, err
		}
	}
	return fsys.statPath("stat", name)
}

func (fsys *boltFS) Lstat(name string) (fs.FileInfo, error) {
	if fsys.isClosed() {
		return nil, pathutil.PathError("lstat", name, fs.ErrClosed)
	}
	if name != pathutil.CwdPath {
		if err := pathutil.ValidPath("lstat", name); err != nil {
			return nil, err
		}
	}
	return fsys.statPath("lstat", name)
}

func (fsys *boltFS) statPath(op, name string) (fs.FileInfo, error) {
	var info fs.FileInfo
	err := fsys.withDB(func(db *bolt.DB) error {
		return db.View(func(tx *bolt.Tx) error {
			if name == pathutil.CwdPath {
				bkt, err := rootBucketTx(tx)
				if err != nil {
					return err
				}
				mode, modTime, _, derr := decodeBoltRecord(bkt.Get(selfKey))
				if derr != nil {
					return derr
				}
				info = fsinfo.New(fsinfo.Params{Name: pathutil.CwdPath, Mode: mode, ModTime: modTime, IsDir: true})
				return nil
			}
			bkt, key, err := getOrCreateBucket(tx, name)
			if err != nil {
				return err
			}
			keyBytes := []byte(key)
			if sub := bkt.Bucket(keyBytes); sub != nil {
				mode, modTime, _, derr := decodeBoltRecord(sub.Get(selfKey))
				if derr != nil {
					return derr
				}
				info = fsinfo.New(fsinfo.Params{Name: key, Mode: mode, ModTime: modTime, IsDir: true})
				return nil
			}
			data := bkt.Get(keyBytes)
			if data == nil {
				return fs.ErrNotExist
			}
			mode, modTime, content, derr := decodeBoltRecord(data)
			if derr != nil {
				return derr
			}
			info = fsinfo.New(fsinfo.Params{Name: key, Size: int64(len(content)), Mode: mode, ModTime: modTime})
			return nil
		})
	})
	if err != nil {
		return nil, pathutil.PathError(op, name, err)
	}
	return info, nil
}

func (fsys *boltFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if fsys.isClosed() {
		return nil, pathutil.PathError("readdir", name, fs.ErrClosed)
	}
	if name == pathutil.CwdPath {
		return fsys.listDir(pathutil.CwdPath)
	}
	if err := pathutil.ValidPath("readdir", name); err != nil {
		return nil, err
	}
	info, err := fsys.statPath("readdir", name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, pathutil.PathError("readdir", name, fs.ErrInvalid)
	}
	entries, err := fsys.listDir(name)
	if err != nil {
		return nil, pathutil.PathError("readdir", name, err)
	}
	return entries, nil
}

func (fsys *boltFS) Glob(pattern string) ([]string, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	var matches []string
	err := fsys.withDB(func(db *bolt.DB) error {
		return db.View(func(tx *bolt.Tx) error {
			bkt := tx.Bucket(selfKey)
			if bkt == nil {
				return nil
			}
			return boltGlobWalk(bkt, pathutil.CwdPath, pattern, &matches)
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

// boltGlobWalk recursively matches pattern against every path under bkt
// (whose own path is prefix), appending matches to *matches.
func boltGlobWalk(bkt *bolt.Bucket, prefix, pattern string, matches *[]string) error {
	return bkt.ForEach(func(k, v []byte) error {
		if bytes.Equal(k, selfKey) {
			return nil
		}
		full := string(k)
		if prefix != pathutil.CwdPath {
			full = prefix + "/" + full
		}
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
		return pathutil.PathError("remove", name, fs.ErrClosed)
	}
	if err := pathutil.ValidPath("remove", name); err != nil {
		return err
	}
	if name == pathutil.CwdPath {
		return pathutil.PathError("remove", name, fs.ErrPermission)
	}
	err := fsys.withDB(func(db *bolt.DB) error {
		return db.Update(func(tx *bolt.Tx) error {
			bkt, key, err := getOrCreateBucket(tx, name)
			if err != nil {
				return err
			}
			keyBytes := []byte(key)
			if sub := bkt.Bucket(keyBytes); sub != nil {
				empty := true
				if err := sub.ForEach(func(k, _ []byte) error {
					if !bytes.Equal(k, selfKey) {
						empty = false
					}
					return nil
				}); err != nil {
					return err
				}
				if !empty {
					return errDirNotEmpty
				}
				return bkt.DeleteBucket(keyBytes)
			}
			if bkt.Get(keyBytes) == nil {
				return fs.ErrNotExist
			}
			return bkt.Delete(keyBytes)
		})
	})
	if err != nil {
		return pathutil.PathError("remove", name, err)
	}
	fsys.notify(NotifyRemove, name)
	return nil
}

func (fsys *boltFS) RemoveAll(name string) error {
	if fsys.isClosed() {
		return pathutil.PathError("removeall", name, fs.ErrClosed)
	}
	if name != pathutil.CwdPath {
		if err := pathutil.ValidPath("removeall", name); err != nil {
			return err
		}
	}

	var removed []string
	err := fsys.withDB(func(db *bolt.DB) error {
		return db.Update(func(tx *bolt.Tx) error {
			if name == pathutil.CwdPath {
				bkt := tx.Bucket(selfKey)
				if bkt == nil {
					return nil
				}
				return removeAllChildren(bkt, pathutil.CwdPath, &removed)
			}
			bkt, key, err := getOrCreateBucket(tx, name)
			if err != nil {
				if err == fs.ErrNotExist {
					return nil
				}
				return err
			}
			keyBytes := []byte(key)
			if sub := bkt.Bucket(keyBytes); sub != nil {
				if err := collectPaths(sub, name, &removed); err != nil {
					return err
				}
				removed = append(removed, name)
				return bkt.DeleteBucket(keyBytes)
			}
			if bkt.Get(keyBytes) == nil {
				return nil
			}
			removed = append(removed, name)
			return bkt.Delete(keyBytes)
		})
	})
	if err != nil {
		return pathutil.PathError("removeall", name, err)
	}
	for _, p := range removed {
		fsys.notify(NotifyRemove, p)
	}
	return nil
}

// collectPaths appends the full path of every descendant (file or directory)
// under bkt (whose own path is prefix) into *removed, depth-first. It is
// read-only and safe to call while enumerating bkt.
func collectPaths(bkt *bolt.Bucket, prefix string, removed *[]string) error {
	return bkt.ForEach(func(k, v []byte) error {
		if bytes.Equal(k, selfKey) {
			return nil
		}
		full := prefix + "/" + string(k)
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
	var keys [][]byte
	if err := bkt.ForEach(func(k, _ []byte) error {
		if bytes.Equal(k, selfKey) {
			return nil
		}
		keys = append(keys, append([]byte(nil), k...))
		return nil
	}); err != nil {
		return err
	}
	for _, k := range keys {
		full := string(k)
		if prefix != pathutil.CwdPath {
			full = prefix + "/" + full
		}
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

func newBoltFS(_ context.Context, name string) (FS, error) {
	return makeBoltFS(name)
}

func makeBoltFS(name string) (*boltFS, error) {
	localPath, ok := strings.CutPrefix(name, boltFSPrefix)
	if !ok {
		return nil, fmt.Errorf("%q does not contain the scheme, %q", name, boltFSPrefix)
	}
	localPath = filepath.Clean(localPath)
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve %q, %w", localPath, err)
	}
	// TODO: plumb bolt.Options (Timeout, ReadOnly, NoSync, NoFreelistSync,
	// etc.) and the file mode through query parameters on the bolt: URI
	// instead of hardcoding them here, mirroring how other backends (e.g.
	// nestFS mounts) take configuration via the URI.
	db, err := bolt.Open(absPath, 0o600, &bolt.Options{
		Timeout: time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot open bolt file %q, %w", name, err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := rootBucketTx(tx)
		return err
	}); err != nil {
		return nil, pathutil.JoinErrors(fmt.Errorf("cannot initialize bolt root bucket for %q, %w", name, err), db.Close())
	}
	return &boltFS{
		name:      name,
		absPath:   absPath,
		db:        db,
		notifyBus: notifybus.New(),
	}, nil
}

func isBoltFSUri(name string) bool {
	return strings.HasPrefix(name, boltFSPrefix)
}
