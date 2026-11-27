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

// go.etcd.io/bbolt has no support for GOARCH=wasm (it has no MaxAllocSize
// constant for that architecture, and its mmap-based storage model has no
// wasm implementation regardless); see boltfs_wasm.go for the stub used on
// that platform.
//go:build !wasm

// Package boltfs registers the "bolt:" ufs driver, a read-write file system
// stored in a single BoltDB (go.etcd.io/bbolt) file. Import it for its side
// effect to make bolt: URIs available to [ufs.New]:
//
//	import _ "github.com/cloudfra/ufs/drivers/boltfs"
//
//	fsys, err := ufs.New(ctx, "bolt:/path/to/data.db")
//
// The equivalent bolt:///path/to/data.db form is also accepted; it is the form
// the file system's URI method returns.
//
// Query parameters configure the database, and the URI method keeps them:
//
//	fsys, err := ufs.New(ctx, "bolt:/path/to/cache.db?ufs.nosync=true")
//
// The parameters are:
//
//   - ufs.nosync=true skips the fsync after each commit. Writes are much
//     faster, but a crash or power loss can lose recent writes or corrupt the
//     database, so it suits data that can be rebuilt, such as a cache.
//   - ufs.nofreelistsync=true does not write the freelist on commit, which
//     makes commits cheaper and opening the database slower.
//   - ufs.nostatistics=true turns off bolt's transaction statistics, which
//     saves a little work on every transaction.
//   - ufs.readonly=true opens the database read-only with a shared lock, so
//     that several processes can read it at once; every write fails with
//     [fs.ErrPermission]. The database must already exist. The readOnly
//     decorator, by contrast, wraps a database opened for writing.
//   - ufs.timeout=5s sets how long opening waits for the file lock that
//     another process holds; the default is one minute, and 0 waits forever.
//
// Directories are nested buckets and files are key/value entries in their
// parent directory's bucket. Writes to an open file are buffered in memory
// and committed to the database, in a single transaction, when the file is
// closed. The driver is unavailable on GOARCH=wasm, where bbolt does not
// build; there, bolt: URIs return an error.
//
// The implementation is split by the interface each method belongs to, one
// file per interface: this one holds the file-system value, the options a
// bolt: URI sets, the bucket helpers the siblings share and what
// [types.SimpleFS] asks for, and the siblings named boltfs_<interface>.go hold
// the rest. Each of them carries the GOARCH=wasm constraint of this file,
// because on that platform boltfs_wasm.go supplies the driver's stub instead.
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/common/buffile"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/httputil"
	"github.com/cloudfra/ufs/internal/notify"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	bolt "go.etcd.io/bbolt"
)

