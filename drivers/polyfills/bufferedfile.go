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
	"io"
	"io/fs"
	"log/slog"
	"os"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var _ types.File = (*bufferedFile)(nil)

// BufferMode selects how the content of a buffered file is kept.
type BufferMode int

const (
	// BufferMemory keeps the content in memory. It is the zero value.
	BufferMemory BufferMode = iota
	// BufferDisk keeps the content in a temporary file, removed on Close.
	BufferDisk
)

// ToBufferedFile adapts f to [types.File], buffering content when that is the
// only way to get a working Seek and ReadAt. A file that already is a
// [types.File] is returned as is.
//
// When Seek or ReadAt is absent from a file that is open only for reading, the
// file content is buffered so that both operations work correctly at any
// position. In memory mode, content is read eagerly into a bytes.Reader. In
// disk mode, content is streamed to a temporary file. The buffer is released on
// Close. Read is also redirected through the buffer so that the file position
// stays consistent across Read/Seek/ReadAt.
//
// A file that is open for writing is never buffered: the wrapper writes through
// to the underlying file, so a buffer would report content that predates those
// writes. A missing Seek or ReadAt on such a file reports [fs.ErrInvalid]
// instead. See https://github.com/cloudfra/ufs/issues/451.
//
// When readOnly is true, Write and WriteString always report [fs.ErrInvalid].
// Unlike [ToFile], which guesses an adaptation from the interfaces f happens to
// implement, this one needs the caller's read or write intent to decide whether
// buffering is allowed, so it is a separate entry point.
func ToBufferedFile(f fs.File, readOnly bool, mode BufferMode) (types.File, error) {
	if cFile, ok := f.(types.File); ok {
		return cFile, nil
	}

	bf := &bufferedFile{File: f}
	if err := polyfillSeekReadAt(bf, f, !readOnly, mode); err != nil {
		return nil, err
	}

	if readOnly {
		bf.writeFunc = func(_ []byte) (int, error) {
			return 0, fs.ErrInvalid
		}
		bf.writeStringFunc = func(_ string) (int, error) {
			return 0, fs.ErrInvalid
		}
		return bf, nil
	}

	if w, ok := f.(io.Writer); ok {
		bf.writeFunc = w.Write
	} else {
		bf.writeFunc = func(_ []byte) (int, error) {
			return 0, fs.ErrInvalid
		}
	}
	if sw, ok := f.(io.StringWriter); ok {
		bf.writeStringFunc = sw.WriteString
	} else if _, ok := f.(io.Writer); ok {
		bf.writeStringFunc = func(s string) (int, error) {
			return bf.writeFunc([]byte(s))
		}
	} else {
		bf.writeStringFunc = func(_ string) (int, error) {
			return 0, fs.ErrInvalid
		}
	}
	return bf, nil
}

// bufferedFile is the [types.File] returned by [ToBufferedFile]. It emulates
// the methods its [fs.File] lacks, redirecting Read through the buffer when
// there is one.
type bufferedFile struct {
	fs.File
	// closed records that Close has run. Close releases the buffer and the
	// closures bound to it, so every method must then report [fs.ErrClosed].
	closed          bool
	buf             *bytes.Reader // non-nil when content is buffered in memory
	tmpFile         *os.File      // non-nil when content is buffered on disk
	writeFunc       func([]byte) (int, error)
	seekFunc        func(int64, int) (int64, error)
	readAtFunc      func([]byte, int64) (int, error)
	writeStringFunc func(string) (int, error)
}

func (f *bufferedFile) Stat() (fs.FileInfo, error) {
	if f.closed {
		return nil, fs.ErrClosed
	}
	return f.File.Stat()
}

func (f *bufferedFile) Read(p []byte) (int, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	if f.buf != nil {
		return f.buf.Read(p)
	}
	if f.tmpFile != nil {
		return f.tmpFile.Read(p)
	}
	return f.File.Read(p)
}

