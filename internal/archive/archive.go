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

// Package archive opens archives (zip, tar, 7z, rar and compressed tar) as
// read-only file systems. It is the only package that imports
// github.com/mholt/archives.
package archive

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mholt/archives"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var extList = []string{".tar", ".tar.gz", ".tar.bz2", ".tar.xz", ".tar.lz4", ".tar.br", ".tar.zst", ".rar", ".zip", ".7z"}

// FS is a read-only file system over the contents of an archive. Close
// releases the archive file, after which the operations report
// [fs.ErrClosed]; Close waits for the operations that are already using the
// archive before it releases it.
//
// Stat and ReadDir are part of the interface because the archive library
// answers both from entry metadata, which is much cheaper than the io/fs
// fallbacks that go through Open. Anything else, such as reading a whole
// file, has no faster path than Open and is left to the io/fs helpers.
type FS interface {
	fs.FS
	fs.StatFS
	fs.ReadDirFS
	io.Closer
}

// IsMountablePath reports whether name has the extension of an archive format
// that New can open.
func IsMountablePath(name string) bool {
	lowerPath := strings.ToLower(name)
	for _, suffix := range extList {
		if strings.HasSuffix(lowerPath, suffix) {
			return true
		}
	}
	return false
}

// New opens the archive at the local path name. A directory is opened as a
// file system over its contents.
func New(ctx context.Context, name string) (FS, error) {
	info, err := osutil.Stat(name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		fsys, err := archives.FileSystem(ctx, name, nil)
		if err != nil {
			return nil, err
		}
		return makeArchiveFS(fsys, name, nil), nil
	}

	// Open the file ourselves and hand archives.FileSystem a stream rather than
	// a bare path. archives.ArchiveFS.Open re-opens the file with os.Open on every
	// call when given only a Path, and leaks that handle whenever the opened name
	// is a directory within the archive (its dirFile.Close is a no-op that never
	// references the opened file). Passing a Stream makes ArchiveFS reuse this
	// single file instead, so the only handle to close is the one we own here.
	file, err := osutil.Open(filepath.Clean(name))
	if err != nil {
		return nil, err
	}
	fsys, err := fileSystem(ctx, name, file)
	if err != nil {
		return nil, ufserrors.Join(err, file.Close())
	}
	return makeArchiveFS(fsys, name, file), nil
}

// NewFromFile opens the archive held by the open file, which must support
// seeking and random reads. name is the archive's file name and is used to
// help identify its format. The returned FS owns file and closes it on Close;
// on error file is left open.
func NewFromFile(ctx context.Context, name string, file fs.File) (FS, error) {
	readerAtSeeker, ok := file.(archives.ReaderAtSeeker)
	if !ok {
		return nil, fmt.Errorf("cannot mount archive %q: file does not support seek and random read", name)
	}
	fsys, err := fileSystem(ctx, name, readerAtSeeker)
	if err != nil {
		return nil, err
	}
	return makeArchiveFS(fsys, name, file), nil
}

type archiveFS struct {
	fsys    fs.FS
	name    string
	closer  io.Closer
	indexed sync.Once
	// isIndexed is set once ensureIndexed has successfully built the
	// underlying archive's implicit-directory index, letting Open skip
	// straight to fsys.fsys.Open on every later call instead of repeating the
	// detect-mismatch-then-retry dance. It stays false if indexing failed, so
	// a failed attempt keeps falling back to the slow path (which still works
	// for every explicit entry; only unindexed implicit directories need the
	// index).
	isIndexed atomic.Bool
	// closed is set by the first Close. fsys and closer are never changed
	// after makeArchiveFS, so an operation can never meet a nil archive
	// because another goroutine called Close.
	closed atomic.Bool
	// mu is what makes closed more than a hint: an operation holds RLock across
	// the closed check and every call into fsys and into the handles taken from
	// it, and Close takes Lock before releasing the archive. The check and the
	// use are therefore one critical section with respect to Close, so no
	// operation can be decided "open" and then work through an archive whose
	// file Close has already released. Readers stay concurrent with each
	// other; only Close waits.
	mu sync.RWMutex
}

