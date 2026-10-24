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

import "io/fs"

// ReleaseFS is an optional interface for file systems that can let go of the
// resources they are holding on to without being closed, so that other
// applications can use them. A file system that keeps a local copy of a remote
// file, for example, can delete that copy, and an archive mount can close the
// handle it holds open on its archive.
//
// Unlike [io.Closer], Release leaves the file system usable: it may be called
// on a file system that is still in use, and calling it repeatedly is not an
// error. A later operation that needs a released resource may have to acquire
// it again, which can mean re-fetching a remote file or re-opening an archive;
// a file system that cannot reacquire a resource by itself drops the part of
// its tree that depended on it until that resource is mounted again.
//
// Release lets go of the resources that the file system acquired for itself. A
// file system that was handed to it by the caller, along with what that file
// system holds open, is left alone: those are the caller's to release.
//
// A file system that implements ReleaseFS also implements [io.Closer], which
// releases the same resources for good.
type ReleaseFS interface {
	fs.FS

	// Release files and mount locks to allow other applications access to the
	// resources. Every resource is attempted: Release returns the errors it met
	// joined, and a resource it could not let go of is left for a later release
	// or close to try again.
	Release() error
}
