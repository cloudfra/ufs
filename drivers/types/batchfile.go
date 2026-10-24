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

// BatchFile is one file that [BatchCreate] writes.
type BatchFile struct {
	// Name is the path of the file. It must satisfy [fs.ValidPath] and must
	// not be the root (".").
	Name string

	// Data is the whole content of the file. The file system does not keep a
	// reference to it after BatchCreate returns, and the caller must not
	// change it before then.
	Data []byte

	// Perm holds the permission bits of the file, on file systems that store
	// them. Zero means the permissions that Create uses.
	Perm fs.FileMode
}
