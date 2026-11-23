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

// LinkFS is an optional interface that a file system may implement to support
// hard links: a second name that shares one stored file. It is not embedded in
// [WriteFS], so a driver need not implement it; callers ask for it with a type
// assertion. It covers hard links only; an existing symbolic link is read
// through [fs.ReadLinkFS], and creating one is a separate capability.
type LinkFS interface {
	// Link creates newname as another name for the file at oldname, so both
	// names describe the same content and metadata. It returns an error
	// wrapping [fs.ErrNotExist] if oldname does not exist and one wrapping
	// [fs.ErrExist] if newname already does. Most platforms refuse to hard link
	// a directory. Errors are *os.LinkError values, as with [RenameFS].
	//
	// A backend that cannot give two names the same file reports an error
	// rather than storing a copy: a copy does not stay in step with the file it
	// was made from.
	Link(oldname string, newname string) error
}
