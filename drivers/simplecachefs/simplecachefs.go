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

package simplecachefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/boltfs"
	"github.com/cloudfra/ufs/drivers/common/buffile"
	"github.com/cloudfra/ufs/drivers/common/overlay"
	"github.com/cloudfra/ufs/drivers/common/writelog"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var (
	// ErrNoSpace is returned, wrapped in an *fs.PathError, by a write that
	// does not fit in the hot tier, and by every write while the hot tier is
	// being evicted to make room.
	ErrNoSpace = errors.New("simplecachefs: out of space")
	// ErrFileTooLarge is returned, wrapped in an *fs.PathError, by a write
	// that would make a file larger than MaxFileSize.
	ErrFileTooLarge = errors.New("simplecachefs: file too large")
)

var (
	_ ufs.FS   = (*cacheFS)(nil)
	_ ufs.File = (*file)(nil)
)

// Syncer is implemented by the file system that [New] returns.
type Syncer interface {
	// Sync flushes the hot tier to the warm tier and sweeps both tiers,
	// blocking until it is done. It returns the flush error, if any.
	Sync() error
}

// Sync calls Sync on fsys, which must be a file system returned by [New].
func Sync(fsys ufs.FS) error {
	s, ok := fsys.(Syncer)
	if !ok {
		return fmt.Errorf("simplecachefs: %s cannot sync: %w", fsys, errors.ErrUnsupported)
	}
	return s.Sync()
}

// cacheFS is the file system returned by New. Reads that it doesn't
// intercept go to the embedded view: the overlay of the two tiers, or the
// hot tier alone when the cache is memory-only.
type cacheFS struct {
	ufs.FS

	s    *settings
	mem  ufs.FS              // hot tier; writes to it directly are not recorded
	ov   *overlay.FS         // nil when memory-only
	wl   *writelog.FS        // overlay layer 0: mem, recorded into log
	log  *writelog.MemoryLog // nil when memory-only
	warm ufs.FS              // nil when memory-only

	// tierMu is held in read mode by every write and remove, and in write
	// mode by promotion and by dropping hot copies, so neither can overwrite
	// a newer write or bring back a removed file.
	tierMu sync.RWMutex

	// mu guards hot and warmIdx. Tier I/O happens outside it.
	mu      sync.Mutex
	hot     *tier
	warmIdx *tier

	// sweepMu serializes sweeps: the maintenance goroutine, the hard-evict
	// goroutine and Sync.
	sweepMu sync.Mutex
	// evicting is set while the hard-evict goroutine runs.
	evicting atomic.Bool
	// dropped counts the entries evicted or expired from either tier.
	dropped atomic.Int64

	// lifeMu guards stopping and the start of background goroutines.
	lifeMu   sync.Mutex
	stopping bool
	closed   atomic.Bool
	wake     chan struct{}
	stop     chan struct{}
	wg       sync.WaitGroup
}

// New returns a cache configured by cfg. With cfg.StoragePath set, the warm
// tier is the bolt file there; entries already in it are loaded into the
// index. The returned file system implements [Syncer].
func New(ctx context.Context, cfg Config) (ufs.FS, error) {
	s, err := cfg.resolve()
	if err != nil {
		return nil, err
	}
	mem, err := ufs.New(ctx, "memory://simplecachefs")
	if err != nil {
		return nil, err
	}
	c := &cacheFS{
		FS:      mem,
		s:       s,
		mem:     mem,
		hot:     newTier(),
		warmIdx: newTier(),
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
	if !s.memoryOnly {
		if err := c.openWarm(ctx); err != nil {
			return nil, ufserrors.Join(err, mem.Close())
		}
	}
	c.wg.Add(1)
	go c.maintain()
	return c, nil
}

// openWarm opens the bolt file, indexes its files and stacks the tiers.
func (c *cacheFS) openWarm(ctx context.Context) error {
	absPath, err := filepath.Abs(c.s.storagePath)
	if err != nil {
		return fmt.Errorf("simplecachefs: cannot resolve storagePath %q, %w", c.s.storagePath, err)
	}
	warm, err := boltfs.New(ctx, "bolt:"+absPath)
	if err != nil {
		return err
	}
	if err := c.loadWarm(warm); err != nil {
		return ufserrors.Join(err, warm.Close())
	}
	mode := writelog.Async
	if c.s.syncLog {
		mode = writelog.Sync
	}
	c.warm = warm
	c.log = writelog.NewMemoryLog(c.mem)
	c.wl = writelog.NewFS(c.mem, c.log, mode)
	ov, err := overlay.New(c.wl, warm)
	if err != nil {
		return ufserrors.Join(err, warm.Close())
	}
	c.ov = ov
	c.FS = ov
	return nil
}

// loadWarm rebuilds the warm index from the files in warm, oldest first.
func (c *cacheFS) loadWarm(warm ufs.FS) error {
	var files []fs.FileInfo
	var names []string
	err := fs.WalkDir(warm, pathutil.CwdPath, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, info)
		names = append(names, name)
		return nil
	})
	if err != nil {
		return fmt.Errorf("simplecachefs: cannot index the warm tier, %w", err)
	}
	order := make([]int, len(files))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return files[a].ModTime().Compare(files[b].ModTime()) })
	for _, i := range order {
		c.warmIdx.put(names[i], files[i].Size(), files[i].ModTime())
	}
	return nil
}

