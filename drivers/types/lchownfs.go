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

// LchownFS is an optional interface that a file system may implement to support
// changing the ownership of a node as its name is given. It is not embedded in
// [WriteFS], so a driver need not implement it; callers ask for it with a type
// assertion. It carries no lookup methods of its own, so it can be asserted
// against any [fs.FS].
type LchownFS interface {
	// Lchown sets the owner and group of the node at name to uid and gid, and
	// when name is a symbolic link changes the link itself rather than the node
	// it points at. That is the difference from [ChownFS], which follows the
	// link. A uid or gid of -1 leaves that half of the ownership alone, and a
	// name that does not exist returns an error wrapping [fs.ErrNotExist].
	//
	// Numeric owner and group ids come from Unix, so which of them a backend
	// honors depends on the platform. A backend that has nothing equivalent to
	// record reports an error rather than storing ids it will not apply.
	Lchown(name string, uid int, gid int) error
}
