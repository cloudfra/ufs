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
	"errors"
	"io"
	"io/fs"
	"sync"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var (
	_ types.File      = (*polyfillFile)(nil)
	_ fs.ReadDirFile  = (*polyfillDir)(nil)
	_ types.File      = (*accessFile)(nil)
	_ io.StringWriter = (*accessFile)(nil)
)

// ToFile adapts f to [types.File]. A file that already is one is returned as
// is. Otherwise each method uses what f implements: writes go to an
// [io.Writer], seeks to an [io.Seeker], and ReadAt to an [io.ReaderAt] or, when
// f can only seek, to a seek, read and seek back. A method f has no means to
// perform returns an [fs.PathError], wrapping [fs.ErrPermission] for a write
// and [errors.ErrUnsupported] otherwise. A directory keeps its ReadDir.
func ToFile(f fs.File) types.File {
	if cFile, ok := f.(types.File); ok {
		return cFile
	}
	return wrapFile(&polyfillFile{f: f})
}

// toReadOnlyFile adapts f, which fsys.Open(name) returned, to [types.File] for a
// read-only OpenFile. Unlike [ToFile], it never writes, and a seek or ReadAt
// that f cannot perform opens name again and buffers its content in mode, so
// that a plain sequential read never pays for the copy.
func toReadOnlyFile(fsys fs.FS, name string, f fs.File, mode BufferMode) types.File {
	if cFile, ok := f.(types.File); ok {
		return cFile
	}
	return wrapFile(&polyfillFile{
		f:        f,
		name:     name,
		readOnly: true,
		reopen: func() (fs.File, error) {
			return fsys.Open(name)
		},
		mode: mode,
	})
}

// wrapFile returns pf, or a [polyfillDir] around it when its file can list a
// directory, so that the wrapper does not hide fs.ReadDirFile.
func wrapFile(pf *polyfillFile) types.File {
	if _, ok := pf.f.(fs.ReadDirFile); ok {
		return &polyfillDir{polyfillFile: pf}
	}
	return pf
}

// polyfillFile emulates the [types.File] methods that its [fs.File] lacks. When
// reopen is set, a seek or ReadAt the file cannot do switches it to a read-only
// [bufferedFile] holding the whole content, positioned at the bytes read so
// far; buf is then the file and f is closed.
type polyfillFile struct {
	mu       sync.Mutex
	f        fs.File
	name     string
	readOnly bool
	reopen   func() (fs.File, error)
	mode     BufferMode
	pos      int64
	buf      *bufferedFile
}

func (f *polyfillFile) Stat() (fs.FileInfo, error) {
	f.mu.Lock()
	var info fs.FileInfo
	var err error
	if f.buf != nil {
		info, err = f.buf.Stat()
	} else {
		info, err = f.f.Stat()
	}
	f.mu.Unlock()
	return info, err
}

func (f *polyfillFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	var n int
	var err error
	if f.buf != nil {
		n, err = f.buf.Read(p)
	} else {
		n, err = f.f.Read(p)
		f.pos += int64(n)
	}
	f.mu.Unlock()
	return n, err
}

func (f *polyfillFile) Close() error {
	f.mu.Lock()
	var err error
	if f.buf != nil {
		err = f.buf.Close()
	} else {
		err = f.f.Close()
	}
	f.mu.Unlock()
	return err
}

func (f *polyfillFile) Write(p []byte) (int, error) {
	if cf, ok := f.f.(io.Writer); ok && !f.readOnly {
		return cf.Write(p)
	}
	return 0, ufserrors.NewPathError("write", f.errName(), fs.ErrPermission)
}

func (f *polyfillFile) WriteString(s string) (int, error) {
	if cf, ok := f.f.(io.StringWriter); ok && !f.readOnly {
		return cf.WriteString(s)
	}
	return f.Write([]byte(s))
}

func (f *polyfillFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	pos, err := f.seekLocked(offset, whence)
	f.mu.Unlock()
	return pos, err
}

