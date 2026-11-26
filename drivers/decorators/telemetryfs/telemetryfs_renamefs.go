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

// Rename is the [types.RenameFS] half of [types.FS]: [ops.Rename] copies and
// removes when the wrapped file system has no Rename of its own, and the copy is
// reported as one rename.

import (
	"github.com/cloudfra/ufs/ops"
)

// Rename renames with [ops.Rename], which copies and removes when the wrapped
// file system has no Rename of its own. The copy is reported as one rename.
func (fsys *telemetryFS) Rename(oldPath, newPath string) error {
	span, start := fsys.begin(opRename)
	err := ops.Rename(fsys.inner, oldPath, newPath)
	fsys.endRename(opRename, oldPath, newPath, span, start, err)
	return err
}
