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

// Package ufs provides a unified virtual file system abstraction for Go. It
// allows applications to treat diverse storage backends — local disk, memory,
// archives, Google Cloud Storage, remote Git repositories, and more — through
// a single consistent interface.
//
// # Creating a file system
//
// Use [New] with a URI to open a file system:
//
//	fsys, err := ufs.New("memory://")       // in-memory, volatile
//	fsys, err := ufs.New("null://")         // /dev/null semantics
//	fsys, err := ufs.New("/path/to/dir")    // local directory
//	fsys, err := ufs.New("file:///abs/dir") // same, explicit scheme
//	fsys, err := ufs.New("gs://bucket/prefix") // Google Cloud Storage
//	fsys, err := ufs.New("https://host/file.zip") // remote archive (downloaded to temp dir)
//
// Every [FS] must be closed when no longer needed.
//
// # Nested mounts
//
// [CreateURI] builds a URI that layers multiple file systems at different paths.
// Pass the resulting URI to [New]:
//
//	uri, _ := ufs.CreateURI("file:///data", map[string]string{
//	    "cache": "memory://",
//	})
//	fsys, _ := ufs.New(uri)
//
// # Path conventions
//
// All path arguments follow [fs.ValidPath]: forward-slash separated, no leading
// slash, no "." or ".." components. The root directory is always ".".
package ufs

import (
	"github.com/cloudfra/ufs/drivers/types"
)

// FS is the top-level file system interface returned by the public
// constructors. It extends [WriteFS] with renaming ([RenameFileFS]) and will
// add file copying ([CopyFileFS]). Every concrete value is a *nestFS.
type FS interface {
	types.WriteFS
	types.RenameFileFS

	// Copy copies the file at srcPath to dstPath.
	// Copy(srcPath, dstPath string) error
}
