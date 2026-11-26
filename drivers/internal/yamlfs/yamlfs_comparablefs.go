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

package yamlfs

// IsSame is the [types.ComparableFS] half of [types.ReadFS]: New builds its
// own tree, so only one and the same value is the same file system.

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
)

// IsSame reports whether other is this file system. New builds a tree from the
// document, so two yamlFS values over one document hold separate trees, and the
// entries of one cannot be recognized as the entries of the other.
func (fsys *yamlFS) IsSame(other fs.FS) bool {
	otherYaml, ok := types.InnermostFS(other).(*yamlFS)
	return ok && fsys == otherYaml
}
