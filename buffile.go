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
	"fmt"
	"io"
	"io/fs"
	"path"
	"sync"
	"time"
)

// bufFile holds the state and read-side behavior shared by every backend's
// fully-buffered file handle (memFile, boltFile): the raw content, the
// current read/write offset, and the file's mode/modTime, all guarded by mu.
// content is a bytes.Buffer so growth (on write) reuses its allocation
// strategy instead of hand-rolled append calls; reads and writes index into
// its live backing slice via Bytes(), which never advances the buffer's own
// read cursor, so content always reflects the full file regardless of our
// offset. It implements Stat, Read, ReadAt and Seek. Each embedding backend
// supplies its own Write, WriteString and Close to decide how, and when,
// content is persisted back to the backing store.
type bufFile struct {
	mu      sync.RWMutex
	path    string
	content bytes.Buffer
	offset  int64
	mode    fs.FileMode
	modTime time.Time
}

// newBufFile returns a bufFile populated with path, content, mode and
// modTime, so every embedding backend constructs it the same way instead of
// listing the struct's fields (and risking missing one) at each call site.
func newBufFile(path string, content []byte, mode fs.FileMode, modTime time.Time) bufFile {
	return bufFile{
		path:    path,
		content: *bytes.NewBuffer(content),
		mode:    mode,
		modTime: modTime,
	}
}

func (f *bufFile) Stat() (fs.FileInfo, error) {
	f.mu.RLock()
	info := &fsInfo{
		name:    path.Base(f.path),
		size:    int64(f.content.Len()),
		mode:    f.mode,
		modTime: f.modTime,
		isDir:   false,
	}
	f.mu.RUnlock()
	return info, nil
}

func (f *bufFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	b := f.content.Bytes()
	if f.offset >= int64(len(b)) {
		f.mu.Unlock()
		return 0, io.EOF
	}
	n := copy(p, b[f.offset:])
	f.offset += int64(n)
	f.mu.Unlock()
	return n, nil
}

// ReadAt only reads content and never moves the shared offset, so unlike
// Read and Seek it can run under a read lock and overlap with other readers.
func (f *bufFile) ReadAt(p []byte, off int64) (int, error) {
	f.mu.RLock()
	if off < 0 {
		f.mu.RUnlock()
		return 0, pathError("readat", f.path, fmt.Errorf("offset %d is negative: %w", off, fs.ErrInvalid))
	}
	b := f.content.Bytes()
	if off >= int64(len(b)) {
		f.mu.RUnlock()
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	atEnd := off+int64(n) >= int64(len(b))
	f.mu.RUnlock()
	if atEnd {
		return n, io.EOF
	}
	return n, nil
}

// writeAtOffsetLocked grows content as needed so that its
// [offset:offset+n) range is valid — zero-padding any gap when offset is
// past the current end, the same sparse-write behavior as os.File — via
// content.Write, so the buffer's own amortized-growth strategy handles the
// allocation. It then returns that range for the caller to copy into and
// advances offset by n. The caller must hold f.mu for the duration of the
// copy, since the returned slice aliases content's backing array.
func (f *bufFile) writeAtOffsetLocked(n int) []byte {
	if n == 0 {
		return nil
	}
	if gap := f.offset - int64(f.content.Len()); gap > 0 {
		f.content.Write(make([]byte, gap))
	}
	if end := f.offset + int64(n); end > int64(f.content.Len()) {
		f.content.Write(make([]byte, end-int64(f.content.Len())))
	}
	dst := f.content.Bytes()[f.offset : f.offset+int64(n)]
	f.offset += int64(n)
	return dst
}

func (f *bufFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = f.offset + offset
	case io.SeekEnd:
		newOffset = int64(f.content.Len()) + offset
	default:
		f.mu.Unlock()
		return 0, pathError("seek", f.path, fmt.Errorf("offset=%d whence=%d: invalid whence: %w", offset, whence, fs.ErrInvalid))
	}
	if newOffset < 0 {
		f.mu.Unlock()
		return 0, pathError("seek", f.path, fmt.Errorf("offset=%d whence=%d: position %d is before start of file: %w", offset, whence, newOffset, fs.ErrInvalid))
	}
	f.offset = newOffset
	f.mu.Unlock()
	return newOffset, nil
}
