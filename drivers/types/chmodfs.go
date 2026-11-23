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

// ChmodFS is an optional interface that a file system may implement to support
// changing the permissions of a node that already exists. It is not embedded in
// [WriteFS], so a driver need not implement it; callers ask for it with a type
// assertion. It carries no lookup methods of its own, so it can be asserted
// against any [fs.FS].
type ChmodFS interface {
	// Chmod sets the mode of the node at name to mode. A name that does not
	// exist returns an error wrapping [fs.ErrNotExist]. Which bits of mode a
	// backend honours depends on the platform: Unix keeps the permission bits
	// along with setuid, setgid and sticky, while a Windows path keeps only
	// enough to say whether the file is writable.
	Chmod(name string, mode fs.FileMode) error
}
