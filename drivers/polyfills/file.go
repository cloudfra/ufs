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
	_ types.File     = (*polyfillFile)(nil)
	_ fs.ReadDirFile = (*polyfillDir)(nil)
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

// wrapFile returns pf, or a [polyfillDir] around it when its file can list a
// directory, so that the wrapper does not hide fs.ReadDirFile.
func wrapFile(pf *polyfillFile) types.File {
	if _, ok := pf.f.(fs.ReadDirFile); ok {
		return &polyfillDir{polyfillFile: pf}
	}
	return pf
}

// polyfillFile emulates the [types.File] methods that its [fs.File] lacks. mu
// keeps a ReadAt done with seeks from interleaving with a Read or Seek, which
// would move the position it restores.
type polyfillFile struct {
	mu sync.Mutex
	f  fs.File
}

func (f *polyfillFile) Stat() (fs.FileInfo, error) {
	return f.f.Stat()
}

func (f *polyfillFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	n, err := f.f.Read(p)
	f.mu.Unlock()
	return n, err
}

func (f *polyfillFile) Close() error {
	return f.f.Close()
}

func (f *polyfillFile) Write(p []byte) (int, error) {
	if cf, ok := f.f.(io.Writer); ok {
		return cf.Write(p)
	}
	return 0, ufserrors.NewPathError("write", f.errName(), fs.ErrPermission)
}

func (f *polyfillFile) WriteString(s string) (int, error) {
	if cf, ok := f.f.(io.StringWriter); ok {
		return cf.WriteString(s)
	}
	return f.Write([]byte(s))
}

func (f *polyfillFile) Seek(offset int64, whence int) (int64, error) {
	cf, ok := f.f.(io.Seeker)
	if !ok {
		return 0, ufserrors.NewPathError("seek", f.errName(), errors.ErrUnsupported)
	}
	f.mu.Lock()
	pos, err := cf.Seek(offset, whence)
	f.mu.Unlock()
	return pos, err
}

func (f *polyfillFile) ReadAt(p []byte, off int64) (int, error) {
	if cf, ok := f.f.(io.ReaderAt); ok {
		return cf.ReadAt(p, off)
	}
	cf, ok := f.f.(io.Seeker)
	if !ok {
		return 0, ufserrors.NewPathError("read", f.errName(), errors.ErrUnsupported)
	}
	f.mu.Lock()
	n, err := seekReadAt(cf, f.f, p, off)
	f.mu.Unlock()
	return n, err
}

// errName is the name reported in an error. A file given to [ToFile] has no
// name of its own, so it asks the file for one.
func (f *polyfillFile) errName() string {
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
