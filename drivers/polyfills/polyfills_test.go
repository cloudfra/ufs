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
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
)

const (
	dirPerm  = fs.FileMode(0o750)
	filePerm = fs.FileMode(0o640)
)

// errNoSpace stands in for a write failure that has nothing to do with the
// path, the way a full disk would fail.
var errNoSpace = errors.New("no space left on device")

// store is the in-memory file system behind the fakes below. Its methods are
// unexported so that each fake satisfies exactly the optional interfaces it
// declares and no more, which is what makes the dispatch order testable.
type store struct {
	dirs  map[string]bool
	files map[string][]byte
	perms map[string]fs.FileMode

	mkdirCalls    []string
	mkdirAllCalls []string
	batches       [][]string
	openCalls     int
	closeCalls    int

	// shortWriteOn makes a write of that name report fewer bytes than it was
	// given without returning an error, which io.Writer does not allow but a
	// broken file handle may.
	shortWriteOn string

	// writeErrOn makes a write of that name fail outright.
	writeErrOn string
}

func newStore() *store {
	return &store{
		dirs:  map[string]bool{pathutil.CwdPath: true},
		files: map[string][]byte{},
		perms: map[string]fs.FileMode{},
	}
}

// seedDir and seedFile set up the state an operation starts from. seedDir
// creates the whole chain, as a real file system would already hold it.
func (s *store) seedDir(name string) {
	dir := pathutil.CwdPath
	for _, part := range strings.Split(name, "/") {
		dir = path.Join(dir, part)
		s.dirs[dir] = true
	}
}

// seedFile also holds the permissions a real file system would already give the
// file, which OpenFile must then leave alone.
func (s *store) seedFile(name string, data string) {
	s.seedDir(path.Dir(name))
	s.files[name] = []byte(data)
	s.perms[name] = filePerm
}

func (s *store) open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if s.dirs[name] {
		return &fakeDir{name: name}, nil
	}
	if _, ok := s.files[name]; ok {
		return &fakeHandle{store: s, name: name}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// mkdir creates one level, failing when the parent is missing or the name is
// taken, the way os.Mkdir does.
func (s *store) mkdir(name string, perm fs.FileMode) error {
	s.mkdirCalls = append(s.mkdirCalls, name)
	if !fs.ValidPath(name) || pathutil.IsCwd(name) {
		return &fs.PathError{Op: "mkdir", Path: name, Err: fs.ErrInvalid}
	}
	if s.dirs[name] {
		return nil
	}
	if _, ok := s.files[name]; ok {
		return &fs.PathError{Op: "mkdir", Path: name, Err: fs.ErrExist}
	}
	if !s.dirs[path.Dir(name)] {
		return &fs.PathError{Op: "mkdir", Path: name, Err: fs.ErrNotExist}
	}
	s.dirs[name] = true
	s.perms[name] = perm
	return nil
}

// mkdirAll creates name and every missing level above it, so that a polyfill
// which forgot its own guard cannot pass a test by leaning on this.
func (s *store) mkdirAll(name string, perm fs.FileMode) error {
	s.mkdirAllCalls = append(s.mkdirAllCalls, name)
	if !fs.ValidPath(name) || pathutil.IsCwd(name) {
		return &fs.PathError{Op: "mkdir", Path: name, Err: fs.ErrInvalid}
	}
	dir := pathutil.CwdPath
	for _, part := range strings.Split(name, "/") {
		dir = path.Join(dir, part)
		if _, ok := s.files[dir]; ok {
			return &fs.PathError{Op: "mkdir", Path: dir, Err: fs.ErrExist}
		}
		s.dirs[dir] = true
		s.perms[dir] = perm
	}
	return nil
}

// create opens a writable handle with the flags Create is documented to use.
func (s *store) create(name string) (types.File, error) {
	return s.openFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0)
}

