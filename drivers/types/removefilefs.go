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

// RemoveFileFS provides file and directory deletion. It is embedded in [WriteFS],
// so every writable backend must implement it.
type RemoveFileFS interface {
	fs.FS

	// Remove deletes the file or empty directory at name. It returns an error
	// wrapping [fs.ErrNotExist] if name does not exist, or an error if name is
	// a non-empty directory. Removing the root (".") returns [fs.ErrPermission].
	Remove(name string) error

	// RemoveAll removes name and all contents beneath it. It is a no-op (returns
	// nil) if name does not exist.
	RemoveAll(name string) error
}
