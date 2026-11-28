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

package syncfs

import (
	"errors"
	"io"
	"io/fs"
	"sync"

	"github.com/cloudfra/ufs/internal/ufserrors"
)

var (
	_ fs.ReadDirFile = (*File)(nil)
	_ io.ReaderAt    = (*File)(nil)
	_ io.WriterAt    = (*File)(nil)
	_ io.Seeker      = (*File)(nil)
	_ io.Writer      = (*File)(nil)
)

// File is an open handle that any number of goroutines may share.
//
// The cursor methods (Read, Write, Seek, ReadDir) and Stat and Close run one
// at a time, so callers sharing the handle share one position and each byte or
// entry is handed to exactly one of them. ReadAt and WriteAt use the handle's
// own positional methods when it has them and run in parallel with each other,
// as [io.ReaderAt] and [io.WriterAt] allow, while still excluding Close. A
// handle without them gets an emulation that runs one at a time: a seek, the
// transfer and a seek back for a handle that can seek, or the transfer at the
// cursor for one that cannot, in which case an offset anywhere else reports
// [errors.ErrUnsupported].
type File struct {
	// mu is held for reading by the native positional calls and for writing by
	// everything that moves the cursor or ends the handle.
	mu sync.RWMutex
	f  fs.File
	// pos is the cursor of f as this wrapper last left it. Every call that
	// moves the cursor goes through the wrapper, so it is known without asking
	// f, and a handle that cannot seek still has a place where the next
	// in-order transfer belongs.
	pos int64
}

// NewFile wraps f so that goroutines can share it. The wrapper takes over f:
// closing the wrapper closes f.
func NewFile(f fs.File) *File {
	return &File{f: f}
}

// Stat reports the handle's file information.
func (f *File) Stat() (fs.FileInfo, error) {
	f.mu.Lock()
	info, err := f.f.Stat()
	f.mu.Unlock()
	return info, err
}

// Read reads from the shared cursor.
func (f *File) Read(p []byte) (int, error) {
	f.mu.Lock()
	n, err := f.f.Read(p)
	f.pos += int64(n)
	f.mu.Unlock()
	return n, err
}

// Write writes at the shared cursor. A handle that cannot be written reports
// an error matching [fs.ErrPermission].
func (f *File) Write(p []byte) (int, error) {
	w, ok := f.f.(io.Writer)
	if !ok {
		return 0, ufserrors.NewPathError("write", f.name(), fs.ErrPermission)
	}
	f.mu.Lock()
	n, err := w.Write(p)
	f.pos += int64(n)
	f.mu.Unlock()
	return n, err
}

// Seek moves the shared cursor. A handle that cannot seek reports an error
// matching [errors.ErrUnsupported].
func (f *File) Seek(offset int64, whence int) (int64, error) {
	s, ok := f.f.(io.Seeker)
	if !ok {
		return 0, ufserrors.NewPathError("seek", f.name(), errors.ErrUnsupported)
	}
	f.mu.Lock()
	pos, err := s.Seek(offset, whence)
	if err == nil {
		f.pos = pos
	}
	f.mu.Unlock()
	return pos, err
}

// ReadAt reads len(p) bytes at off without moving the shared cursor, and
// reports [io.EOF] when fewer are left.
func (f *File) ReadAt(p []byte, off int64) (int, error) {
	if ra, ok := f.f.(io.ReaderAt); ok {
		f.mu.RLock()
		n, err := ra.ReadAt(p, off)
		f.mu.RUnlock()
		return n, err
	}

	f.mu.Lock()
	n, err := f.readAtLocked(p, off)
	f.mu.Unlock()
	return n, err
}

func (f *File) readAtLocked(p []byte, off int64) (int, error) {
	s, ok := f.f.(io.Seeker)
	if !ok {
		// Without a seek the cursor is the only place a read can start, so a
		// read there continues the handle and moves its cursor along.
		if off != f.pos {
			return 0, ufserrors.NewPathError("read", f.name(), errors.ErrUnsupported)
		}
		n, err := readFull(f.f, p)
		f.pos += int64(n)
		return n, err
	}

	saved := f.pos
	if _, err := s.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := readFull(f.f, p)
	if _, serr := s.Seek(saved, io.SeekStart); serr != nil {
		// The cursor is wherever the read left it.
		f.pos = off + int64(n)
		return n, ufserrors.Join(err, serr)
	}
	return n, err
}

// WriteAt writes p at off without moving the shared cursor. A handle that
// takes its bytes only in order, which is what a handle that refuses to seek
// does, accepts a write at its cursor and reports [errors.ErrUnsupported] for
// any other offset rather than putting the bytes in the wrong place.
func (f *File) WriteAt(p []byte, off int64) (int, error) {
	if wa, ok := f.f.(io.WriterAt); ok {
		f.mu.RLock()
		n, err := wa.WriteAt(p, off)
		f.mu.RUnlock()
		return n, err
	}
	w, ok := f.f.(io.Writer)
	if !ok {
		return 0, ufserrors.NewPathError("write", f.name(), fs.ErrPermission)
	}

	f.mu.Lock()
	n, err := f.writeAtLocked(w, p, off)
	f.mu.Unlock()
	return n, err
}

func (f *File) writeAtLocked(w io.Writer, p []byte, off int64) (int, error) {
	inOrder := func() (int, error) {
		if off != f.pos {
			return 0, ufserrors.NewPathError("write", f.name(), errors.ErrUnsupported)
		}
		n, err := w.Write(p)
		f.pos += int64(n)
		return n, err
	}

	s, ok := f.f.(io.Seeker)
	if !ok {
		return inOrder()
	}
	saved := f.pos
	if _, err := s.Seek(off, io.SeekStart); err != nil {
		// A handle that refuses every seek, such as a whole-file upload, still
		// takes the write that continues where it stands.
		if errors.Is(err, fs.ErrInvalid) || errors.Is(err, errors.ErrUnsupported) {
			return inOrder()
		}
		return 0, err
	}
	n, err := w.Write(p)
	if _, serr := s.Seek(saved, io.SeekStart); serr != nil {
		f.pos = off + int64(n)
		return n, ufserrors.Join(err, serr)
	}
	return n, err
}

// ReadDir reads the next n entries of a directory from the shared cursor. A
// handle that is not a directory reports an error matching [fs.ErrInvalid].
func (f *File) ReadDir(n int) ([]fs.DirEntry, error) {
	d, ok := f.f.(fs.ReadDirFile)
	if !ok {
		return nil, ufserrors.NewPathError("readdir", f.name(), fs.ErrInvalid)
	}
	f.mu.Lock()
	entries, err := d.ReadDir(n)
	f.mu.Unlock()
	return entries, err
}

// Close closes the handle once every call already in progress on it has
// returned.
func (f *File) Close() error {
	f.mu.Lock()
	err := f.f.Close()
	f.mu.Unlock()
	return err
}

// name is the name reported in an error. Asking the handle costs a Stat, so it
// is only done once there is an error to report.
func (f *File) name() string {
	if info, err := f.f.Stat(); err == nil {
		return info.Name()
	}
	return ""
}

// readFull fills p from r, reporting [io.EOF] rather than
// [io.ErrUnexpectedEOF] when r ends first, as a short ReadAt does.
func readFull(r io.Reader, p []byte) (int, error) {
	n, err := io.ReadFull(r, p)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	return n, err
}
