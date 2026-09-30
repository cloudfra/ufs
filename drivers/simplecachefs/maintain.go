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
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"slices"
	"time"

	"github.com/cloudfra/ufs/drivers/common/writelog"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	pb "github.com/cloudfra/ufs/proto"
)

// maintain runs a sweep every SweepInterval and whenever it is signalled,
// until the cache is closed.
func (c *cacheFS) maintain() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.s.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
		case <-c.wake:
		}
		if err := c.sweep(false); err != nil {
			slog.Warn("simplecachefs: sweep failed", "cache", c.String(), "error", err)
		}
	}
}

// signal wakes the maintenance goroutine without blocking.
func (c *cacheFS) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// maybeSignal wakes the maintenance goroutine when the hot tier has reached
// its soft limit or enough dirty data to flush.
func (c *cacheFS) maybeSignal() {
	c.mu.Lock()
	hotBytes := c.hot.bytes
	c.mu.Unlock()
	if hotBytes >= c.s.memorySoft() || (c.log != nil && c.log.Bytes() >= c.s.flushBytes()) {
		c.signal()
	}
}

// startHardEvict starts the hard-evict goroutine unless it is running or the
// cache is closing. Writes fail with ErrNoSpace until it finishes.
func (c *cacheFS) startHardEvict() {
	c.lifeMu.Lock()
	defer c.lifeMu.Unlock()
	if c.stopping || !c.evicting.CompareAndSwap(false, true) {
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer c.evicting.Store(false)
		if err := c.sweep(true); err != nil {
			slog.Warn("simplecachefs: hard eviction failed", "cache", c.String(), "error", err)
		}
	}()
}

// sweep flushes the hot tier, expires entries past their TTL and drops clean
// hot entries once the hot tier reaches its soft limit, down to its
// low-water mark. With force, it drops them down to the low-water mark
// whatever the hot tier's size. It returns the flush error, if any.
func (c *cacheFS) sweep(force bool) error {
	c.sweepMu.Lock()
	defer c.sweepMu.Unlock()
	var err error
	if c.ov != nil {
		err = ufserrors.Join(c.flush(), c.expireWarm())
	} else {
		c.expireHot()
	}
	c.shrinkHot(force)
	return err
}

// flush writes the write log and the overlay's tombstones to the warm tier in
// one batch, evicting the oldest warm entries when the batch would take the
// warm tier past its soft limit. On failure nothing is committed, so the next
// flush retries.
func (c *cacheFS) flush() error {
	c.wl.Barrier()
	tombstones := c.ov.Tombstones()
	snapshot, err := c.log.Snapshot()
	if err != nil {
		return fmt.Errorf("simplecachefs: flush: %w", err)
	}

	var expired []*pb.WriteLogEntry
	entries := make([]*pb.WriteLogEntry, 0, len(snapshot.Entries))
	for _, e := range snapshot.Entries {
		if e.GetOp() == pb.WriteLogEntry_OP_PUT && c.expired(e.GetModTime().AsTime()) {
			expired = append(expired, e)
			continue
		}
		entries = append(entries, e)
	}

	removed := make([]string, 0, len(tombstones))
	for name := range tombstones {
		removed = append(removed, name)
	}
	slices.Sort(removed)
	c.mu.Lock()
	evicted := c.planWarm(removed, entries)
	c.mu.Unlock()

	batch := make([]*pb.WriteLogEntry, 0, len(removed)+len(evicted)+len(entries))
	for _, name := range removed {
		batch = append(batch, removeEntry(name))
	}
	for _, name := range evicted {
		batch = append(batch, removeEntry(name))
	}
	batch = append(batch, entries...)
	if len(batch) > 0 {
		if err := writelog.Apply(c.warm, batch); err != nil {
			return fmt.Errorf("simplecachefs: flush: %w", err)
		}
	}
	if err := c.log.Commit(snapshot); err != nil {
		return fmt.Errorf("simplecachefs: flush: %w", err)
	}
	c.ov.ClearTombstones(tombstones)
	c.dropped.Add(int64(len(evicted)))

	c.mu.Lock()
	for _, e := range batch {
		switch e.GetOp() {
		case pb.WriteLogEntry_OP_REMOVE_ALL:
			c.warmIdx.removeAll(e.GetName())
		case pb.WriteLogEntry_OP_PUT:
			c.warmIdx.put(e.GetName(), e.GetSize(), e.GetModTime().AsTime())
		}
	}
	c.mu.Unlock()

	// A dirty entry that expired before it was flushed is dropped from hot,
	// unless it was written again since the snapshot.
	for _, e := range expired {
		modTime := e.GetModTime().AsTime()
		c.dropHot([]string{e.GetName()}, func(info fs.FileInfo) bool { return info.ModTime().Equal(modTime) }, nil)
	}
	c.dropHot(evicted, nil, nil)
	return nil
}

