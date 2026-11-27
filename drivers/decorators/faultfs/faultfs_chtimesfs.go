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

package faultfs

// Chtimes is [types.ChtimesFS], which [types.WriteFS] does not ask for: a
// caller asks for it with a type assertion. It is faulted like every sibling and
// then handed to the wrapped file system.

import (
	"time"

	"github.com/cloudfra/ufs/drivers/polyfills"
)

// The times belong to the file system being wrapped, so the request goes to
// [polyfills.Chtimes], which asks that file system and reports what it says.
// A file system wrapped without a Chtimes of its own is reported as unable to
// change times rather than answered with a rewrite of the file, which would move
// its modification time to now and leave its access time where it was.
func (fsys *faultFS) Chtimes(name string, atime time.Time, mtime time.Time) error {
	if err := fsys.maybeInjectFault("chtimes", name); err != nil {
		return err
	}
	return polyfills.Chtimes(fsys.inner, name, atime, mtime)
}