// check returns the error for op on name when the cache is closed or name is
// invalid.
func (c *cacheFS) check(op, name string) error {
	if c.closed.Load() {
		return ufserrors.NewPathError(op, name, fs.ErrClosed)
	}
	return pathutil.Validate(op, name)
}

// Open opens name. A file found only in the warm tier is promoted into the
// hot tier when there is room.
func (c *cacheFS) Open(name string) (fs.File, error) {
	if c.ov == nil {
		return c.FS.Open(name)
	}
	if err := c.check("open", name); err != nil {
		return nil, err
	}
	if info, err := c.mem.Stat(name); err == nil && !info.IsDir() {
		if f, err := c.mem.Open(name); err == nil {
			return f, nil
		}
	}
	info, err := c.ov.Stat(name)
	if err != nil {
		return ufserrors.ChangePathErrorOp[fs.File](nil, err, "open")
	}
	if info.IsDir() || !c.promotable(info.Size()) {
		return c.ov.Open(name)
	}
	content, err := c.ov.ReadFile(name)
	if err != nil {
		return ufserrors.ChangePathErrorOp[fs.File](nil, err, "open")
	}
	c.promote(name, content)
	return &readFile{File: buffile.New(name, content, info.Mode(), info.ModTime())}, nil
}

// ReadFile reads name. A file found only in the warm tier is promoted into
// the hot tier when there is room.
func (c *cacheFS) ReadFile(name string) ([]byte, error) {
	if c.ov == nil {
		return c.FS.ReadFile(name)
	}
	if err := c.check("readfile", name); err != nil {
		return nil, err
	}
	content, err := c.mem.ReadFile(name)
	if !errors.Is(err, fs.ErrNotExist) {
		return content, err
	}
	content, err = c.ov.ReadFile(name)
	if err != nil {
		return nil, err
	}
	if c.promotable(int64(len(content))) {
		c.promote(name, content)
	}
	return content, nil
}

// Create creates or truncates name in the hot tier.
func (c *cacheFS) Create(name string) (ufs.File, error) {
	if err := c.check("create", name); err != nil {
		return nil, err
	}
	if c.evicting.Load() {
		return nil, ufserrors.NewPathError("create", name, ErrNoSpace)
	}
	c.tierMu.RLock()
	defer c.tierMu.RUnlock()
	inner, err := c.FS.Create(name)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	// The hot tier stamps files with the wall clock; Close records the time
	// it stored.
	e := c.hot.put(name, 0, time.Now())
	e.writers++
	c.mu.Unlock()
	return &file{File: inner, c: c, e: e, name: name}, nil
}

// MkdirAll creates the directory name in the hot tier.
func (c *cacheFS) MkdirAll(name string, perm fs.FileMode) error {
	if err := c.check("mkdir", name); err != nil {
		return err
	}
	if c.evicting.Load() {
		return ufserrors.NewPathError("mkdir", name, ErrNoSpace)
	}
	c.tierMu.RLock()
	defer c.tierMu.RUnlock()
	return c.FS.MkdirAll(name, perm)
}

// Remove removes the file or empty directory name from both tiers. The warm
// tier's copy is removed by the next flush.
func (c *cacheFS) Remove(name string) error {
	if err := c.check("remove", name); err != nil {
		return err
	}
	c.tierMu.RLock()
	defer c.tierMu.RUnlock()
	if err := c.FS.Remove(name); err != nil {
		return err
	}
	c.mu.Lock()
	c.hot.remove(name)
	c.mu.Unlock()
	return nil
}

