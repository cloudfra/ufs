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

package polyfills

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
)

// IsSame reports whether fsys and other hold one set of data, keeping the
// contract of [types.ComparableFS].
//
// fsys's own [types.ComparableFS] implementation is used when it has one. The
// fallback is not implemented yet and reports false, which is the answer
// [types.ComparableFS] asks of a file system that cannot prove its data is
// other's data. Unlike [types.SameFS], which falls back to comparing the two
// values, this reports false for a file system that is not a
// [types.ComparableFS] even when it is compared with itself.
func IsSame(fsys fs.FS, other fs.FS) bool {
	if cFS, ok := fsys.(types.ComparableFS); ok {
		return cFS.IsSame(other)
	}
	// TODO: Populate this with the best effort implementation of this method based on any combinations of methods that are available to reconstruct this method. Also include tests.
	return false
}
