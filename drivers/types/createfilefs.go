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

// CreateFileFS is an optional interface that a file system may implement to support
// file creation.
type CreateFileFS interface {
	fs.FS

	// Create opens a new writable file at name, replacing any existing file at
	// that path. Parent directories are not created automatically; call
	// MkdirAll first if needed.
	Create(name string) (File, error)
}
