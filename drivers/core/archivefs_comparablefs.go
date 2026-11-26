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

// IsSame is the [types.ComparableFS] half of [types.ReadFS].

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
)

// IsSame reports whether other reads one opened archive under one name, which
// is one set of entries that cannot change.
func (fsys *archiveFS) IsSame(other fs.FS) bool {
	otherArchive, ok := types.InnermostFS(other).(*archiveFS)
	return ok && fsys.name == otherArchive.name && types.SameFS(fsys.fsys, otherArchive.fsys)
}
