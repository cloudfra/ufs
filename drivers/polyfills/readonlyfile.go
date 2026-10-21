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
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
)

// ToReadOnlyFile returns the handle f of a file that was opened for reading as a
// [types.File] that cannot write: its Write and WriteString always report
// [fs.ErrInvalid], the error the other file polyfills use for a read-only
// handle. A method f has besides Write and WriteString is kept, except for the
// optional interfaces that would write anyway, so Read, Seek, ReadAt, Stat and
// Close still reach the handle underneath.
//
// [ToBufferedFile] refuses writes only for a handle it has to adapt. A handle
// that is already a [types.File] is returned by it unchanged, and the handles of
// backends such as memFS are read-write even when the file was opened for
// reading, so a caller that opened a file for reading needs this polyfill for
// them.
//
// A handle backed by an OS file is returned as is, because the OS already
// rejects writes to a file that was opened for reading. The check is on an Fd
// method rather than on *os.File because localFS wraps its files on Windows, and
// a wrapper would hide the other methods of the handle.
func ToReadOnlyFile(f types.File) types.File {
	if _, ok := f.(interface{ Fd() uintptr }); ok {
		return f
	}
	return &readOnlyFile{File: f}
}

// readOnlyFile is a [types.File] that cannot write. It embeds the interface
// rather than the concrete handle, so an optional interface of the handle
// underneath such as io.WriterAt is not promoted and offers no way around Write.
type readOnlyFile struct {
	types.File
}

// Write always reports [fs.ErrInvalid].
func (f *readOnlyFile) Write(_ []byte) (int, error) {
	return 0, fs.ErrInvalid
}

// WriteString always reports [fs.ErrInvalid].
func (f *readOnlyFile) WriteString(_ string) (int, error) {
	return 0, fs.ErrInvalid
}
