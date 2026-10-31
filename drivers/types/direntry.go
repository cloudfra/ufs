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
	"time"

	"github.com/cloudfra/ufs/internal/osutil"
)

var (
	_ fs.DirEntry = (*DirEntry)(nil)
	_ fs.FileInfo = (*DirEntry)(nil)
)

// DirEntry is the [fs.DirEntry] and [fs.FileInfo] of a directory that a file
// system knows about without being able to describe it, such as the level above
// where a mount begins. It reports no modification time and the size an empty
// directory has on this platform, and Sys reports nothing.
type DirEntry struct {
	name string
}

// NewDirEntry returns the [DirEntry] of a directory called name.
func NewDirEntry(name string) *DirEntry {
	return &DirEntry{
		name: name,
	}
}

// Name returns the name the entry was made with.
func (entry *DirEntry) Name() string {
	return entry.name
}

// IsDir reports true: the entry is always a directory.
func (entry *DirEntry) IsDir() bool {
	return true
}

// Type returns [fs.ModeDir], the only type the entry can have.
func (entry *DirEntry) Type() fs.FileMode {
	return fs.ModeDir
}

// Mode returns the type as a file mode, which carries no permission bits.
func (entry *DirEntry) Mode() fs.FileMode {
	return entry.Type()
}

// Info returns the entry itself, which describes a directory as both a
// [fs.DirEntry] and a [fs.FileInfo].
func (entry *DirEntry) Info() (fs.FileInfo, error) {
	return entry, nil
}

// Size returns the size an empty directory has on this platform.
func (entry *DirEntry) Size() int64 {
	return osutil.EmptyDirSize
}

// ModTime returns the zero time, because nothing is known about the directory.
func (entry *DirEntry) ModTime() time.Time {
	return time.Time{}
}

// Sys returns nil: there is no underlying data source to report.
func (entry *DirEntry) Sys() any {
	return nil
}
