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

package readwrapfs

// String is the [fmt.Stringer] half of [types.ReadFS].

import (
	"fmt"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/fsutil"
)

func (fsys *readWrapFS) String() string {
	if _, ok := fsys.fsys.(fmt.Stringer); ok {
		return types.Description(map[string]any{"readWrapFS": types.Nested(fsutil.String(fsys.fsys))})
	}
	return types.Description(map[string]any{"readWrapFS": fmt.Sprintf("%T", fsys.fsys)})
}
