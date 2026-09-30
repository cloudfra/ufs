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
	"io"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudfra/ufs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	pb "github.com/cloudfra/ufs/proto"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// clock is an injectable clock that runs offset from the wall clock, which
// the hot tier uses to stamp files.
type clock struct {
	offset atomic.Int64
}

func (c *clock) now() time.Time {
	return time.Now().Add(time.Duration(c.offset.Load()))
}

func (c *clock) advance(d time.Duration) {
	c.offset.Add(int64(d))
}

// memoryOnlyConfig is a memory-only cache of memory bytes whose maintenance
// goroutine only runs when signalled.
func memoryOnlyConfig(memory string) Config {
	return Config{MemorySize: memory, SweepInterval: "1h"}
}

// tieredConfig is a two-tier cache in a temporary bolt file whose maintenance
// goroutine only runs when signalled.
func tieredConfig(t *testing.T, memory, storage string) Config {
	t.Helper()
	return Config{
		StoragePath:   filepath.Join(t.TempDir(), "cache.db"),
		MemorySize:    memory,
		StorageSize:   storage,
		SweepInterval: "1h",
	}
}

func newCache(t *testing.T, cfg Config) *cacheFS {
	t.Helper()
	fsys, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	return fsys.(*cacheFS)
}

// assertNoEviction fails t at cleanup if c evicted or expired anything.
func assertNoEviction(t *testing.T, c *cacheFS) {
	t.Helper()
	t.Cleanup(func() {
		if n := c.dropped.Load(); n != 0 {
			t.Errorf("the cache dropped %d entries; the conformance config must not evict", n)
		}
	})
}

func writeFile(t *testing.T, fsys ufs.WriteFS, name, content string) {
	t.Helper()
	if dir := filepath.Dir(name); dir != "." {
		if err := fsys.MkdirAll(filepath.ToSlash(dir), fs.ModePerm); err != nil {
			t.Fatalf("MkdirAll(%q) = %v", dir, err)
		}
	}
	ufsdriversTesting.WriteFile(t, fsys, name, content)
}

func mustRead(t *testing.T, fsys fs.FS, name string) string {
	t.Helper()
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		t.Fatalf("ReadFile(%q) = %v", name, err)
	}
	return string(b)
}

func mustSync(t *testing.T, fsys ufs.FS) {
	t.Helper()
	if err := Sync(fsys); err != nil {
		t.Fatalf("Sync() = %v", err)
	}
}

func assertNotExist(t *testing.T, fsys fs.FS, name string) {
	t.Helper()
	if _, err := fs.Stat(fsys, name); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(%q) = %v, want fs.ErrNotExist", name, err)
	}
}

// hotHas reports whether the hot tier holds name.
func hotHas(c *cacheFS, name string) bool {
	_, err := c.mem.Stat(name)
	return err == nil
}

// warmNames returns the warm index's files, oldest first.
func warmNames(c *cacheFS) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var names []string
	c.warmIdx.ascend(func(e *entry) bool {
		names = append(names, e.name)
		return true
	})
	return names
}

func content(n int, fill byte) string {
	return strings.Repeat(string(fill), n)
}

func TestConformance(t *testing.T) {
	t.Run("MemoryOnly", func(t *testing.T) {
		ufsdriversTesting.WriteFS(t, func(t *testing.T) ufs.WriteFS {
			c := newCache(t, memoryOnlyConfig("64MiB"))
			assertNoEviction(t, c)
			return c
		})
	})
	for _, syncLog := range []bool{false, true} {
		t.Run(fmt.Sprintf("Tiered/SyncLog=%v", syncLog), func(t *testing.T) {
			ufsdriversTesting.WriteFS(t, func(t *testing.T) ufs.WriteFS {
				cfg := tieredConfig(t, "64MiB", "1GiB")
				cfg.SyncLog = syncLog
				c := newCache(t, cfg)
				assertNoEviction(t, c)
				return c
			})
		})
	}
}

func TestNewErrors(t *testing.T) {
	if _, err := New(t.Context(), Config{Policy: "lru"}); err == nil {
		t.Error("New(policy lru) = nil, want error")
	}
	dir := t.TempDir()
	if _, err := New(t.Context(), Config{StoragePath: dir}); err == nil {
		t.Error("New(storagePath is a directory) = nil, want error")
	}
}

