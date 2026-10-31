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

// ClosestParentFS is an optional interface that a file system may implement to
// report the closest existing node above a path in one call, which is what a
// back end already knows while it resolves the path for its own operation.
type ClosestParentFS interface {
	fs.FS

	// ClosestParent returns the closest node that exists at or above name, name
	// itself included, and reports whether that node is a directory. A file
	// system that has no node above a missing name reports its root as a
	// directory. An error is returned only when the file system cannot answer
	// at all: missing levels are the answer, not an error.
	ClosestParent(name string) (parent string, isDir bool, err error)
}
