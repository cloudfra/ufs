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

package types

import (
	"io"
	"io/fs"
)

// FileInfo provides file metadata. It currently mirrors [fs.FileInfo] and is
// defined as a separate interface to allow future extensions without breaking
// callers.
type FileInfo interface {
	fs.FileInfo
}

// ReadFile is a read-only file handle. It satisfies [fs.File].
type ReadFile interface {
	fs.File
}

// File is a read-write file handle. It extends [ReadFile] with write, seek, and
// random-read operations. All methods are safe to call at any time after the
// file is opened; unlike bare [fs.File], no method returns a "not supported"
// error.
type File interface {
	ReadFile
	io.ReadWriteSeeker
	io.ReaderAt
	io.StringWriter
}
