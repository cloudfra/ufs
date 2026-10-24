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

package polyfills

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"sync"
	"time"

	"github.com/cloudfra/ufs/drivers/common/buffile"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// BufferMode chooses where an emulated file keeps the copy of its content that
// [OpenFileBuffered] edits, or reads from when the file system's own file
// cannot seek.
type BufferMode int

const (
	// BufferAuto keeps files up to [MemoryBufferLimit] bytes in memory and
	// larger ones in a temporary local file.
	BufferAuto BufferMode = iota
	// BufferMemory keeps the content in memory.
	BufferMemory
	// BufferTempFile keeps the content in a temporary file in the default
	// temporary directory, removed when the file is closed.
	BufferTempFile
)

// MemoryBufferLimit is the largest file, in bytes, that [BufferAuto] keeps in
// memory.
const MemoryBufferLimit = 4 << 20

var _ types.File = (*bufferedFile)(nil)

// bufferedFile is a [types.File] over a [buffile.File] copy of a file's
// content, limited to the access the file was opened with. The file system
// never sees a write until Close, which replaces the file with the whole copy
// through create if anything was written.
type bufferedFile struct {
	buffile.File

	// mu guards closed and makes an append's seek to the end and its write
	// one step, so that no other write or seek on the handle lands between.
	mu       sync.Mutex
	readable bool
	writable bool
	append   bool
	closed   bool
	create   func() (types.File, error)
}

// loadBufferedFile reads all of src into storage chosen by mode and closes src.
// The result can neither read nor write until the caller says which it may.
func loadBufferedFile(name string, src fs.File, mode BufferMode) (_ *bufferedFile, err error) {
	defer func() {
		err = ufserrors.Join(err, src.Close())
	}()
	info, err := src.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, ufserrors.NewPathError("open", name, fmt.Errorf("%q is a directory, %w", name, fs.ErrInvalid))
	}
	inMemory, err := keepInMemory(mode, info.Size())
	if err != nil {
		return nil, err
	}
	if !inMemory {
		f := &bufferedFile{}
		if f.File, err = buffile.NewTempFile(name, src, info.Mode(), info.ModTime()); err != nil {
			return nil, err
		}
		return f, nil
	}
	var content bytes.Buffer
	if size := info.Size(); size > 0 && size <= MemoryBufferLimit {
		// Room for the whole file and the free space ReadFrom asks for at
		// the end, so that reading it never grows the buffer again.
		content.Grow(int(size) + bytes.MinRead)
	}
	if _, err := content.ReadFrom(src); err != nil {
		return nil, err
	}
	return &bufferedFile{File: buffile.New(name, content.Bytes(), info.Mode(), info.ModTime())}, nil
}

// keepInMemory reports whether mode keeps content of size bytes in memory.
func keepInMemory(mode BufferMode, size int64) (bool, error) {
	switch mode {
	case BufferAuto:
		return size <= MemoryBufferLimit, nil
	case BufferMemory:
		return true, nil
	case BufferTempFile:
		return false, nil
	default:
		return false, fmt.Errorf("unknown buffer mode %d, %w", mode, fs.ErrInvalid)
	}
}

func (f *bufferedFile) Stat() (fs.FileInfo, error) {
	f.mu.Lock()
	if err := f.check("stat", true); err != nil {
		f.mu.Unlock()
		return nil, err
	}
	info, err := f.File.Stat()
	f.mu.Unlock()
	return info, err
}

func (f *bufferedFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	if err := f.check("read", f.readable); err != nil {
		f.mu.Unlock()
		return 0, err
	}
	n, err := f.File.Read(p)
	f.mu.Unlock()
	return n, err
}

func (f *bufferedFile) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	if err := f.check("read", f.readable); err != nil {
		f.mu.Unlock()
		return 0, err
	}
	n, err := f.File.ReadAt(p, off)
	f.mu.Unlock()
	return n, err
}

func (f *bufferedFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	if err := f.prepareWrite(); err != nil {
		f.mu.Unlock()
		return 0, err
	}
	n, err := f.File.Write(p)
	f.mu.Unlock()
	return n, err
}

func (f *bufferedFile) WriteString(s string) (int, error) {
	f.mu.Lock()
	if err := f.prepareWrite(); err != nil {
		f.mu.Unlock()
		return 0, err
	}
	n, err := f.File.WriteString(s)
	f.mu.Unlock()
	return n, err
}

func (f *bufferedFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	if err := f.check("seek", true); err != nil {
		f.mu.Unlock()
		return 0, err
	}
	pos, err := f.File.Seek(offset, whence)
	f.mu.Unlock()
	return pos, err
}

// Close writes the content back if it changed and releases the copy either way.
func (f *bufferedFile) Close() error {
	f.mu.Lock()
	if err := f.check("close", true); err != nil {
		f.mu.Unlock()
		return err
	}
	f.closed = true
	var err error
	if f.create != nil && f.Dirty() {
		err = f.writeBack()
	}
	err = ufserrors.Join(err, f.Release())
	f.mu.Unlock()
	return err
}

// writeBack replaces the file with the whole copy. f.mu must be held.
func (f *bufferedFile) writeBack() error {
	dst, err := f.create()
	if err != nil {
		return err
	}
	_, err = f.FlushTo(dst, time.Now())
	return ufserrors.Join(err, dst.Close())
}

// prepareWrite checks that f may be written and, for an append, moves to the
// end. f.mu must be held.
func (f *bufferedFile) prepareWrite() error {
	if err := f.check("write", f.writable); err != nil {
		return err
	}
	if f.append {
		if _, err := f.File.Seek(0, io.SeekEnd); err != nil {
			return err
		}
	}
	return nil
}

// check fails an operation on a closed file or one the open flags did not
// allow. f.mu must be held.
func (f *bufferedFile) check(op string, allowed bool) error {
	if f.closed {
		return ufserrors.NewPathError(op, f.Path(), fs.ErrClosed)
	}
	if !allowed {
		return ufserrors.NewPathError(op, f.Path(), fs.ErrPermission)
	}
	return nil
}