func TestStringURIAndSync(t *testing.T) {
	c := newCache(t, tieredConfig(t, "1MiB", "4MiB"))
	if got := c.String(); !strings.HasPrefix(got, "simplecachefs(overlay(") {
		t.Errorf("String() = %q, want simplecachefs(overlay(...))", got)
	}
	if u, err := c.URI(); u != nil || err != nil {
		t.Errorf("URI() = (%v, %v), want (nil, nil)", u, err)
	}
	mem, err := ufs.New(t.Context(), "memory://not-a-cache")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ufsTesting.ValidateClose(t, mem))
	if err := Sync(mem); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Sync(memory FS) = %v, want errors.ErrUnsupported", err)
	}
}

func TestErrFileTooLarge(t *testing.T) {
	cfg := memoryOnlyConfig("1MiB")
	cfg.MaxFileSize = "100"
	c := newCache(t, cfg)
	f, err := c.Create("big")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, 100)); err != nil {
		t.Fatalf("Write(100 bytes) = %v", err)
	}
	_, err = f.WriteString("x")
	var pathErr *fs.PathError
	if !errors.Is(err, ErrFileTooLarge) || !errors.As(err, &pathErr) || pathErr.Path != "big" {
		t.Errorf("Write past MaxFileSize = %v, want a PathError wrapping ErrFileTooLarge", err)
	}
	// Overwriting within the limit is fine.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("abc"); err != nil {
		t.Errorf("WriteString after Seek = %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("x")); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Write after Close = %v, want fs.ErrClosed", err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("second Close() = %v", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hot.bytes != 100 {
		t.Errorf("hot bytes = %d, want 100", c.hot.bytes)
	}
}

func TestReadTracksOffset(t *testing.T) {
	c := newCache(t, memoryOnlyConfig("1MiB"))
	f, err := c.Create("file")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("abcdef"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(f, buf); err != nil {
		t.Fatal(err)
	}
	// The write lands at offset 4, growing the file by 2 bytes to 8.
	if _, err := f.WriteString("WXYZ"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, c, "file"); got != "abcdWXYZ" {
		t.Errorf("content = %q, want %q", got, "abcdWXYZ")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hot.bytes != 8 {
		t.Errorf("hot bytes = %d, want 8", c.hot.bytes)
	}
}

// fillUntilNoSpace writes 90-byte files until one fails, which it returns.
func fillUntilNoSpace(t *testing.T, c *cacheFS) (int, error) {
	t.Helper()
	for i := range 100 {
		f, err := c.Create(fmt.Sprintf("f%03d", i))
		if err != nil {
			return i, err
		}
		_, werr := f.WriteString(content(90, 'a'))
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if werr != nil {
			return i, werr
		}
	}
	t.Fatal("the hot tier never filled")
	return 0, nil
}

func waitNotEvicting(c *cacheFS) {
	for c.evicting.Load() {
		runtime.Gosched()
	}
}

func TestHardEvictMemoryOnly(t *testing.T) {
	c := newCache(t, memoryOnlyConfig("1000"))
	open, err := c.Create("open")
	if err != nil {
		t.Fatal(err)
	}
	// Hold off every sweep until the hot tier is full.
	c.sweepMu.Lock()
	n, err := fillUntilNoSpace(t, c)
	if !errors.Is(err, ErrNoSpace) {
		c.sweepMu.Unlock()
		t.Fatalf("write %d = %v, want ErrNoSpace", n, err)
	}
	if !c.evicting.Load() {
		t.Error("hard eviction did not start")
	}
	if _, err := c.Create("more"); !errors.Is(err, ErrNoSpace) {
		t.Errorf("Create during hard eviction = %v, want ErrNoSpace", err)
	}
	if err := c.MkdirAll("dir", fs.ModePerm); !errors.Is(err, ErrNoSpace) {
		t.Errorf("MkdirAll during hard eviction = %v, want ErrNoSpace", err)
	}
	if _, err := open.WriteString("x"); !errors.Is(err, ErrNoSpace) {
		t.Errorf("Write during hard eviction = %v, want ErrNoSpace", err)
	}
	if c.promotable(0) {
		t.Error("promotable() during hard eviction = true, want false")
	}
	c.sweepMu.Unlock()
	waitNotEvicting(c)

	c.mu.Lock()
	hotBytes := c.hot.bytes
	c.mu.Unlock()
	if hotBytes > c.s.memoryLow() {
		t.Errorf("hot bytes after hard eviction = %d, want <= %d", hotBytes, c.s.memoryLow())
	}
	assertNotExist(t, c, "f000")
	if got := mustRead(t, c, fmt.Sprintf("f%03d", n-1)); got != content(90, 'a') {
		t.Errorf("newest file = %q, want it kept", got)
	}
	writeFile(t, c, "after", "fits again")
	if _, err := open.WriteString("x"); err != nil {
		t.Errorf("Write after hard eviction = %v", err)
	}
	if err := open.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHardEvictTiered(t *testing.T) {
	c := newCache(t, tieredConfig(t, "1000", "10000"))
	c.sweepMu.Lock()
	n, err := fillUntilNoSpace(t, c)
	c.sweepMu.Unlock()
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("write %d = %v, want ErrNoSpace", n, err)
	}
	waitNotEvicting(c)
	if b := c.log.Bytes(); b != 0 {
		t.Errorf("dirty bytes after hard eviction = %d, want 0", b)
	}
	if hotHas(c, "f000") {
		t.Error("the oldest clean file is still hot")
	}
	// Dropped hot copies are still served from warm.
	for i := range n {
		name := fmt.Sprintf("f%03d", i)
		if got := mustRead(t, c, name); got != content(90, 'a') {
			t.Errorf("%s = %q", name, got)
		}
	}
}

func TestSyncFlushesToWarm(t *testing.T) {
	c := newCache(t, tieredConfig(t, "1MiB", "4MiB"))
	writeFile(t, c, "d/a", "alpha")
	if err := c.MkdirAll("empty/dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	mustSync(t, c)
	if got := mustRead(t, c.warm, "d/a"); got != "alpha" {
		t.Errorf("warm d/a = %q, want alpha", got)
	}
	if info, err := c.warm.Stat("empty/dir"); err != nil || !info.IsDir() {
		t.Errorf("warm Stat(empty/dir) = (%v, %v), want a directory", info, err)
	}
	if b := c.log.Bytes(); b != 0 {
		t.Errorf("dirty bytes = %d, want 0", b)
	}
	hot, err := c.mem.Stat("d/a")
	if err != nil {
		t.Fatal(err)
	}
	warm, err := c.warm.Stat("d/a")
	if err != nil {
		t.Fatal(err)
	}
	if !warm.ModTime().Equal(hot.ModTime()) {
		t.Errorf("warm ModTime = %v, want the hot ModTime %v", warm.ModTime(), hot.ModTime())
	}

	if err := c.Remove("d/a"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveAll("empty"); err != nil {
		t.Fatal(err)
	}
	mustSync(t, c)
	assertNotExist(t, c.warm, "d/a")
	assertNotExist(t, c.warm, "empty")
	if names := warmNames(c); len(names) != 0 {
		t.Errorf("warm index = %v, want empty", names)
	}
	if tombstones := c.ov.Tombstones(); len(tombstones) != 0 {
		t.Errorf("tombstones after flush = %v, want none", tombstones)
	}
}

func TestFlushThreshold(t *testing.T) {
	cfg := tieredConfig(t, "10000", "100000")
	cfg.FlushThreshold = 0.01 // 100 bytes
	cfg.SyncLog = true
	c := newCache(t, cfg)
	writeFile(t, c, "small", content(50, 's'))
	writeFile(t, c, "big", content(200, 'b'))
	deadline := time.Now().Add(10 * time.Second)
	for c.log.Bytes() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the maintenance goroutine did not flush at the threshold")
		}
		runtime.Gosched()
	}
	c.sweepMu.Lock()
	c.sweepMu.Unlock() //nolint:staticcheck // wait for the flush to finish
	if got := mustRead(t, c.warm, "big"); got != content(200, 'b') {
		t.Errorf("warm big = %q", got)
	}
}

func TestSweepInterval(t *testing.T) {
	cfg := tieredConfig(t, "10000", "100000")
	cfg.SweepInterval = "10ms"
	c := newCache(t, cfg)
	writeFile(t, c, "a", "alpha")
	deadline := time.Now().Add(10 * time.Second)
	for {
		c.sweepMu.Lock()
		_, err := c.warm.Stat("a")
		c.sweepMu.Unlock()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the maintenance goroutine did not flush on its interval")
		}
		runtime.Gosched()
	}
}

func TestClosePersistsAndReopens(t *testing.T) {
	cfg := tieredConfig(t, "1MiB", "4MiB")
	fsys, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, fsys, "old", "first")
	mustSync(t, fsys)
	time.Sleep(time.Millisecond) // distinct modification times
	writeFile(t, fsys, "d/new", "second")
	if err := fsys.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	for _, check := range []struct {
		name string
		err  error
	}{
		{"Sync", Sync(fsys)},
		{"ReadFile", func() error { _, err := fsys.ReadFile("old"); return err }()},
		{"Open", func() error { _, err := fsys.Open("old"); return err }()},
		{"Create", func() error { _, err := fsys.Create("x"); return err }()},
		{"MkdirAll", fsys.MkdirAll("x", fs.ModePerm)},
		{"Remove", fsys.Remove("old")},
		{"RemoveAll", fsys.RemoveAll("old")},
	} {
		if !errors.Is(check.err, fs.ErrClosed) {
			t.Errorf("%s after Close = %v, want fs.ErrClosed", check.name, check.err)
		}
	}

	c := newCache(t, cfg)
	if got := mustRead(t, c, "d/new"); got != "second" {
		t.Errorf("d/new after reopen = %q, want second", got)
	}
	names := warmNames(c)
	if len(names) != 2 || names[0] != "old" || names[1] != "d/new" {
		t.Errorf("warm FIFO after reopen = %v, want [old d/new]", names)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.warmIdx.bytes != int64(len("first")+len("second")) {
		t.Errorf("warm bytes after reopen = %d, want %d", c.warmIdx.bytes, len("first")+len("second"))
	}
}

// hookFS wraps the warm tier and calls hook before each BatchWrite.
type hookFS struct {
	ufs.FS
	hook func() error
}

func (h *hookFS) BatchWrite(entries []*pb.WriteLogEntry) error {
	if err := h.hook(); err != nil {
		return err
	}
	return h.FS.(interface {
		BatchWrite([]*pb.WriteLogEntry) error
	}).BatchWrite(entries)
}

func TestFailedFlushRetries(t *testing.T) {
	c := newCache(t, tieredConfig(t, "1MiB", "4MiB"))
	warm := c.warm
	errInjected := errors.New("injected")
	c.warm = &hookFS{FS: warm, hook: func() error { return errInjected }}
	writeFile(t, c, "a", "alpha")
	if err := c.RemoveAll("gone"); err != nil {
		t.Fatal(err)
	}
	if err := Sync(c); !errors.Is(err, errInjected) {
		t.Fatalf("Sync() = %v, want the injected error", err)
	}
	if !c.log.Pending("a") {
		t.Error("a is not pending after a failed flush")
	}
	if !c.ov.Hidden("gone") {
		t.Error("the tombstone was cleared after a failed flush")
	}
	c.warm = warm
	mustSync(t, c)
	if got := mustRead(t, c.warm, "a"); got != "alpha" {
		t.Errorf("warm a = %q after retry", got)
	}
}

func TestWriteRacingFlushStaysPending(t *testing.T) {
	c := newCache(t, tieredConfig(t, "1MiB", "4MiB"))
	writeFile(t, c, "a", "old")
	warm := c.warm
	c.warm = &hookFS{FS: warm, hook: func() error {
		writeFile(t, c, "a", "new")
		c.wl.Barrier()
		return nil
	}}
	mustSync(t, c)
	c.warm = warm
	if !c.log.Pending("a") {
		t.Fatal("a write racing the flush was committed")
	}
	mustSync(t, c)
	if got := mustRead(t, c.warm, "a"); got != "new" {
		t.Errorf("warm a = %q, want new", got)
	}
}

func TestRemoveRacingFlushIsNotResurrected(t *testing.T) {
	c := newCache(t, tieredConfig(t, "1MiB", "4MiB"))
	writeFile(t, c, "a", "alpha")
	warm := c.warm
	c.warm = &hookFS{FS: warm, hook: func() error { return c.Remove("a") }}
	mustSync(t, c)
	c.warm = warm
	assertNotExist(t, c, "a")
	mustSync(t, c)
	assertNotExist(t, c, "a")
	assertNotExist(t, c.warm, "a")
}

// demote flushes name and drops its hot copy, so that it is only in warm.
func demote(t *testing.T, c *cacheFS, name string) {
	t.Helper()
	mustSync(t, c)
	c.dropHot([]string{name}, nil, nil)
	if hotHas(c, name) {
		t.Fatalf("%s is still hot", name)
	}
}

func TestPromotion(t *testing.T) {
	c := newCache(t, tieredConfig(t, "1MiB", "4MiB"))
	writeFile(t, c, "d/a", "alpha")
	writeFile(t, c, "b", "bravo")
	demote(t, c, "d/a")
	demote(t, c, "b")
	before, err := c.warm.Stat("d/a")
	if err != nil {
		t.Fatal(err)
	}

	f, err := c.Open("d/a")
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(before.ModTime()) {
		t.Errorf("Open().Stat().ModTime() = %v, want the pre-promotion %v", info.ModTime(), before.ModTime())
	}
	if b, err := io.ReadAll(f); err != nil || string(b) != "alpha" {
		t.Errorf("read = (%q, %v), want alpha", b, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if !hotHas(c, "d/a") {
		t.Fatal("Open did not promote d/a")
	}
	if got := mustRead(t, c, "b"); got != "bravo" {
		t.Errorf("ReadFile(b) = %q", got)
	}
	if !hotHas(c, "b") {
		t.Fatal("ReadFile did not promote b")
	}

	mustSync(t, c)
	hot, err := c.mem.Stat("d/a")
	if err != nil {
		t.Fatal(err)
	}
	warm, err := c.warm.Stat("d/a")
	if err != nil {
		t.Fatal(err)
	}
	if !hot.ModTime().After(before.ModTime()) || !warm.ModTime().Equal(hot.ModTime()) {
		t.Errorf("after promotion and flush: hot %v, warm %v, want both equal and after %v", hot.ModTime(), warm.ModTime(), before.ModTime())
	}
	// The promoted files moved to the back of the warm FIFO, in read order.
	if names := warmNames(c); len(names) != 2 || names[0] != "d/a" || names[1] != "b" {
		t.Errorf("warm FIFO = %v, want [d/a b]", names)
	}

	// Hot hits and directories are served without promotion.
	if got := mustRead(t, c, "d/a"); got != "alpha" {
		t.Errorf("hot ReadFile = %q", got)
	}
	entries, err := fs.ReadDir(c, "d")
	if err != nil || len(entries) != 1 {
		t.Errorf("ReadDir(d) = (%v, %v), want one entry", entries, err)
	}
}

func TestNoPromotionAboveSoftLimit(t *testing.T) {
	c := newCache(t, tieredConfig(t, "1000", "10000"))
	writeFile(t, c, "a", content(90, 'a'))
	demote(t, c, "a")
	c.mu.Lock()
	c.hot.bytes += c.s.memorySoft()
	c.mu.Unlock()
	f, err := c.Open("a")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, c, "a"); got != content(90, 'a') {
		t.Errorf("ReadFile(a) = %q", got)
	}
	if hotHas(c, "a") {
		t.Error("a was promoted above the soft limit")
	}
	c.mu.Lock()
	c.hot.bytes -= c.s.memorySoft()
	c.mu.Unlock()
}

func TestPromotionRaces(t *testing.T) {
	c := newCache(t, tieredConfig(t, "1MiB", "4MiB"))
	writeFile(t, c, "a", "old")
	writeFile(t, c, "b", "old")
	demote(t, c, "a")
	demote(t, c, "b")

	// A write that lands between the warm read and the promotion wins.
	writeFile(t, c, "a", "new")
	c.promote("a", []byte("old"))
	if got := mustRead(t, c, "a"); got != "new" {
		t.Errorf("a = %q, want new", got)
	}
	// A remove that lands between them is not undone.
	if err := c.Remove("b"); err != nil {
		t.Fatal(err)
	}
	c.promote("b", []byte("old"))
	assertNotExist(t, c, "b")
}

func TestWarmEviction(t *testing.T) {
	c := newCache(t, tieredConfig(t, "1000", "2000"))
	for i := range 30 {
		writeFile(t, c, fmt.Sprintf("f%02d", i), content(90, 'a'))
		mustSync(t, c)
	}
	c.mu.Lock()
	warmBytes := c.warmIdx.bytes
	c.mu.Unlock()
	if warmBytes > c.s.storageSoft() {
		t.Errorf("warm bytes = %d, want <= %d", warmBytes, c.s.storageSoft())
	}
	assertNotExist(t, c, "f00")
	if got := mustRead(t, c, "f29"); got != content(90, 'a') {
		t.Errorf("newest file = %q", got)
	}
	if c.dropped.Load() == 0 {
		t.Error("nothing was evicted")
	}
}

func TestTTL(t *testing.T) {
	var clk clock
	cfg := tieredConfig(t, "1MiB", "4MiB")
	cfg.TTL = "1h"
	cfg.now = clk.now
	c := newCache(t, cfg)

	writeFile(t, c, "clean", "flushed")
	mustSync(t, c)
	writeFile(t, c, "dirty", "never flushed")

	clk.advance(30 * time.Minute)
	c.sweepMu.Lock()
	err := c.expireWarm()
	c.sweepMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, c, "clean"); got != "flushed" {
		t.Errorf("clean before its TTL = %q", got)
	}
	if got := mustRead(t, c, "dirty"); got != "never flushed" {
		t.Errorf("dirty before its TTL = %q", got)
	}

	clk.advance(time.Hour)
	mustSync(t, c)
	for _, name := range []string{"clean", "dirty"} {
		assertNotExist(t, c, name)
		assertNotExist(t, c.warm, name)
		if hotHas(c, name) {
			t.Errorf("%s is still hot", name)
		}
	}
	if names := warmNames(c); len(names) != 0 {
		t.Errorf("warm index = %v, want empty", names)
	}
}

func TestTTLMemoryOnly(t *testing.T) {
	var clk clock
	cfg := memoryOnlyConfig("1MiB")
	cfg.TTL = "1h"
	cfg.now = clk.now
	c := newCache(t, cfg)
	writeFile(t, c, "file", "x")
	clk.advance(30 * time.Minute)
	mustSync(t, c)
	if got := mustRead(t, c, "file"); got != "x" {
		t.Errorf("file before its TTL = %q", got)
	}
	clk.advance(time.Hour)
	mustSync(t, c)
	assertNotExist(t, c, "file")
}

func TestFIFOMemoryOnly(t *testing.T) {
	c := newCache(t, memoryOnlyConfig("1000"))
	c.sweepMu.Lock()
	for i := range 9 {
		writeFile(t, c, fmt.Sprintf("f%d", i), content(90, 'a'))
	}
	c.sweepMu.Unlock()
	mustSync(t, c)
	c.mu.Lock()
	hotBytes := c.hot.bytes
	c.mu.Unlock()
	if hotBytes > c.s.memoryLow() {
		t.Errorf("hot bytes = %d, want <= %d", hotBytes, c.s.memoryLow())
	}
	assertNotExist(t, c, "f0")
	if got := mustRead(t, c, "f8"); got != content(90, 'a') {
		t.Errorf("f8 = %q", got)
	}
}

func TestOpenWriterIsNotDropped(t *testing.T) {
	c := newCache(t, memoryOnlyConfig("1000"))
	f, err := c.Create("open")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content(90, 'o')); err != nil {
		t.Fatal(err)
	}
	c.shrinkHot(true)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, c, "open"); got != content(90, 'o') {
		t.Errorf("open = %q, want it kept while being written", got)
	}
}