// planWarm returns the warm entries to evict so that the warm tier, after a
// batch removing removed and applying entries, stays at or below its soft
// limit: the oldest entries that the batch doesn't touch, until the warm tier
// is at or below its low-water mark. c.mu must be held.
func (c *cacheFS) planWarm(removed []string, entries []*pb.WriteLogEntry) []string {
	size := c.warmIdx.bytes
	touched := map[string]bool{}
	removeTree := func(name string) {
		for key, e := range c.warmIdx.entries {
			if !touched[key] && covers(name, key) {
				touched[key] = true
				size -= e.size
			}
		}
	}
	for _, name := range removed {
		removeTree(name)
	}
	for _, e := range entries {
		name := e.GetName()
		switch e.GetOp() {
		case pb.WriteLogEntry_OP_REMOVE_ALL:
			removeTree(name)
		case pb.WriteLogEntry_OP_PUT:
			if old := c.warmIdx.get(name); old != nil && !touched[name] {
				size -= old.size
			}
			touched[name] = true
			size += e.GetSize()
		}
	}
	if size <= c.s.storageSoft() {
		return nil
	}
	var evicted []string
	low := c.s.storageLow()
	c.warmIdx.ascend(func(e *entry) bool {
		if size <= low {
			return false
		}
		if !touched[e.name] {
			evicted = append(evicted, e.name)
			size -= e.size
		}
		return true
	})
	return evicted
}

// expireWarm removes the warm entries past their TTL, and their clean hot
// copies.
func (c *cacheFS) expireWarm() error {
	if c.s.ttl <= 0 {
		return nil
	}
	var names []string
	c.mu.Lock()
	c.warmIdx.ascend(func(e *entry) bool {
		if !c.expired(e.modTime) {
			return false
		}
		names = append(names, e.name)
		return true
	})
	c.mu.Unlock()
	if len(names) == 0 {
		return nil
	}
	batch := make([]*pb.WriteLogEntry, 0, len(names))
	for _, name := range names {
		batch = append(batch, removeEntry(name))
	}
	if err := writelog.Apply(c.warm, batch); err != nil {
		return fmt.Errorf("simplecachefs: expire: %w", err)
	}
	c.mu.Lock()
	for _, name := range names {
		c.warmIdx.remove(name)
	}
	c.mu.Unlock()
	c.dropped.Add(int64(len(names)))
	c.dropHot(names, func(info fs.FileInfo) bool { return c.expired(info.ModTime()) }, nil)
	return nil
}

// expireHot drops the hot entries past their TTL. It is used when the cache
// is memory-only, where every hot entry is the only copy.
func (c *cacheFS) expireHot() {
	if c.s.ttl <= 0 {
		return
	}
	var names []string
	c.mu.Lock()
	c.hot.ascend(func(e *entry) bool {
		if !c.expired(e.modTime) {
			return false
		}
		names = append(names, e.name)
		return true
	})
	c.mu.Unlock()
	c.dropHot(names, func(info fs.FileInfo) bool { return c.expired(info.ModTime()) }, nil)
}

