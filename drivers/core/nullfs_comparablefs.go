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

// IsSame reports whether other is a nullFS with the same name, which holds the
// same data because nullFS stores nothing.
func (fsys *nullFS) IsSame(other fs.FS) bool {
	otherNull, ok := types.InnermostFS(other).(*nullFS)
	return ok && fsys.name == otherNull.name
}