// Close releases the buffered content along with the closures that reached it,
// so that no method can read content that was dropped. A second Close reports
// [fs.ErrClosed], like [os.File.Close].
func (f *bufferedFile) Close() error {
	if f.closed {
		return fs.ErrClosed
	}
	f.closed = true
	f.buf = nil
	f.seekFunc = nil
	f.readAtFunc = nil
	if f.tmpFile != nil {
		name := f.tmpFile.Name()
		if err := f.tmpFile.Close(); err != nil {
			f.tmpFile = nil
			return ufserrors.Join(err, f.File.Close())
		}
		if err := osutil.Remove(name); err != nil {
			f.tmpFile = nil
			return ufserrors.Join(err, f.File.Close())
		}
		f.tmpFile = nil
	}
	return f.File.Close()
}

func (f *bufferedFile) Write(p []byte) (int, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	return f.writeFunc(p)
}

func (f *bufferedFile) Seek(off int64, whence int) (int64, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	return f.seekFunc(off, whence)
}

func (f *bufferedFile) ReadAt(p []byte, off int64) (int, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	return f.readAtFunc(p, off)
}

func (f *bufferedFile) WriteString(s string) (int, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	return f.writeStringFunc(s)
}

// polyfillSeekReadAt populates bf.seekFunc and bf.readAtFunc for the underlying
// file f. A method f provides natively is used directly. When f is missing one
// of them, a file that is open only for reading has its content buffered so
// that both work at any position, using in-memory (BufferMemory) or temp-file
// (BufferDisk) buffering; on failure f is closed and the error returned. A file
// open for writing (writable) is not buffered, so a missing method reports
// [fs.ErrInvalid].
func polyfillSeekReadAt(bf *bufferedFile, f fs.File, writable bool, mode BufferMode) error {
	if seeker, ok := f.(io.Seeker); ok {
		bf.seekFunc = seeker.Seek
	}
	if readerAt, ok := f.(io.ReaderAt); ok {
		bf.readAtFunc = readerAt.ReadAt
	}
	if bf.seekFunc == nil || bf.readAtFunc == nil {
		if writable {
			if bf.seekFunc == nil {
				bf.seekFunc = func(int64, int) (int64, error) {
					return 0, fs.ErrInvalid
				}
			}
			if bf.readAtFunc == nil {
				bf.readAtFunc = func([]byte, int64) (int, error) {
					return 0, fs.ErrInvalid
				}
			}
			return nil
		}
		if mode == BufferDisk {
			return polyfillSeekReadAtDisk(bf, f)
		}
		return polyfillSeekReadAtMemory(bf, f)
	}
	return nil
}

func polyfillSeekReadAtMemory(bf *bufferedFile, f fs.File) error {
	data, err := io.ReadAll(f)
	if err != nil {
		if closeErr := f.Close(); closeErr != nil {
			slog.Warn("failed to close file after ReadAll error", "error", closeErr)
		}
		return err
	}
	bf.buf = bytes.NewReader(data)
	bf.seekFunc = bf.buf.Seek
	bf.readAtFunc = bf.buf.ReadAt
	return nil
}

func polyfillSeekReadAtDisk(bf *bufferedFile, f fs.File) error {
	tmp, err := osutil.CreateTemp("", "ufs-polyfill-*.tmp")
	if err != nil {
		fCloseErr := f.Close()
		return ufserrors.Join(err, fCloseErr)
	}
	if _, err := io.Copy(tmp, f); err != nil {
		closeErr := tmp.Close()
		removeErr := osutil.Remove(tmp.Name())
		fCloseErr := f.Close()
		return ufserrors.Join(err, closeErr, removeErr, fCloseErr)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		closeErr := tmp.Close()
		removeErr := osutil.Remove(tmp.Name())
		fCloseErr := f.Close()
		return ufserrors.Join(err, closeErr, removeErr, fCloseErr)
	}
	bf.tmpFile = tmp
	bf.seekFunc = tmp.Seek
	bf.readAtFunc = tmp.ReadAt
	return nil
}
