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

package ufs

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
)

// readOnlyFile is a types.File whose Write and WriteString always fail. It keeps a
// backend's read-write handle, such as the one memFS returns from Open, from
// being used to modify a file that was opened for reading.
type readOnlyFile struct {
	types.File
}

func (f *readOnlyFile) Write([]byte) (int, error) {
	return 0, fs.ErrInvalid
}

func (f *readOnlyFile) WriteString(string) (int, error) {
	return 0, fs.ErrInvalid
}

// readOnlyHandle returns a types.File for the handle f of a file that was opened for
// reading, one that cannot be used to write.
//
// A handle backed by an OS file needs no wrapper: the OS already rejects
// writes to a file that was opened for reading. The check is on Fd rather than
// on *os.File because localFS wraps its files on Windows, and a wrapper would
// hide the other methods of the handle (Sync, ReadDir, the io.Copy fast paths).
func readOnlyHandle(f types.File) types.File {
	if _, ok := f.(interface{ Fd() uintptr }); ok {
		return f
	}
	return &readOnlyFile{File: f}
}
