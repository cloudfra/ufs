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

package grpcfs

// grpcDir is the handle [grpcFS.Open] returns for a directory: [fs.ReadDirFile]
// over the ListFiles answer, with no handle behind it on the server.

import (
	"io"
	"io/fs"

	"github.com/cloudfra/ufs/internal/ufserrors"
)

var _ fs.ReadDirFile = (*grpcDir)(nil)

// grpcDir is a directory handle. It is not a handle on the server at all, but a
// view over the ListFiles answer, listed the first time it is walked so that
// opening a directory costs nothing beyond the one metadata read that told the
// caller it was a directory.
type grpcDir struct {
	fsys *grpcFS
	name string
	info fs.FileInfo

	entries []fs.DirEntry
	offset  int
	// listed says the listing has been asked for, so an empty directory is not
	// listed again on every call.
	listed bool
}

// Stat reports the directory.
func (d *grpcDir) Stat() (fs.FileInfo, error) {
	return d.info, nil
}

// Read is refused: a directory has no content to read, which is the answer the
// file systems behind this service give for the same call.
func (d *grpcDir) Read([]byte) (int, error) {
	return 0, ufserrors.NewPathError("read", d.name, fs.ErrInvalid)
}

// Seek is refused for the same reason Read is; a directory is walked from the
// front, in the order the server answered it.
func (d *grpcDir) Seek(int64, int) (int64, error) {
	return 0, ufserrors.NewPathError("seek", d.name, fs.ErrInvalid)
}

// ReadDir returns the children of the directory, from where the last call of
// ReadDir left off. A count that is zero or negative is the rest of them, which
// is what [fs.ReadDirFile] asks for in that case.
func (d *grpcDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if !d.listed {
		entries, err := d.fsys.ReadDir(d.name)
		if err != nil {
			return nil, err
		}
		d.entries, d.listed = entries, true
	}
	if n <= 0 {
		rest := d.entries[d.offset:]
		d.offset = len(d.entries)
		return rest, nil
	}
	if d.offset >= len(d.entries) {
		return nil, io.EOF
	}
	end := min(d.offset+n, len(d.entries))
	rest := d.entries[d.offset:end]
	d.offset = end
	return rest, nil
}

// Readdir answers the older spelling of ReadDir, which the io/fs directory
// handles still carry.
func (d *grpcDir) Readdir(n int) ([]fs.FileInfo, error) {
	entries, err := d.ReadDir(n)
	// The io/fs handles report the information of the entries they list, and an
	// io.EOF that comes with no entries says the walk is over rather than that
	// anything failed.
	if len(entries) == 0 {
		return nil, err
	}
	infos := make([]fs.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, infoErr
		}
		infos = append(infos, info)
	}
	return infos, err
}

// Close ends the handle. There is nothing on the server to give up, since the
// listing was a call that had already finished.
func (d *grpcDir) Close() error {
	return nil
}
