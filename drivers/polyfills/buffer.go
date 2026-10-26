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
	"os"
	"time"

	"github.com/cloudfra/ufs/drivers/common/buffile"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// buffer is the content a [bufferedFile] reads through once the polyfill takes
// over the file's position, so Read, Seek and ReadAt share one position.
type buffer interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
}

// fileStat caches what a file's Stat reported.
type fileStat struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
}

// statFile reads f's attributes. size is -1 when f reports no usable length.
func statFile(f fs.File) fileStat {
	info, err := f.Stat()
	if err != nil || info == nil {
		return fileStat{size: -1}
	}
	size := info.Size()
	if size < 0 {
		size = -1
	}
	return fileStat{name: info.Name(), size: size, mode: info.Mode(), modTime: info.ModTime()}
}

// newBuffer copies the content of f into the store args selects. On failure f is
// left unclosed; the caller closes it along with the error.
func newBuffer(f fs.File, args BufferArgs) (buffer, error) {
	switch args.Mode {
	case BufferDisk:
		return newDiskBuffer("", f)
	case BufferAuto:
		// A length f does not report is treated as large, so an unknown length
		// never decides to hold the whole content in memory.
		if stat := statFile(f); stat.size >= 0 && stat.size < args.threshold() {
			return newMemoryBuffer(f, stat)
		}
		return newDiskBuffer("", f)
	default:
		stat := statFile(f)
		return newMemoryBuffer(f, stat)
	}
}

// memoryBuffer keeps a copy of the content in memory, sharing the in-memory
// file state the fully-buffered drivers use.
type memoryBuffer struct {
	buffile.File
}

// newMemoryBuffer reads all of f into a memoryBuffer, reusing the attributes the
// caller already read.
func newMemoryBuffer(f fs.File, stat fileStat) (*memoryBuffer, error) {
	content, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return &memoryBuffer{File: buffile.New(stat.name, content, stat.mode, stat.modTime)}, nil
}

// Close discards the content; an in-memory copy has no handle or name to
// release.
func (b *memoryBuffer) Close() error {
	return nil
}

// diskBuffer keeps a copy of the content in a temporary file, using the
// operating system's own Read, Seek and ReadAt.
type diskBuffer struct {
	f *os.File
}

// newDiskBuffer streams all of f into a temporary file in dir (the default
// temporary directory when dir is empty) and rewinds it. A failed copy or rewind
// leaves no temporary file behind.
func newDiskBuffer(dir string, f fs.File) (*diskBuffer, error) {
	tmp, err := osutil.CreateTemp(dir, "ufs-polyfill-*.tmp")
	if err != nil {
		return nil, err
	}
	buf := &diskBuffer{f: tmp}
	if _, err := io.Copy(tmp, f); err != nil {
		return nil, ufserrors.Join(err, buf.Close())
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, ufserrors.Join(err, buf.Close())
	}
	return buf, nil
}

func (b *diskBuffer) Read(p []byte) (int, error) {
	return b.f.Read(p)
}

func (b *diskBuffer) ReadAt(p []byte, off int64) (int, error) {
	return b.f.ReadAt(p, off)
}

func (b *diskBuffer) Seek(off int64, whence int) (int64, error) {
	return b.f.Seek(off, whence)
}

// Close closes the temporary file and removes it. The handle is closed first
// because an open handle is what makes removal fail on Windows, and removal is
// attempted even when closing reports an error so a partial copy cannot leak.
func (b *diskBuffer) Close() error {
	name := b.f.Name()
	return ufserrors.Join(b.f.Close(), osutil.Remove(name))
}

// sectionBuffer gives a file that can only read at an offset a position of its
// own without copying: [io.SectionReader] serves Read, Seek and ReadAt through
// the underlying [io.ReaderAt], bounded by the length Stat reported.
type sectionBuffer struct {
	*io.SectionReader
}

func newSectionBuffer(readerAt io.ReaderAt, size int64) *sectionBuffer {
	return &sectionBuffer{SectionReader: io.NewSectionReader(readerAt, 0, size)}
}

// Close is a no-op: the view does not own the content it reads through.
func (b *sectionBuffer) Close() error {
	return nil
}
