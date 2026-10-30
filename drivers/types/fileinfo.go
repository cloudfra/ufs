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
)

var _ FileInfo = (*fileInfo)(nil)

// NewFileInfo returns a [FileInfo] for the given attributes. The result reports
// IsDir according to mode.IsDir() and a nil Sys value. It sits in this leaf
// package because both drivers/common/buffile and the root package need it.
func NewFileInfo(name string, size int64, mode fs.FileMode, modTime time.Time) FileInfo {
	return &fileInfo{
		name:    name,
		size:    size,
		mode:    mode,
		modTime: modTime,
		isDir:   mode.IsDir(),
	}
}

// fileInfo is the attribute-holding [FileInfo] returned by [NewFileInfo].
type fileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
	sys     any
}

func (finfo *fileInfo) Name() string {
	return finfo.name
}

func (finfo *fileInfo) Size() int64 {
	return finfo.size
}

func (finfo *fileInfo) Mode() fs.FileMode {
	return finfo.mode
}

func (finfo *fileInfo) ModTime() time.Time {
	return finfo.modTime
}

func (finfo *fileInfo) IsDir() bool {
	return finfo.isDir
}

func (finfo *fileInfo) Sys() any {
	return finfo.sys
}
