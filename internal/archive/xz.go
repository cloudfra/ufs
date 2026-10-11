// Copyright 2026 Jeremy Edwards
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

package archive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"

	"github.com/mholt/archives"
	fastxz "github.com/mikelolasagasti/xz"
)

// xzDictMax is the largest LZMA2 dictionary an xz archive may declare and
// still be opened. The decoder allocates the declared size on every open, so
// the limit bounds what an untrusted archive can make this package allocate.
// archives.Xz allows only 64 MiB, which rejects files written with a larger
// dictionary so that big, similar files compress against each other.
const xzDictMax = 256 << 20 // 256 MiB

// xzDecompressor is archives.Xz with a configurable dictionary limit in place
// of the 64 MiB that archives.Xz.OpenReader hard-codes.
type xzDecompressor struct {
	archives.Xz
	dictMax uint32
}

// OpenReader returns a reader that decompresses the xz stream r. Reads fail
// with fastxz.ErrMemlimit if the stream declares a dictionary above dictMax.
func (x xzDecompressor) OpenReader(r io.Reader) (io.ReadCloser, error) {
	xr, err := fastxz.NewReader(r, x.dictMax)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(xr), nil
}

// fileSystem is archives.FileSystem, except that an xz-compressed tar
// archive may declare a dictionary of up to xzDictMax. archives.FileSystem
// fails to identify such a stream when its dictionary is above 64 MiB, in
// which case the archive is opened with xzDecompressor instead.
func fileSystem(ctx context.Context, name string, stream archives.ReaderAtSeeker) (fs.FS, error) {
	fsys, err := archives.FileSystem(ctx, name, stream)
	if err == nil || stream == nil || !errors.Is(err, fastxz.ErrMemlimit) {
		return fsys, err
	}
	return xzTarFileSystem(ctx, name, stream)
}

// xzTarFileSystem returns a file system over the xz-compressed tar archive in
// stream, allowing a dictionary of up to xzDictMax. name is the archive's file
// name and is used, along with the decompressed content, to confirm that the
// xz stream holds a tar archive.
func xzTarFileSystem(ctx context.Context, name string, stream archives.ReaderAtSeeker) (fs.FS, error) {
	size, err := stream.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, fmt.Errorf("seeking for size: %w", err)
	}
	format := archives.CompressedArchive{
		Archival:    archives.Tar{},
		Extraction:  archives.Tar{},
		Compression: xzDecompressor{dictMax: xzDictMax},
	}

	decompressed, err := format.OpenReader(io.NewSectionReader(stream, 0, size))
	if err != nil {
		return nil, fmt.Errorf("open xz stream with a dictionary limit of %d MiB: %w", xzDictMax>>20, err)
	}
	match, err := archives.Tar{}.Match(ctx, filepath.Base(name), decompressed)
	if err != nil {
		return nil, fmt.Errorf("matching tar in xz stream: %w", err)
	}
	if !match.Matched() {
		return nil, errors.New("xz stream with a dictionary above 64 MiB does not hold a tar archive")
	}

	return &archives.ArchiveFS{Stream: io.NewSectionReader(stream, 0, size), Format: format, Context: ctx}, nil
}
