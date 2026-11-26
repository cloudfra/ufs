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

package ufs

// Release is [types.ReleaseFS]: it lets go of what the mounts hold on to
// without closing the nests the caller configured.

// Release lets go of the resources that the mounts of fsys hold on to without
// closing fsys itself, so that other applications can use them. A mount that ufs
// opened for itself is closed and unmounted, which releases the handle it keeps
// on the file it was opened from; in practice such a mount is the content
// directory of an archive inside the tree. A mount that the caller configured,
// by handing over a file system or by giving a URI for ufs to open, and a mount
// that is not backed by a file, keeps its own file system and releases the
// mounts below it, recursively.
//
// fsys stays usable, and the content directory of an archive inside it is
// mounted again from the file that holds it the next time a path under it is
// used. The base file system of fsys is not released; only [nestFS.Close] lets
// go of it.
//
// Every mount is attempted. Release joins the errors it meets, and a mount that
// could not be released stays mounted, so that a later release or close can try
// again.
func (fsys *nestFS) Release() error {
	return fsys.mounts.releaseAll()
}
