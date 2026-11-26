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

package gcsfs

// IsSame is the [types.ComparableFS] half of [types.ReadFS]: two file systems are the same when one client reads one bucket and prefix.

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
)

// IsSame reports whether other reads one bucket and prefix through one client,
// which is one set of objects.
func (fsys *gcsFS) IsSame(other fs.FS) bool {
	otherGCS, ok := types.InnermostFS(other).(*gcsFS)
	return ok && fsys.client == otherGCS.client && fsys.bucket == otherGCS.bucket && fsys.baseDir == otherGCS.baseDir
}
