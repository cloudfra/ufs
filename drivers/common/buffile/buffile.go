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

// Package buffile provides the fully-buffered file handle state shared by ufs
// drivers that load a file's whole content on open and persist it back to
// their backing store on their own schedule (for example on Close). The
// content is kept in memory, or in a temporary local file for content too large
// to hold in memory.
package buffile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sync"
	"time"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const tempFilePattern = "ufs-buffile-*"

// File holds the state and behavior shared by every driver's fully-buffered
// file handle: the raw content, the current read/write offset, the file's
// mode/modTime, and whether the content has been modified since it was last
// persisted, all guarded by a single mutex. It implements Stat, Read, ReadAt,
// Seek, Write and WriteString. Writes only modify the buffered content; each
// embedding driver supplies its own Close to decide how, and when, the
// content is persisted back to its backing store (see [File.TakeDirty]).
type File struct {
	mu      sync.Mutex
	path    string
	content bytes.Buffer
	// tmp, when set, holds the content in place of content, and tmpSize is
	// its length. Only [NewTempFile] sets it.
	tmp     *os.File
	tmpSize int64
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

// NewTempFile returns a File like [New] whose content is everything r returns,
// kept in a new temporary local file instead of in memory. Persist it with
// [File.FlushTo] and call [File.Release] once it is no longer needed, which
// removes the temporary file.
func NewTempFile(path string, r io.Reader, mode fs.FileMode, modTime time.Time) (File, error) {
	tmp, err := osutil.CreateTemp("", tempFilePattern)
	if err != nil {
		return File{}, err
	}
	n, err := io.Copy(tmp, r)
	if err != nil {
		return File{}, ufserrors.Join(err, removeTemp(tmp))
	}
	return File{
		path:    path,
		tmp:     tmp,
		tmpSize: n,
		mode:    mode,
		modTime: modTime,
	}, nil
}

// Path returns the path the file was opened with.
func (f *File) Path() string {
	return f.path
}

// Stat returns the file's current name, size, mode and modification time.
func (f *File) Stat() (fs.FileInfo, error) {
	f.mu.Lock()
	info := &fileInfo{name: path.Base(f.path), size: f.sizeLocked(), mode: f.mode, modTime: f.modTime}
	f.mu.Unlock()
	return info, nil
}

// Read reads from the current offset and advances it, returning [io.EOF]
// once the offset reaches the end of the content.
func (f *File) Read(p []byte) (int, error) {
	f.mu.Lock()
	if f.tmp != nil {
		n, err := f.readTempLocked(p, f.offset)
		f.offset += int64(n)
		f.mu.Unlock()
		if n > 0 && errors.Is(err, io.EOF) {
			err = nil
		}
		return n, err
	}
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
	if f.tmp != nil {
		n, err := f.readTempLocked(p, off)
		f.mu.Unlock()
		return n, err
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
		newOffset = f.sizeLocked() + offset
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
	if f.tmp != nil {
		n, err := f.writeTempLocked(p)
		f.mu.Unlock()
		return n, err
	}
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
	if f.tmp != nil {
		n, err := f.writeTempLocked([]byte(s))
		f.mu.Unlock()
		return n, err
	}
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
//
// A file from [NewTempFile] is read back into memory for the copy. TakeDirty
// cannot report a failure to read it, so it then leaves the file dirty and
// returns ok false; use [File.FlushTo] for such a file instead.
func (f *File) TakeDirty(modTime time.Time) (content []byte, mode fs.FileMode, ok bool) {
	f.mu.Lock()
	if !f.dirty {
		f.mu.Unlock()
		return nil, 0, false
	}
	if f.tmp != nil {
		content = make([]byte, f.tmpSize)
		if _, err := f.tmp.ReadAt(content, 0); err != nil && !errors.Is(err, io.EOF) {
			f.mu.Unlock()
			return nil, 0, false
		}
	} else {
		content = bytes.Clone(f.content.Bytes())
	}
	mode = f.mode
	f.modTime = modTime
	f.dirty = false
	f.mu.Unlock()
	return content, mode, true
}

// FlushTo writes the whole content to w if the file has been written since it
// was opened or since it was last persisted, and reports whether it was. On
// success it stamps the file with modTime and clears the dirty flag; on failure
// the file stays dirty, so a later FlushTo retries the write. Unlike
// [File.TakeDirty] it makes no copy of the content.
func (f *File) FlushTo(w io.Writer, modTime time.Time) (bool, error) {
	f.mu.Lock()
	if !f.dirty {
		f.mu.Unlock()
		return false, nil
	}
	var err error
	if f.tmp != nil {
		_, err = io.Copy(w, io.NewSectionReader(f.tmp, 0, f.tmpSize))
	} else {
		var n int
		n, err = w.Write(f.content.Bytes())
		if err == nil && n != f.content.Len() {
			err = io.ErrShortWrite
		}
	}
	if err == nil {
		f.modTime = modTime
		f.dirty = false
	}
	f.mu.Unlock()
	return true, err
}

// Release frees the content, removing the temporary file of a file from
// [NewTempFile]. The file must not be used afterwards.
func (f *File) Release() error {
	f.mu.Lock()
	f.content = bytes.Buffer{}
	var err error
	if f.tmp != nil {
		err = removeTemp(f.tmp)
		f.tmp = nil
		f.tmpSize = 0
	}
	f.mu.Unlock()
	return err
}

// Dirty reports whether the file has been written since it was opened or since
// it was last persisted.
func (f *File) Dirty() bool {
	f.mu.Lock()
	dirty := f.dirty
	f.mu.Unlock()
	return dirty
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

// sizeLocked returns the length of the content. f.mu must be held.
func (f *File) sizeLocked() int64 {
	if f.tmp != nil {
		return f.tmpSize
	}
	return int64(f.content.Len())
}

// readTempLocked reads the temporary file at off, returning [io.EOF] alongside
// the final bytes of the content as ReadAt does for memory. f.mu must be held
// and f.tmp set.
func (f *File) readTempLocked(p []byte, off int64) (int, error) {
	if off >= f.tmpSize {
		return 0, io.EOF
	}
	n, err := f.tmp.ReadAt(p[:min(int64(len(p)), f.tmpSize-off)], off)
	if err == nil && off+int64(n) >= f.tmpSize {
		err = io.EOF
	}
	return n, err
}

// writeTempLocked writes p to the temporary file at the current offset, which
// like a Write in memory zero-fills a gap left by a Seek past the end, and
// advances the offset by what was written. f.mu must be held and f.tmp set.
func (f *File) writeTempLocked(p []byte) (int, error) {
	n, err := f.tmp.WriteAt(p, f.offset)
	f.offset += int64(n)
	f.tmpSize = max(f.tmpSize, f.offset)
	if n > 0 {
		f.dirty = true
	}
	return n, err
}

// removeTemp closes and deletes a temporary file.
func removeTemp(tmp *os.File) error {
	return ufserrors.Join(tmp.Close(), osutil.DeleteFile(tmp.Name()))
}

// fileInfo is the [fs.FileInfo] that Stat returns.
type fileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
}

func (i *fileInfo) Name() string {
	return i.name
}

func (i *fileInfo) Size() int64 {
	return i.size
}

func (i *fileInfo) Mode() fs.FileMode {
	return i.mode
}

func (i *fileInfo) ModTime() time.Time {
	return i.modTime
}

func (i *fileInfo) IsDir() bool {
	return i.mode.IsDir()
}

func (i *fileInfo) Sys() any {
	return nil
}
