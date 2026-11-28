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

package core

// Chtimes is [types.ChtimesFS], which [types.WriteFS] does not ask for: a caller asks for it with a type assertion.

import (
	"time"

	"github.com/cloudfra/ufs/drivers/polyfills"
)

// A temporary mount owns the directory and nothing in it: the times belong to
// the file system mounted there, so the request goes to [polyfills.Chtimes],
// which asks that file system and reports what it says. The mount keeps no copy
// of a time it could change on its own, so a file system mounted without a
// Chtimes is reported as it is rather than answered with a rewrite.
func (fsys *tempMountFS) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return polyfills.Chtimes(fsys.lfs, name, atime, mtime)
}