func (s *store) openFile(name string, flag int, perm fs.FileMode) (types.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if s.dirs[name] {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if _, ok := s.files[name]; !ok {
		if flag&os.O_CREATE == 0 {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		if !s.dirs[path.Dir(name)] {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		s.files[name] = nil
		s.perms[name] = perm
	}
	if flag&os.O_TRUNC != 0 {
		s.files[name] = nil
	}
	s.openCalls++
	return &fakeHandle{store: s, name: name, writable: flag&(os.O_WRONLY|os.O_RDWR) != 0}, nil
}

func (s *store) batchCreate(files []types.BatchFile) error {
	names := make([]string, len(files))
	for i, file := range files {
		names[i] = file.Name
	}
	s.batches = append(s.batches, names)
	return nil
}

// createdDirs lists the directories the store holds, excluding the root.
func createdDirs(s *store) []string {
	var dirs []string
	for name := range s.dirs {
		if name != pathutil.CwdPath {
			dirs = append(dirs, name)
		}
	}
	sort.Strings(dirs)
	return dirs
}

// plainFS implements fs.FS and no optional interface.
type plainFS struct{ *store }

func (f plainFS) Open(name string) (fs.File, error) { return f.open(name) }

// mkdirFS implements [types.MkdirFS] and nothing else.
type mkdirFS struct{ *store }

func (f mkdirFS) Open(name string) (fs.File, error)         { return f.open(name) }
func (f mkdirFS) Mkdir(name string, perm fs.FileMode) error { return f.mkdir(name, perm) }

// mkdirAllFS implements [types.MkdirAllFS] and nothing else.
type mkdirAllFS struct{ *store }

func (f mkdirAllFS) Open(name string) (fs.File, error)            { return f.open(name) }
func (f mkdirAllFS) MkdirAll(name string, perm fs.FileMode) error { return f.mkdirAll(name, perm) }

// mkdirBothFS implements [types.MkdirFS] and [types.MkdirAllFS], so the native
// method must win over the emulation.
type mkdirBothFS struct{ *store }

func (f mkdirBothFS) Open(name string) (fs.File, error)            { return f.open(name) }
func (f mkdirBothFS) Mkdir(name string, perm fs.FileMode) error    { return f.mkdir(name, perm) }
func (f mkdirBothFS) MkdirAll(name string, perm fs.FileMode) error { return f.mkdirAll(name, perm) }

// createFS implements [types.CreateFileFS] and nothing else.
type createFS struct{ *store }

func (f createFS) Open(name string) (fs.File, error)      { return f.open(name) }
func (f createFS) Create(name string) (types.File, error) { return f.create(name) }

// openFileFS implements [types.OpenFileFS] and nothing else.
type openFileFS struct{ *store }

func (f openFileFS) Open(name string) (fs.File, error) { return f.open(name) }
func (f openFileFS) OpenFile(name string, flag int, perm fs.FileMode) (types.File, error) {
	return f.openFile(name, flag, perm)
}

// batchFS implements [types.BatchCreateFS] and nothing else. It records the
// batch instead of writing it, so a test can tell the native path from the
// emulation.
type batchFS struct{ *store }

func (f batchFS) Open(name string) (fs.File, error)         { return f.open(name) }
func (f batchFS) BatchCreate(files []types.BatchFile) error { return f.batchCreate(files) }

// brokenFS implements [types.MkdirFS] but cannot be queried: every open fails
// with something other than [fs.ErrNotExist], so a stat of an ancestor reports
// that failure rather than a missing level.
type brokenFS struct{ *store }

func (f brokenFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
}

func (f brokenFS) Mkdir(name string, perm fs.FileMode) error { return f.mkdir(name, perm) }

// openFileMkdirAllFS implements [types.OpenFileFS] and [types.MkdirAllFS], the
// pair that lets an emulated batch place a file in a directory that is not there
// yet.
type openFileMkdirAllFS struct{ *store }

func (f openFileMkdirAllFS) Open(name string) (fs.File, error) { return f.open(name) }

func (f openFileMkdirAllFS) OpenFile(name string, flag int, perm fs.FileMode) (types.File, error) {
	return f.openFile(name, flag, perm)
}

func (f openFileMkdirAllFS) MkdirAll(name string, perm fs.FileMode) error {
	return f.mkdirAll(name, perm)
}

// createMkdirFS implements [types.CreateFileFS] and [types.MkdirFS], so the
// parent of an emulated batch entry is created one level at a time.
type createMkdirFS struct{ *store }

func (f createMkdirFS) Open(name string) (fs.File, error)         { return f.open(name) }
func (f createMkdirFS) Create(name string) (types.File, error)    { return f.create(name) }
func (f createMkdirFS) Mkdir(name string, perm fs.FileMode) error { return f.mkdir(name, perm) }

// fakeDir is the fs.File returned for a directory.
type fakeDir struct{ name string }

func (d *fakeDir) Stat() (fs.FileInfo, error) {
	return fakeInfo{name: path.Base(d.name), mode: fs.ModeDir | dirPerm}, nil
}

func (d *fakeDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: errors.New("is a directory")}
}

func (d *fakeDir) Close() error { return nil }

// fakeHandle is both the fs.File returned by Open and the types.File returned
// by Create and OpenFile.
type fakeHandle struct {
	store    *store
	name     string
	writable bool
	offset   int64
	closed   bool
}

func (h *fakeHandle) Stat() (fs.FileInfo, error) {
	if h.closed {
		return nil, &fs.PathError{Op: "stat", Path: h.name, Err: fs.ErrClosed}
	}
	return fakeInfo{
		name: path.Base(h.name),
		size: int64(len(h.store.files[h.name])),
		mode: h.store.perms[h.name],
	}, nil
}

func (h *fakeHandle) Read(p []byte) (int, error) {
	if h.closed {
		return 0, &fs.PathError{Op: "read", Path: h.name, Err: fs.ErrClosed}
	}
	data := h.store.files[h.name]
	if h.offset >= int64(len(data)) {
		return 0, io.EOF
	}
	n := copy(p, data[h.offset:])
	h.offset += int64(n)
	return n, nil
}

func (h *fakeHandle) Write(p []byte) (int, error) {
	if h.closed {
		return 0, &fs.PathError{Op: "write", Path: h.name, Err: fs.ErrClosed}
	}
	if !h.writable {
		return 0, &fs.PathError{Op: "write", Path: h.name, Err: fs.ErrPermission}
	}
	if h.name == h.store.writeErrOn {
		return 0, &fs.PathError{Op: "write", Path: h.name, Err: errNoSpace}
	}
	if h.name == h.store.shortWriteOn {
		n := len(p) / 2
		h.store.files[h.name] = append(h.store.files[h.name], p[:n]...)
		return n, nil
	}
	h.store.files[h.name] = append(h.store.files[h.name], p...)
	return len(p), nil
}

func (h *fakeHandle) Seek(offset int64, whence int) (int64, error) {
	if h.closed {
		return 0, &fs.PathError{Op: "seek", Path: h.name, Err: fs.ErrClosed}
	}
	switch whence {
	case io.SeekStart:
		h.offset = offset
	case io.SeekCurrent:
		h.offset += offset
	case io.SeekEnd:
		h.offset = int64(len(h.store.files[h.name])) + offset
	default:
		return 0, &fs.PathError{Op: "seek", Path: h.name, Err: fs.ErrInvalid}
	}
	return h.offset, nil
}

func (h *fakeHandle) ReadAt(p []byte, off int64) (int, error) {
	if h.closed {
		return 0, &fs.PathError{Op: "read", Path: h.name, Err: fs.ErrClosed}
	}
	data := h.store.files[h.name]
	if off >= int64(len(data)) {
		return 0, io.EOF
	}
	n := copy(p, data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (h *fakeHandle) WriteString(s string) (int, error) { return h.Write([]byte(s)) }

func (h *fakeHandle) Close() error {
	if h.closed {
		return &fs.PathError{Op: "close", Path: h.name, Err: fs.ErrClosed}
	}
	h.closed = true
	h.store.closeCalls++
	return nil
}

// fakeInfo is the fs.FileInfo the fakes report.
type fakeInfo struct {
	name string
	size int64
	mode fs.FileMode
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return i.size }
func (i fakeInfo) Mode() fs.FileMode  { return i.mode }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return i.mode.IsDir() }
func (i fakeInfo) Sys() any           { return nil }
