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
// It implements Stat, Read, ReadAt and Seek. Each embedding backend supplies
// its own Write, WriteString and Close to decide how, and when, content is
// persisted back to the backing store.
type bufFile struct {
	mu      sync.Mutex
	path    string
	content []byte
	offset  int64
	mode    fs.FileMode
	modTime time.Time

	// dirty marks that content has been written since the file was opened (or
	// since the last commit) and has not yet been persisted back to the
	// backing store. Backends that sync on every write (memFile) leave it
	// unused; backends that defer persistence to Close (boltFile) set it in
	// Write/WriteString and clear it once the buffered content is committed.
	dirty bool
}

// newBufFile returns a bufFile populated with path, content, mode and
// modTime, so every embedding backend constructs it the same way instead of
// listing the struct's fields (and risking missing one) at each call site.
func newBufFile(path string, content []byte, mode fs.FileMode, modTime time.Time) bufFile {
	return bufFile{
		path:    path,
		content: content,
		mode:    mode,
		modTime: modTime,
	}
}

func (f *bufFile) Stat() (fs.FileInfo, error) {
	f.mu.Lock()
	info := &fsInfo{
		name:    path.Base(f.path),
		size:    int64(len(f.content)),
		mode:    f.mode,
		modTime: f.modTime,
		isDir:   false,
	}
	f.mu.Unlock()
	return info, nil
}

func (f *bufFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	if f.offset >= int64(len(f.content)) {
		f.mu.Unlock()
		return 0, io.EOF
	}
	n := copy(p, f.content[f.offset:])
	f.offset += int64(n)
	f.mu.Unlock()
	return n, nil
}

func (f *bufFile) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	if off < 0 {
		f.mu.Unlock()
		return 0, pathError("readat", f.path, fmt.Errorf("offset %d is negative: %w", off, fs.ErrInvalid))
	}
	if off >= int64(len(f.content)) {
		f.mu.Unlock()
		return 0, io.EOF
	}
	n := copy(p, f.content[off:])
	atEnd := off+int64(n) >= int64(len(f.content))
	f.mu.Unlock()
	if atEnd {
		return n, io.EOF
	}
	return n, nil
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
		newOffset = int64(len(f.content)) + offset
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