// shrinkHot drops clean hot entries, oldest first, down to the hot tier's
// low-water mark: when the hot tier has reached its soft limit, or always
// with force.
func (c *cacheFS) shrinkHot(force bool) {
	low := c.s.memoryLow()
	c.mu.Lock()
	if !force && c.hot.bytes < c.s.memorySoft() {
		c.mu.Unlock()
		return
	}
	var names []string
	c.hot.ascend(func(e *entry) bool {
		names = append(names, e.name)
		return true
	})
	c.mu.Unlock()
	c.dropHot(names, nil, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.hot.bytes <= low
	})
}

// dropHot removes names from the hot tier without recording the removal, so
// a copy in the warm tier stays visible. It skips a name that is open for
// writing, has an unflushed write or is not a file, and one that keep
// (when non-nil) rejects. It stops early once done (when non-nil) returns
// true.
func (c *cacheFS) dropHot(names []string, keep func(info fs.FileInfo) bool, done func() bool) {
	if len(names) == 0 {
		return
	}
	c.tierMu.Lock()
	defer c.tierMu.Unlock()
	if c.wl != nil {
		c.wl.Barrier()
	}
	for _, name := range names {
		if done != nil && done() {
			return
		}
		c.mu.Lock()
		e := c.hot.get(name)
		busy := e != nil && e.writers > 0
		c.mu.Unlock()
		if busy || (c.log != nil && c.log.Pending(name)) {
			continue
		}
		info, err := c.mem.Stat(name)
		if errors.Is(err, fs.ErrNotExist) {
			c.mu.Lock()
			c.hot.remove(name)
			c.mu.Unlock()
			continue
		}
		if err != nil || info.IsDir() || (keep != nil && !keep(info)) {
			continue
		}
		if err := c.mem.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("simplecachefs: cannot drop hot copy", "path", name, "error", err)
			continue
		}
		c.mu.Lock()
		c.hot.remove(name)
		c.mu.Unlock()
		c.dropped.Add(1)
	}
}

// expired reports whether an entry last written at modTime is past its TTL.
func (c *cacheFS) expired(modTime time.Time) bool {
	return c.s.ttl > 0 && c.s.now().Sub(modTime) >= c.s.ttl
}

// promotable reports whether a warm file of size bytes may be promoted: the
// cache has a warm tier, isn't evicting, and the hot tier stays within its
// soft limit.
func (c *cacheFS) promotable(size int64) bool {
	if c.ov == nil || c.evicting.Load() || c.closed.Load() {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hot.bytes+size <= c.s.memorySoft()
}

// promote writes content, just read from the warm tier, to the hot tier
// through the write log, so the hot copy's modification time is now and the
// next flush writes it back to the warm tier with that time. It does nothing
// when name has been written or removed since it was read. A failure is
// logged; it never fails the read.
func (c *cacheFS) promote(name string, content []byte) {
	c.tierMu.Lock()
	defer c.tierMu.Unlock()
	if _, err := c.mem.Stat(name); !errors.Is(err, fs.ErrNotExist) || c.ov.Hidden(name) {
		return
	}
	if err := c.writeHot(name, content); err != nil {
		slog.Debug("simplecachefs: cannot promote", "path", name, "error", err)
	}
}

// writeHot writes content to name in the hot tier through the write log and
// indexes it. c.tierMu must be held.
func (c *cacheFS) writeHot(name string, content []byte) error {
	if dir := path.Dir(name); dir != pathutil.CwdPath {
		if err := c.wl.MkdirAll(dir, fs.ModePerm); err != nil {
			return err
		}
	}
	f, err := c.wl.Create(name)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		return ufserrors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	info, err := c.mem.Stat(name)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.hot.put(name, info.Size(), info.ModTime())
	c.mu.Unlock()
	c.maybeSignal()
	return nil
}

func removeEntry(name string) *pb.WriteLogEntry {
	return &pb.WriteLogEntry{Op: pb.WriteLogEntry_OP_REMOVE_ALL, Name: name}
}