var (
	_ types.File          = (*boltFile)(nil)
	_ types.WriteFS       = (*boltFS)(nil)
	_ types.BatchCreateFS = (*boltFS)(nil)
	_ fs.GlobFS           = (*boltFS)(nil)
	_ fs.ReadDirFile      = (*boltDirFile)(nil)
	_ types.Watcher       = (*boltFS)(nil)

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

// boltOptions are the database options that a bolt: URI sets.
type boltOptions struct {
	noSync         bool
	noFreelistSync bool
	noStatistics   bool
	readOnly       bool
	timeout        time.Duration
}

// boolParams maps each bool parameter to its field of boltOptions.
var boolParams = map[string]func(*boltOptions) *bool{
	noSyncParam:         func(o *boltOptions) *bool { return &o.noSync },
	noFreelistSyncParam: func(o *boltOptions) *bool { return &o.noFreelistSync },
	noStatisticsParam:   func(o *boltOptions) *bool { return &o.noStatistics },
	readOnlyParam:       func(o *boltOptions) *bool { return &o.readOnly },
}

// parseBoltOptions reads the options from the query of a bolt: URI with
// httputil.ParseURI, which extracts the "ufs." parameters. It fails for a
// parameter it does not know, so that a misspelled option is not ignored.
// Only the query is parsed, so that a path that is not a valid URL path,
// such as one holding "%" or "#", still names its file.
func parseBoltOptions(rawQuery string) (boltOptions, error) {
	opts := boltOptions{timeout: defaultTimeout}
	if rawQuery == "" {
		return opts, nil
	}
	rest, params, err := httputil.ParseURI("?" + rawQuery)
	if err != nil {
		return opts, fmt.Errorf("%w, %w", err, fs.ErrInvalid)
	}
	if rest.RawQuery != "" {
		return opts, fmt.Errorf("unknown parameters %q, only ufs. parameters are accepted, %w", rest.RawQuery, fs.ErrInvalid)
	}
	for key, value := range params {
		if field, ok := boolParams[key]; ok {
			b, err := strconv.ParseBool(value)
			if err != nil {
				return opts, fmt.Errorf("parameter %s=%q is not a bool, %w", key, value, fs.ErrInvalid)
			}
			*field(&opts) = b
			continue
		}
		if key != timeoutParam {
			return opts, fmt.Errorf("unknown parameter %q, %w", key, fs.ErrInvalid)
		}
		timeout, err := time.ParseDuration(value)
		if err != nil || timeout < 0 {
			return opts, fmt.Errorf("parameter %s=%q is not a duration of 0 or more, %w", key, value, fs.ErrInvalid)
		}
		opts.timeout = timeout
	}
	return opts, nil
}

// query returns the URI query that sets the options of opts that differ from
// the defaults.
func (opts boltOptions) query() string {
	query := url.Values{}
	for key, field := range boolParams {
		if *field(&opts) {
			query.Set(key, "true")
		}
	}
	if opts.timeout != defaultTimeout {
		query.Set(timeoutParam, opts.timeout.String())
	}
	return query.Encode()
}

func isBoltFSUri(name string) bool {
	return strings.HasPrefix(name, boltFSPrefix)
}

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
	opts    boltOptions
	db      *bolt.DB

	notifyBus *notify.Bus
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
// Close retries it. The handle is left closed, so it can no longer write what
// a later Close would persist.
func (f *boltFile) Close() error {
	if f.fsys == nil {
		return f.File.Close()
	}
	modTime := time.Now()
	content, mode, ok := f.TakeDirty(modTime)
	if !ok {
		return f.File.Close()
	}
	name := f.Path()
	if err := f.fsys.writeFileContent(name, mode, modTime, content); err != nil {
		f.MarkDirty()
		return ufserrors.NewPathError("close", name, err)
	}
	f.fsys.notify(types.NotifyWrite, name)
	return f.File.Close()
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
//
// openDirBucket fixes path, entries, mode and modTime, so those are read-only
// once the handle exists and need no guard. offset is not: Open hands one handle
// to every caller that opens the directory, so two goroutines walking it would
// advance one position between them and each would be handed part of what the
// other was owed. mu keeps that position moving one call at a time, which is what
// makes the pair collectively see every snapshot entry exactly once, in order.
type boltDirFile struct {
	mu      sync.Mutex
	path    string
	entries []fs.DirEntry
	offset  int
	mode    fs.FileMode
	modTime time.Time
}

func (d *boltDirFile) Stat() (fs.FileInfo, error) {
	return types.NewFileInfo(path.Base(d.path), osutil.EmptyDirSize, d.mode, d.modTime), nil
}

func (d *boltDirFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.path, Err: errIsDirectory}
}

func (d *boltDirFile) Close() error {
	return nil
}

func (d *boltDirFile) ReadDir(n int) ([]fs.DirEntry, error) {
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
	end := min(d.offset+n, len(d.entries))
	batch := d.entries[d.offset:end]
	d.offset = end
	d.mu.Unlock()
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
	return parentBucketCreating(tx, name, nil)
}

// parentBucketCreating is parentBucket that also calls created, when it is
// not nil, with the path of each directory it creates, outermost first.
func parentBucketCreating(tx *bolt.Tx, name string, created func(dir string)) (*bolt.Bucket, []byte, error) {
	bkt, err := rootBucket(tx)
	if err != nil {
		return nil, nil, err
	}
	dir, last := path.Split(name)
	if dir != "" {
		now := time.Now()
		end := 0
		for part := range strings.SplitSeq(dir[:len(dir)-1], pathutil.UnixSeparator) {
			end += len(part)
			var isNew bool
			if bkt, isNew, err = createDirBucket(bkt, []byte(part), fs.ModeDir|fs.ModePerm, now); err != nil {
				return nil, nil, err
			}
			if isNew && created != nil {
				created(dir[:end])
			}
			end++
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
			entries = append(entries, fs.FileInfoToDirEntry(types.NewFileInfo(string(k), osutil.EmptyDirSize, mode, modTime)))
			return nil
		}
		rec, err := decodeBoltRecord(v)
		if err != nil {
			return err
		}
		entries = append(entries, fs.FileInfoToDirEntry(types.NewFileInfo(string(k), int64(len(rec.content)), rec.mode, rec.modTime)))
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
// database. It returns fs.ErrClosed if the file system has already been
// closed, and fs.ErrPermission if it was opened with ufs.readonly.
func (fsys *boltFS) update(fn func(tx *bolt.Tx) error) error {
	db, err := fsys.liveDB()
	if err != nil {
		return err
	}
	if fsys.opts.readOnly {
		return fmt.Errorf("opened with %s=true, %w", readOnlyParam, fs.ErrPermission)
	}
	return db.Update(fn)
}

// batch runs fn in a batched read-write transaction against the live bolt
// database, so that the batched calls of concurrent goroutines share one
// transaction. It returns fs.ErrClosed if the file system has already been
// closed, and fs.ErrPermission if it was opened with ufs.readonly.
//
// Unlike update, bbolt may call fn more than once: when a shared transaction
// is refused, fn is run again on its own. Its side effects must therefore be
// idempotent and must take effect only once batch has returned nil.
func (fsys *boltFS) batch(fn func(tx *bolt.Tx) error) error {
	db, err := fsys.liveDB()
	if err != nil {
		return err
	}
	if fsys.opts.readOnly {
		return fmt.Errorf("opened with %s=true, %w", readOnlyParam, fs.ErrPermission)
	}
	return db.Batch(fn)
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
	busErr := fsys.notifyBus.Close()

	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	if fsys.db == nil {
		return busErr
	}
	err := fsys.db.Close()
	fsys.db = nil
	return ufserrors.Join(busErr, err)
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

func newBoltFS(_ context.Context, name string) (types.WriteFS, error) {
	return makeBoltFS(name)
}

func makeBoltFS(name string) (*boltFS, error) {
	localPath, ok := strings.CutPrefix(name, boltFSPrefix)
	if !ok {
		return nil, fmt.Errorf("%q does not contain the scheme, %q", name, boltFSPrefix)
	}
	localPath, rawQuery, _ := strings.Cut(localPath, "?")
	opts, err := parseBoltOptions(rawQuery)
	if err != nil {
		return nil, fmt.Errorf("invalid bolt URI %q, %w", name, err)
	}
	// bolt:///abs/path (the form URI returns) has an empty authority.
	localPath = strings.TrimPrefix(localPath, "//")
	if localPath == "" {
		return nil, fmt.Errorf("%q does not name a bolt database file", name)
	}
	absPath, err := filepath.Abs(filepath.Clean(localPath))
	if err != nil {
		return nil, fmt.Errorf("cannot resolve %q, %w", localPath, err)
	}
	// TODO: take the file mode from the URI query too.
	db, err := bolt.Open(absPath, osutil.DefaultFilePermissions, &bolt.Options{
		Timeout:        opts.timeout,
		NoSync:         opts.noSync,
		NoFreelistSync: opts.noFreelistSync,
		NoStatistics:   opts.noStatistics,
		ReadOnly:       opts.readOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot open bolt file %q, %w", name, err)
	}
	// MaxBatchDelay is not a bolt.Options field, so it is set here. Only
	// BatchCreate uses bolt.DB.Batch, and batchMaxDelay is why it is not bbolt's
	// default.
	db.MaxBatchDelay = batchMaxDelay
	if opts.readOnly {
		// A read-only database cannot be initialized, so it must already be
		// one that this driver wrote.
		if err := db.View(func(tx *bolt.Tx) error {
			_, err := rootBucket(tx)
			return err
		}); err != nil {
			return nil, ufserrors.Join(fmt.Errorf("cannot open bolt file %q read-only, %w", name, err), db.Close())
		}
	} else if err := db.Update(func(tx *bolt.Tx) error {
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
		opts:      opts,
		db:        db,
		notifyBus: notify.New(),
	}, nil
}
