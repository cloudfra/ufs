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

// ChtimesFS is an optional interface that a file system may implement to
// support setting the times of a node that already exists. It is not embedded
// in [WriteFS], so a driver need not implement it; callers ask for it with a
// type assertion. It carries no lookup methods of its own, so it can be
// asserted against any [fs.FS].
type ChtimesFS interface {
	// Chtimes sets the access and modification times of the node at name to
	// atime and mtime. A zero time leaves that time alone. A name that does not
	// exist returns an error wrapping [fs.ErrNotExist]. As with [ChownFS], a
	// symbolic link at name is followed to the node it points at.
	//
	// Whether the times read back afterwards exactly match what was set depends
	// on what the backend records: some store sub-second precision and some
	// round to the second, and a backend that does not track access times at
	// all may leave atime untouched.
	Chtimes(name string, atime time.Time, mtime time.Time) error
}