func makeArchiveFS(fsys fs.FS, name string, closer io.Closer) *archiveFS {
	return &archiveFS{
		fsys:   fsys,
		name:   name,
		closer: closer,
	}
}

// ensureIndexed triggers the underlying archives.ArchiveFS's implicit-directory
// index build, which requires a full pass over every entry: the mholt/archives
// library has no mode to index directory structure alone, so this is the
// cheapest correct option without bypassing the library to parse archives
// ourselves. It runs at most once per archiveFS (sync.Once) and only when
// Open has already detected that the library returned the wrong entry for an
// implicit directory, so well-formed archives and plain file access never pay
// this cost.
func (fsys *archiveFS) ensureIndexed() {
	fsys.indexed.Do(func() {
		rdfs, ok := fsys.fsys.(fs.ReadDirFS)
		if !ok {
			return
		}
		if _, err := rdfs.ReadDir("."); err != nil {
			slog.Warn("failed to index archive", "name", fsys.name, "error", err)
			return
		}
		fsys.isIndexed.Store(true)
	})
}

// isClosed reports whether Close has already released the archive. Every
// decision to use the archive goes through this check while holding
// [archiveFS.mu], so the check and the use that follows it cannot be split by
// Close.
func (fsys *archiveFS) isClosed() bool {
	return fsys.closed.Load()
}

// Open opens name in the underlying FS. If the archive returns an entry
// whose name doesn't match (implicit directory bug in non-indexed archives),
// it triggers an index build and retries once. Once the archive is known to be
// indexed, name always resolves correctly on the first try, so later calls
// skip the detect-and-retry dance entirely. A closed file system reports
// [fs.ErrClosed].
func (fsys *archiveFS) Open(name string) (fs.File, error) {
	fsys.mu.RLock()
	if fsys.isClosed() {
		fsys.mu.RUnlock()
		return nil, ufserrors.NewPathError("open", name, fs.ErrClosed)
	}
	if fsys.isIndexed.Load() {
		file, err := fsys.fsys.Open(name)
		fsys.mu.RUnlock()
		return snapshotDir(file, err)
	}

	f, err := fsys.fsys.Open(name)
	if err != nil {
		fsys.mu.RUnlock()
		return nil, err
	}
	if name == "." {
		fsys.mu.RUnlock()
		return snapshotDirFile(f), nil
	}
	// The library's own handle is a view onto the archive Close releases, so
	// its Stat and the close of the wrong entry also stay inside the section.
	info, statErr := f.Stat()
	if statErr != nil || info.Name() == path.Base(name) {
		fsys.mu.RUnlock()
		return snapshotDirFile(f), nil
	}
	if err := f.Close(); err != nil {
		fsys.mu.RUnlock()
		return nil, fmt.Errorf("cannot close %q, %w", name, err)
	}
	// ensureIndexed walks every entry of the archive and the retry opens name
	// again, so both belong to the same section as the check above. Neither
	// calls back into archiveFS, so holding the lock across them cannot nest.
	fsys.ensureIndexed()
	file, err := fsys.fsys.Open(name)
	fsys.mu.RUnlock()
	return snapshotDir(file, err)
}

// snapshotDir adapts an archive library handle for a directory so that it
// reports the end of its listing, which io/fs requires and the library does
// not: its handles answer every ReadDir with the whole listing again.
func snapshotDir(file fs.File, err error) (fs.File, error) {
	if err != nil {
		return nil, err
	}
	return snapshotDirFile(file), nil
}

func snapshotDirFile(file fs.File) fs.File {
	dir, ok := file.(fs.ReadDirFile)
	if !ok {
		return file
	}
	return &snapshotDirFileHandle{ReadDirFile: dir}
}

// snapshotDirFileHandle answers ReadDir from one snapshot of the directory
// listing, tracking the position the library's handle does not. mu guards that
// snapshot and the position: a host can hand the one handle Open returned to two
// goroutines, and an unguarded position lets them be handed the same entries
// twice and skip others. The snapshot is taken once under the same lock, so the
// walker that gets there second reads what the first stored rather than listing
// the archive again.
type snapshotDirFileHandle struct {
	fs.ReadDirFile
	mu      sync.Mutex
	entries []fs.DirEntry
	off     int
	listed  bool
}

