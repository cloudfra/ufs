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

// Package buffile provides the fully-buffered, in-memory file handle state
// shared by ufs drivers that load a file's whole content on open and persist
// it back to their backing store on their own schedule (for example on
// Close).
package buffile

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sync"
	"time"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// File holds the state and behavior shared by every driver's fully-buffered
// file handle: the raw content, the current read/write offset, the file's
// mode/modTime, and whether the content has been modified since it was last
// persisted, all guarded by a single mutex. It implements Stat, Read, ReadAt,
// Seek, Write and WriteString. Writes only modify the in-memory buffer; each
// embedding driver supplies its own Close to decide how, and when, the
// content is persisted back to its backing store (see [File.TakeDirty]).
type File struct {
	mu      sync.Mutex
	path    string
	content bytes.Buffer
	offset  int64
	mode    fs.FileMode
	modTime time.Time

	// dirty marks that content has been written since the file was opened (or
	// since the last TakeDirty) and has not yet been persisted back to the
	// backing store.
	dirty bool
}

// New returns a File populated with path, content, mode and modTime. The
// File takes ownership of content; callers must not modify it afterwards.
func New(path string, content []byte, mode fs.FileMode, modTime time.Time) File {
	return File{
		path:    path,
		content: *bytes.NewBuffer(content),
		mode:    mode,
		modTime: modTime,
	}
}

// Path returns the path the file was opened with.
func (f *File) Path() string {
	return f.path
}

// Stat returns the file's current name, size, mode and modification time.
func (f *File) Stat() (fs.FileInfo, error) {
	f.mu.Lock()
	info := ufs.NewFileInfo(path.Base(f.path), int64(f.content.Len()), f.mode, f.modTime)
	f.mu.Unlock()
	return info, nil
}

// Read reads from the current offset and advances it, returning [io.EOF]
// once the offset reaches the end of the content.
func (f *File) Read(p []byte) (int, error) {
	f.mu.Lock()
	if f.offset >= int64(f.content.Len()) {
		f.mu.Unlock()
		return 0, io.EOF
	}
	n := copy(p, f.content.Bytes()[f.offset:])
	f.offset += int64(n)
	f.mu.Unlock()
	return n, nil
}

// ReadAt reads len(p) bytes starting at off without moving the offset. It
// returns [io.EOF] alongside the final bytes of the content.
func (f *File) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	if off < 0 {
		f.mu.Unlock()
		return 0, ufserrors.NewPathError("readat", f.path, fmt.Errorf("offset %d is negative: %w", off, fs.ErrInvalid))
	}
	if off >= int64(f.content.Len()) {
		f.mu.Unlock()
		return 0, io.EOF
	}
	n := copy(p, f.content.Bytes()[off:])
	atEnd := off+int64(n) >= int64(f.content.Len())
	f.mu.Unlock()
	if atEnd {
		return n, io.EOF
	}
	return n, nil
}

// Seek sets the offset for the next Read or Write. Seeking past the end is
// allowed; seeking before the start returns an error wrapping [fs.ErrInvalid].
func (f *File) Seek(offset int64, whence int) (int64, error) {
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
		return 0, ufserrors.NewPathError("seek", f.path, fmt.Errorf("offset=%d whence=%d: invalid whence: %w", offset, whence, fs.ErrInvalid))
	}
	if newOffset < 0 {
		f.mu.Unlock()
		return 0, ufserrors.NewPathError("seek", f.path, fmt.Errorf("offset=%d whence=%d: position %d is before start of file: %w", offset, whence, newOffset, fs.ErrInvalid))
	}
	f.offset = newOffset
	f.mu.Unlock()
	return newOffset, nil
}

// Write writes p at the current offset, overwriting existing bytes or
// extending the file (zero-filling any gap left by a Seek past the end), and
// marks the file dirty. It never touches the backing store.
func (f *File) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	f.mu.Lock()
	n := copy(f.overwritableLocked(len(p)), p)
	f.content.Write(p[n:])
	f.offset += int64(len(p))
	f.dirty = true
	f.mu.Unlock()
	return len(p), nil
}

// WriteString is like [File.Write] but takes a string, avoiding a []byte
// conversion.
func (f *File) WriteString(s string) (int, error) {
	if len(s) == 0 {
		return 0, nil
	}
	f.mu.Lock()
	n := copy(f.overwritableLocked(len(s)), s)
	f.content.WriteString(s[n:])
	f.offset += int64(len(s))
	f.dirty = true
	f.mu.Unlock()
	return len(s), nil
}

// TakeDirty reports whether the file has been written since it was opened or
// since the last TakeDirty. If it has, TakeDirty stamps the file with
// modTime, clears the dirty flag, and returns a copy of the content and the
// file's mode for the caller to persist. If persisting fails, the caller
// should call [File.MarkDirty] so a later attempt retries the write.
func (f *File) TakeDirty(modTime time.Time) (content []byte, mode fs.FileMode, ok bool) {
	f.mu.Lock()
	if !f.dirty {
		f.mu.Unlock()
		return nil, 0, false
	}
	content = bytes.Clone(f.content.Bytes())
	mode = f.mode
	f.modTime = modTime
	f.dirty = false
	f.mu.Unlock()
	return content, mode, true
}

// MarkDirty flags the file as modified so the next [File.TakeDirty] returns
// its content again.
func (f *File) MarkDirty() {
	f.mu.Lock()
	f.dirty = true
	f.mu.Unlock()
}

// overwritableLocked prepares content for an n-byte write at the current
// offset. It reserves capacity for any growth up front, so the write costs at
// most one allocation, and zero-fills the gap left by a Seek past the end
// (the same sparse-write behavior as os.File). It returns the existing bytes
// in [offset, offset+n) that the caller must overwrite; the caller then
// appends the remainder of its data and advances offset by n. f.mu must be
// held and n must be positive.
func (f *File) overwritableLocked(n int) []byte {
	size := int64(f.content.Len())
	end := f.offset + int64(n)
	if end > size {
		f.content.Grow(int(end - size))
	}
	if gap := f.offset - size; gap > 0 {
		// Write the gap's zeros from the buffer's own spare capacity, reserved
		// by Grow above, instead of allocating a zeroed slice.
		zeros := f.content.AvailableBuffer()[:gap]
		clear(zeros)
		f.content.Write(zeros)
		size = f.offset
	}
	return f.content.Bytes()[f.offset:min(end, size)]
}
