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
	"os"

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

// fileSize returns the length f's Stat reported, or -1 when f reports no usable
// length.
func fileSize(f fs.File) int64 {
	info, err := f.Stat()
	if err != nil || info == nil {
		return -1
	}
	if size := info.Size(); size >= 0 {
		return size
	}
	return -1
}

// newBuffer copies the content of f into the store mode selects. On failure f is
// left unclosed; the caller closes it along with the error.
func newBuffer(f fs.File, mode BufferMode) (buffer, error) {
	if mode == BufferDisk {
		return newDiskBuffer("", f)
	}
	content, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return newMemoryBuffer(content), nil
}

// memoryBuffer keeps a copy of the content in memory.
type memoryBuffer struct {
	*bytes.Reader
}

// newMemoryBuffer returns a memoryBuffer holding content.
func newMemoryBuffer(content []byte) *memoryBuffer {
	return &memoryBuffer{Reader: bytes.NewReader(content)}
}

// Close is a no-op: an in-memory copy has no handle or name to release.
func (b *memoryBuffer) Close() error {
	return nil
}

// diskBuffer keeps a copy of the content in a temporary file, using the
// operating system's own Read, Seek and ReadAt.
type diskBuffer struct {
	f *os.File
}

// newDiskBuffer streams src into a temporary file in dir (the default temporary
// directory, os.TempDir, when dir is empty) and rewinds it. A failed copy or
// rewind leaves no temporary file behind.
func newDiskBuffer(dir string, src io.Reader) (*diskBuffer, error) {
	tmp, err := osutil.CreateTemp(dir, "ufs-polyfill-*.tmp")
	if err != nil {
		return nil, err
	}
	buf := &diskBuffer{f: tmp}
	if _, err := io.Copy(tmp, src); err != nil {
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