// ReadDir returns the next n entries of the listing, all the rest when n is not
// positive, and io.EOF once the listing is spent. The entries a call returns are
// a read-only window of the snapshot, so they stay valid after the lock is
// released.
func (dir *snapshotDirFileHandle) ReadDir(n int) ([]fs.DirEntry, error) {
	dir.mu.Lock()

	if !dir.listed {
		entries, err := dir.ReadDirFile.ReadDir(-1)
		if err != nil {
			dir.mu.Unlock()
			return nil, err
		}
		dir.entries = entries
		dir.listed = true
	}
	rest := dir.entries[dir.off:]
	if n <= 0 {
		dir.off = len(dir.entries)
		dir.mu.Unlock()
		return rest, nil
	}
	if len(rest) == 0 {
		dir.mu.Unlock()
		return nil, io.EOF
	}
	if n < len(rest) {
		rest = rest[:n]
	}
	dir.off += len(rest)
	dir.mu.Unlock()
	return rest, nil
}

// Stat forwards to the library's own Stat rather than leaving callers to the
// fs.Stat fallback of Open followed by File.Stat. archives.ArchiveFS.Stat
// resolves implicit directories correctly on its own (it compares the full
// in-archive path, not just the base name), so unlike Open it never needs
// ensureIndexed, and it avoids opening (and decompressing into) a content
// stream just to read metadata. A closed file system reports [fs.ErrClosed].
func (fsys *archiveFS) Stat(name string) (fs.FileInfo, error) {
	fsys.mu.RLock()
	if fsys.isClosed() {
		fsys.mu.RUnlock()
		return nil, ufserrors.NewPathError("stat", name, fs.ErrClosed)
	}
	// fs.Stat uses the library's own Stat (archives.ArchiveFS implements
	// fs.StatFS); its fallback is the library's Open, never archiveFS.Open, so
	// this cannot re-enter the guard above.
	info, err := fs.Stat(fsys.fsys, name)
	fsys.mu.RUnlock()
	return info, err
}

// ReadDir forwards to the library's own ReadDir, which builds and then reuses
// a directory index, rather than leaving callers to the fs.ReadDir fallback of
// Open followed by ReadDirFile.ReadDir, which would also pay for Open's
// implicit-directory check on every call.
//
// The library answers a name that is absent from that index with an empty
// listing instead of an error, so an empty listing is confirmed with Stat: an
// empty directory that the archive does store stays empty, while a name the
// archive does not store at all reports [fs.ErrNotExist]. A closed file
// system reports [fs.ErrClosed].
func (fsys *archiveFS) ReadDir(name string) ([]fs.DirEntry, error) {
	fsys.mu.RLock()
	if fsys.isClosed() {
		fsys.mu.RUnlock()
		return nil, ufserrors.NewPathError("readdir", name, fs.ErrClosed)
	}
	entries, err := fs.ReadDir(fsys.fsys, name)
	fsys.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		// The confirming Stat takes the read lock itself, so it has to stay
		// outside the section above: sync.RWMutex refuses a new reader while a
		// writer waits, so a reader that re-entered the guard here would block
		// forever against a Close that is waiting for this very section.
		if _, err := fsys.Stat(name); err != nil {
			return nil, ufserrors.NewPathError("readdir", name, fs.ErrNotExist)
		}
	}
	return entries, nil
}

// Close releases the archive file. It is idempotent: only the first call
// closes the underlying reader and every later call reports nil, and it never
// changes fsys or closer, so an operation that was already running keeps the
// archive it is using instead of meeting a nil one.
//
// Close waits for the operations that had already passed their closed check to
// finish using the archive before it releases the archive file, and every
// operation that starts after this call sets closed takes the read lock either
// before or after the teardown and is refused either way. There is no schedule
// on which an operation decides to use the archive and then works through a
// file Close has released — which for an indexed archive would mean answering
// entries and metadata from the library's index with no open file behind it.
func (fsys *archiveFS) Close() error {
	if !fsys.closed.CompareAndSwap(false, true) {
		return nil
	}
	fsys.mu.Lock()
	if fsys.closer == nil {
		fsys.mu.Unlock()
		return nil
	}
	err := fsys.closer.Close()
	fsys.mu.Unlock()
	return err
}
