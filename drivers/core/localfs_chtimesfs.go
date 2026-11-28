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
)

// The times go to the operating system, which is what decides how precisely they
// are kept. A zero time is passed on as one to leave alone, which is what
// [os.Root] already does; filling in the current clock instead would turn a call
// that asks to change one time into one that moves the other to now.
func (fsys *localFS) Chtimes(name string, atime time.Time, mtime time.Time) error {
	if err := validLocalPath("chtimes", name); err != nil {
		return err
	}
	return fsys.osFS.Chtimes(name, atime, mtime)
}
