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

// WriteFileFS is an optional interface that a file system may implement to
// write a whole file in one call. It is the alternative to [CreateFileFS] for a
// backend that can store a completed file but has no use for handing out a
// writable handle. It is not embedded in [WriteFS], so a driver need not
// implement it; callers ask for it with a type assertion.
type WriteFileFS interface {
	// WriteFile writes data to the file at name, creating it if necessary and
	// truncating what was there before. mode gives the permission bits of a
	// file that is created; a file that already exists keeps the permissions it
	// has. It returns an error wrapping [fs.ErrNotExist] when a parent
	// directory of name is missing: parents are not created here, as with
	// [CreateFileFS].
	WriteFile(name string, data []byte, mode fs.FileMode) error
}
