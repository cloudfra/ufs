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
	"io"
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
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

// ToBufferedFile adapts f to [types.File], giving it a working Seek and ReadAt.
// A file that already is a [types.File] is returned as is.
//
// What the file lacks is derived from what it provides, so its content is
// copied only when it has neither Seek nor ReadAt, into the store mode selects.
// A file open for writing is never copied because the wrapper writes through to
// it; a method that cannot be derived reports [fs.ErrInvalid]. See
// https://github.com/cloudfra/ufs/issues/451.
//
// A ReadAt ending exactly at the end of the content reports [io.EOF] from a
// memory copy and a nil error from a temporary file; [io.ReaderAt] allows
// either. A temporary file is created in the operating system's temporary
// directory and is removed only by Close. Like the file it wraps, the result is
// not safe for concurrent use.
//
// When readOnly is true, Write and WriteString always report [fs.ErrInvalid].
// Unlike [ToFile], which guesses from the interfaces f happens to implement,
// this one needs the caller's read or write intent to decide whether copying
// the content is allowed.
func ToBufferedFile(f fs.File, readOnly bool, mode BufferMode) (types.File, error) {
	if cFile, ok := f.(types.File); ok {
		return cFile, nil
	}

	bf := &bufferedFile{File: f}
	if err := bf.polyfillSeekReadAt(!readOnly, mode); err != nil {
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

// bufferedFile is the [types.File] returned by [ToBufferedFile]. It emulates the
// methods its [fs.File] lacks, reading through content whenever the polyfill
// took over the file's position.
type bufferedFile struct {
	fs.File
	// closed records that Close has run, after which every method reports
	// [fs.ErrClosed].
	closed bool
	// content is what Read, Seek and ReadAt operate on when the file itself
	// could not provide that pair. It is nil when the underlying file serves
	// those methods itself.
	content         buffer
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
	if f.content != nil {
		return f.content.Read(p)
	}
	return f.File.Read(p)
}

// Close releases the buffered content along with the closures that reached it.
// A second Close reports [fs.ErrClosed], like [os.File.Close].
func (f *bufferedFile) Close() error {
	if f.closed {
		return fs.ErrClosed
	}
	f.closed = true
	content := f.content
	f.content = nil
	f.seekFunc = nil
	f.readAtFunc = nil
	var contentErr error
	if content != nil {
		contentErr = content.Close()
	}
	return ufserrors.Join(contentErr, f.File.Close())
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

// polyfillSeekReadAt populates f.seekFunc, f.readAtFunc and, when the file
// position has to be tracked for them, f.content. What the file provides is
// used directly and what it lacks is derived from it, which is how content
// avoids being copied.
func (f *bufferedFile) polyfillSeekReadAt(writable bool, mode BufferMode) error {
	seeker, hasSeek := f.File.(io.Seeker)
	readerAt, hasReadAt := f.File.(io.ReaderAt)
	switch {
	case hasSeek && hasReadAt:
		f.seekFunc = seeker.Seek
		f.readAtFunc = readerAt.ReadAt
		return nil
	case hasSeek:
		f.seekFunc = seeker.Seek
		f.readAtFunc = func(p []byte, off int64) (int, error) {
			return seekReadAt(seeker, f.File, p, off)
		}
		return nil
	case hasReadAt:
		// A file open for writing gets no position of its own: the wrapper
		// writes through the file, so a view bounded by the size Stat reported
		// could hide those writes. Reading at an offset still reaches the file
		// directly, and Seek is left to copyContent, which refuses it.
		if writable {
			f.readAtFunc = readerAt.ReadAt
			break
		}
		// Tracking a position needs a length to seek relative to, so a file
		// that reports none has to fall back to a copy of its content.
		if size := fileSize(f.File); size >= 0 {
			content := newSectionBuffer(readerAt, size)
			f.content = content
			f.seekFunc = content.Seek
			f.readAtFunc = content.ReadAt
			return nil
		}
	}
	return f.copyContent(writable, mode)
}

// copyContent gives f a private copy of its file's content so that Seek and
// ReadAt work at any position, in the store mode selects. A file open for
// writing is not copied at all; see https://github.com/cloudfra/ufs/issues/451.
func (f *bufferedFile) copyContent(writable bool, mode BufferMode) error {
	if writable {
		if f.seekFunc == nil {
			f.seekFunc = func(int64, int) (int64, error) {
				return 0, fs.ErrInvalid
			}
		}
		if f.readAtFunc == nil {
			f.readAtFunc = func([]byte, int64) (int, error) {
				return 0, fs.ErrInvalid
			}
		}
		return nil
	}
	content, err := newBuffer(f.File, mode)
	if err != nil {
		return ufserrors.Join(err, f.File.Close())
	}
	f.content = content
	f.seekFunc = content.Seek
	f.readAtFunc = content.ReadAt
	return nil
}