// seekLocked is Seek with f.mu held.
func (f *polyfillFile) seekLocked(offset int64, whence int) (int64, error) {
	if f.buf == nil {
		if cf, ok := f.f.(io.Seeker); ok {
			return cf.Seek(offset, whence)
		}
		if err := f.materialize("seek"); err != nil {
			return 0, err
		}
	}
	return f.buf.Seek(offset, whence)
}

func (f *polyfillFile) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	n, err := f.readAtLocked(p, off)
	f.mu.Unlock()
	return n, err
}

// readAtLocked is ReadAt with f.mu held.
func (f *polyfillFile) readAtLocked(p []byte, off int64) (int, error) {
	if f.buf == nil {
		if cf, ok := f.f.(io.ReaderAt); ok {
			return cf.ReadAt(p, off)
		}
		if cf, ok := f.f.(io.Seeker); ok {
			return seekReadAt(cf, f.f, p, off)
		}
		if err := f.materialize("read"); err != nil {
			return 0, err
		}
	}
	return f.buf.ReadAt(p, off)
}

// materialize switches f to a read-only buffered copy of its content, keeping
// the read position. It fails with [errors.ErrUnsupported] when f has no way
// to open its content again, as for a file given to [ToFile].
func (f *polyfillFile) materialize(op string) error {
	if f.reopen == nil {
		return ufserrors.NewPathError(op, f.errName(), errors.ErrUnsupported)
	}
	src, err := f.reopen()
	if err != nil {
		return err
	}
	buf, err := loadBufferedFile(f.name, src, f.mode)
	if err != nil {
		return err
	}
	buf.readable = true
	if _, err := buf.Seek(f.pos, io.SeekStart); err != nil {
		return ufserrors.Join(err, buf.Close())
	}
	if err := f.f.Close(); err != nil {
		return ufserrors.Join(err, buf.Close())
	}
	f.buf = buf
	return nil
}

// errName is the name reported in an error. A file given to [ToFile] has no
// name of its own, so it asks the file for one.
func (f *polyfillFile) errName() string {
	if f.name != "" {
		return f.name
	}
	if info, err := f.f.Stat(); err == nil {
		return info.Name()
	}
	return ""
}

// seekReadAt reads len(p) bytes at off with a seek, a full read and a seek back
// to the offset s had, so that, as [io.ReaderAt] requires, the read position is
// unchanged afterwards.
func seekReadAt(s io.Seeker, r io.Reader, p []byte, off int64) (int, error) {
	cur, err := s.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	var n int
	if _, err = s.Seek(off, io.SeekStart); err == nil {
		n, err = io.ReadFull(r, p)
		if errors.Is(err, io.ErrUnexpectedEOF) {
			err = io.EOF
		}
	}
	if _, serr := s.Seek(cur, io.SeekStart); serr != nil {
		err = ufserrors.Join(err, serr)
	}
	return n, err
}

// polyfillDir is a [polyfillFile] whose file is a directory that can be listed.
type polyfillDir struct {
	*polyfillFile
}

func (d *polyfillDir) ReadDir(n int) ([]fs.DirEntry, error) {
	return d.f.(fs.ReadDirFile).ReadDir(n)
}

// accessFile narrows a writable [types.File] from Create to the access an
// OpenFile asked for: reads fail unless it was opened read-write, and with
// append every write first moves to the end of the file.
type accessFile struct {
	types.File
	name     string
	readable bool
	append   bool
}

func (f *accessFile) Read(p []byte) (int, error) {
	if !f.readable {
		return 0, ufserrors.NewPathError("read", f.name, fs.ErrPermission)
	}
	return f.File.Read(p)
}

func (f *accessFile) ReadAt(p []byte, off int64) (int, error) {
	if !f.readable {
		return 0, ufserrors.NewPathError("read", f.name, fs.ErrPermission)
	}
	return f.File.ReadAt(p, off)
}

func (f *accessFile) Write(p []byte) (int, error) {
	if f.append {
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			return 0, err
		}
	}
	return f.File.Write(p)
}

func (f *accessFile) WriteString(s string) (int, error) {
	if f.append {
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			return 0, err
		}
	}
	return f.File.WriteString(s)
}
