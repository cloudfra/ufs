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

import "time"

// ChtimesFS is an optional interface for file systems that can change the
// access and modification times of a file or directory in place. The
// Chtimes function in the ops package uses it when the file system
// implements it, and otherwise rewrites the file.
type ChtimesFS interface {
	// Chtimes changes the access and modification times of name, like
	// os.Chtimes. A zero time.Time leaves that time unchanged. It returns an
	// error wrapping fs.ErrNotExist if name does not exist.
	Chtimes(name string, atime, mtime time.Time) error
}
