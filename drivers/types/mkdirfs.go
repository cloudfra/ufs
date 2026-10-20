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

// MkdirFS is an optional interface that a file system may implement to support
// directory creation.
type MkdirFS interface {
	fs.FS

	// MkdirAll creates the directory at name and any missing parent directories,
	// using perm for newly created nodes. It is a no-op if the directory already
	// exists. Backends that do not have a real directory concept (e.g. GCS) treat
	// this as a no-op.
	Mkdir(name string, perm fs.FileMode) error
}
