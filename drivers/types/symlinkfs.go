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

// SymlinkFS is an optional interface that a file system may implement to
// support creating symbolic links. It is not embedded in [WriteFS], so a driver
// need not implement it; callers ask for it with a type assertion. It creates
// links only; reading one that exists is [fs.ReadLinkFS].
type SymlinkFS interface {
	// Symlink creates newname as a symbolic link storing oldname as its target.
	// The target is kept as written rather than resolved here: it is
	// interpreted later by whichever lookup follows the link, so it may be
	// relative to the directory holding newname, and it need not exist when the
	// link is made. [LinkFS] is the opposite case, needing a file that is
	// already there to share. It returns an error wrapping [fs.ErrExist] if
	// newname already exists and one wrapping [fs.ErrNotExist] if a parent
	// directory of newname is missing.
	//
	// A backend that has no link of its own reports an error rather than
	// copying or renaming something into place: a copy does not stay a link.
	Symlink(oldname string, newname string) error
}
