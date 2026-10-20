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

// ExternalPathGet is an optional interface implemented by file systems that
// can expose a concrete external path for a virtual file. The returned value may
// be an absolute local path, a URL, or any other identifier that allows
// callers to access the file outside the virtual file system.
//
// This is useful when passing a file to a library or tool that does not
// understand [fs.FS] or the virtual path abstraction.
type ExternalPathGet interface {
	fs.FS

	// ExternalPath returns the canonical external path for the file at path.
	// Implementations that do not support this operation should return an empty
	// string.
	ExternalPath(path string) string
}
