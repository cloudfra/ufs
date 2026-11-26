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

package telemetryfs

// IsSame is the [types.ComparableFS] half of [types.ReadFS]: reporting
// operations does not change what is stored, so the wrapper is the same as what
// it wraps. Both types answer it the same way.

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
)

// IsSame reports whether other holds the wrapped file system's data, since
// reporting operations does not change what is stored.
func (fsys *watchTelemetryFS) IsSame(other fs.FS) bool {
	return types.SameFS(fsys.inner, other)
}

// IsSame reports whether other holds the wrapped file system's data, since
// reporting operations does not change what is stored.
func (fsys *telemetryFS) IsSame(other fs.FS) bool {
	return types.SameFS(fsys.inner, other)
}
