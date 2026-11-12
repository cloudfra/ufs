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

// fileStat holds the attributes read from a file being wrapped. Its size decides
// where auto mode puts the content; the rest is what the shared in-memory file
// needs to describe a memory copy, even though the polyfill reports the wrapped
// file through Stat. size is -1 when the file reports no usable length.
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

// hasContentBeyond reports whether readerAt still holds bytes at or past size,
// which is what a view bounded by size would hide.
func hasContentBeyond(readerAt io.ReaderAt, size int64) bool {
	var probe [1]byte
	// Only the byte count counts: a file that ends at size reports io.EOF with
	// no bytes, and any other error there says nothing about its length.
	n, _ := readerAt.ReadAt(probe[:], size) //nolint:errcheck // see above
	return n > 0
}

// newBuffer copies the content of f into the store args selects. On failure f is
// left unclosed; the caller closes it along with the error.
func newBuffer(f fs.File, args BufferArgs) (buffer, error) {
	switch args.Mode {
	case BufferDisk:
		return newDiskBuffer("", f)
	case BufferAuto:
		return newAutoBuffer(f, statFile(f), args.threshold())
	default:
		stat := statFile(f)
		content, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		return newMemoryBuffer(stat, content), nil
	}
}

// newAutoBuffer picks the store [BufferAuto] uses: content that fits under limit
// stays in memory and content that reaches it goes to a temporary file.
//
// The length Stat reports is used as a hint and never as a claim the result
// relies on: a file at or above limit is moved unread, while any other file is
// read with the read capped at limit, so content longer than its Stat admitted
// spills instead of filling memory. Spilling that way transiently costs limit
// bytes of memory on top of the temporary copy, because the bytes already read
// are handed to the temporary file without being copied twice.
func newAutoBuffer(f fs.File, stat fileStat, limit int64) (buffer, error) {
	if stat.size >= limit {
		return newDiskBuffer("", f)
	}
	content, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) < limit {
		return newMemoryBuffer(stat, content), nil
	}
	return newDiskBuffer("", io.MultiReader(bytes.NewReader(content), f))
}

// memoryBuffer keeps a copy of the content in memory, reusing the in-memory file
// state the fully-buffered drivers use so Seek, ReadAt, Read and Stat come from
// one implementation.
type memoryBuffer struct {
	buffile.File
}

// newMemoryBuffer returns a memoryBuffer holding content, under the attributes
// the caller already read.
func newMemoryBuffer(stat fileStat, content []byte) *memoryBuffer {
	return &memoryBuffer{File: buffile.New(stat.name, content, stat.mode, stat.modTime)}
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
// the underlying [io.ReaderAt], bounded by a length that was checked not to hide
// content the file still has.
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