// RemoveAll removes name and everything below it from both tiers. The warm
// tier's copies are removed by the next flush.
func (c *cacheFS) RemoveAll(name string) error {
	if err := c.check("removeall", name); err != nil {
		return err
	}
	c.tierMu.RLock()
	defer c.tierMu.RUnlock()
	if err := c.FS.RemoveAll(name); err != nil {
		return err
	}
	c.mu.Lock()
	c.hot.removeAll(name)
	c.mu.Unlock()
	return nil
}

// Sync flushes the hot tier to the warm tier and sweeps both tiers.
func (c *cacheFS) Sync() error {
	if c.closed.Load() {
		return ufserrors.NewPathError("sync", pathutil.CwdPath, fs.ErrClosed)
	}
	return c.sweep(false)
}

// Close stops the background goroutines, persists the hot tier to the warm
// tier and closes both. Later calls return nil.
func (c *cacheFS) Close() error {
	c.lifeMu.Lock()
	if c.stopping {
		c.lifeMu.Unlock()
		return nil
	}
	c.stopping = true
	close(c.stop)
	c.lifeMu.Unlock()
	c.wg.Wait()

	var syncErr error
	if c.ov != nil {
		syncErr = c.sweep(false)
	}
	c.closed.Store(true)
	return ufserrors.Join(syncErr, c.FS.Close())
}

// URI returns nil: the cache is configured programmatically and has no URI.
func (c *cacheFS) URI() (*url.URL, error) {
	return nil, nil //nolint:nilnil // no URI, as documented by ufs.URIGet
}

// String describes the cache and its tiers.
func (c *cacheFS) String() string {
	return "simplecachefs(" + c.FS.String() + ")"
}

// readFile is an open file read from the warm tier: its content, mode and
// modification time as they were before the promotion.
type readFile struct {
	buffile.File
}

func (f *readFile) Close() error {
	return nil
}

// file is a file being written to the hot tier. It checks each write
// against MaxFileSize and the hot tier's hard limit before making it.
type file struct {
	ufs.File
	c    *cacheFS
	e    *entry
	name string

	mu     sync.Mutex
	off    int64
	size   int64
	closed bool
}

func (f *file) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := f.File.Read(p)
	f.off += int64(n)
	return n, err
}

func (f *file) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	off, err := f.File.Seek(offset, whence)
	if err == nil {
		f.off = off
	}
	return off, err
}

func (f *file) Write(p []byte) (int, error) {
	return f.write("write", len(p), func() (int, error) { return f.File.Write(p) })
}

func (f *file) WriteString(s string) (int, error) {
	return f.write("write", len(s), func() (int, error) { return f.File.WriteString(s) })
}

// write reserves room for n bytes at the current offset, then calls do.
func (f *file) write(op string, n int, do func() (int, error)) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, ufserrors.NewPathError(op, f.name, fs.ErrClosed)
	}
	end := f.off + int64(n)
	if end > f.size {
		if err := f.c.reserve(f.e, end, end-f.size); err != nil {
			return 0, ufserrors.NewPathError(op, f.name, err)
		}
	}
	f.c.tierMu.RLock()
	written, err := do()
	f.c.tierMu.RUnlock()
	f.off += int64(written)
	f.size = max(f.size, f.off)
	f.c.mu.Lock()
	f.c.hot.resize(f.e, f.size)
	f.c.mu.Unlock()
	f.c.maybeSignal()
	return written, err
}

// Close closes the file, which records it in the write log, and moves it to
// the back of the hot tier's FIFO.
func (f *file) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return f.File.Close()
	}
	f.closed = true
	f.c.tierMu.RLock()
	err := f.File.Close()
	info, statErr := f.c.mem.Stat(f.name)
	f.c.tierMu.RUnlock()

	f.c.mu.Lock()
	f.e.writers--
	if f.c.hot.live(f.e) && statErr == nil && !info.IsDir() {
		f.c.hot.put(f.name, info.Size(), info.ModTime())
	}
	f.c.mu.Unlock()
	f.c.maybeSignal()
	return err
}

// reserve makes room in the hot tier for a file growing by grow bytes to
// size end, or returns why it can't.
func (c *cacheFS) reserve(e *entry, end, grow int64) error {
	if end > c.s.maxFileSize {
		return ErrFileTooLarge
	}
	if c.evicting.Load() {
		return ErrNoSpace
	}
	c.mu.Lock()
	if c.hot.bytes+grow > c.s.memorySize {
		c.mu.Unlock()
		c.startHardEvict()
		return ErrNoSpace
	}
	c.hot.resize(e, end)
	c.mu.Unlock()
	return nil
}
