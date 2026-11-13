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

package types

import (
	"io/fs"
)

// WrapperFS presents the data of another file system without storing data of
// its own, such as a read-only view or a decorator that reports operations. A
// file system that adds, hides or caches data stores data of its own, so the
// file system it wraps does not hold all of it and it must not implement this
// interface.
type WrapperFS interface {
	SimpleFS

	// InnerFS returns the file system whose data this file system presents, or
	// nil when it presents none.
	InnerFS() fs.FS
}

// InnermostFS returns the file system underneath every [WrapperFS] wrapped
// around fsys, which is where the data that fsys presents is stored. fsys is
// returned when it wraps nothing.
func InnermostFS(fsys fs.FS) fs.FS {
	for {
		wrapper, ok := fsys.(WrapperFS)
		if !ok {
			return fsys
		}
		inner := wrapper.InnerFS()
		// A file system that returns itself as its inner file system would
		// otherwise be unwrapped forever.
		if isNil(inner) || samePointer(fsys, inner) {
			return fsys
		}
		fsys = inner
	}
}
