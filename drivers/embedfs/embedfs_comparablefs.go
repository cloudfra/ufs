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

package embedfs

// IsSame is the [types.ComparableFS] half of [types.ReadFS].

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
)

// IsSame reports whether other is this file system, since two embedFS values
// over one embedded file system cannot be recognized as holding the same bytes.
// The embedded file system is a value that each embedFS copies, so it holds no
// identity that the two could compare.
func (fsys *embedFS) IsSame(other fs.FS) bool {
	otherEmbed, ok := types.InnermostFS(other).(*embedFS)
	return ok && fsys == otherEmbed
}
