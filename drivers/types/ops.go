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

// ListFilenames is an optional interface that a file system may implement to
// return all file paths under a directory without building an intermediate
// [fs.FileInfo] slice, reducing memory usage for large trees. ops.ListFiles
// uses this interface when available.
type ListFilenames interface {
	// ListFilenames returns the paths of all files (not directories) under dir,
	// in unspecified order, with reduced allocations.
	ListFilenames(string) ([]string, error)
}

// ForEachFileInfoIter is an optional interface for streaming [fs.FileInfo]
// values without building a full slice. ops.ForEachFileInfo uses this when the
// file system implements it.
type ForEachFileInfoIter interface {
	// ForEachFileInfo calls f for each file (not directory) under dir. If f
	// returns a non-nil error the walk stops and that error is returned.
	ForEachFileInfo(dir string, f func(fs.FileInfo) error) error
}

// ForEachFilenameIter is an optional interface for streaming file names without
// building a full slice. ops.ForEachFilename uses this when the file system
// implements it.
type ForEachFilenameIter interface {
	// ForEachFilename calls f for each file path (not directory) under dir. If f
	// returns a non-nil error the walk stops and that error is returned.
	ForEachFilename(dir string, f func(string) error) error
}
