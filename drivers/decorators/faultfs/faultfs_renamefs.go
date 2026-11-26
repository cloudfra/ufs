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

package faultfs

// Rename is the [types.RenameFS] half of [types.FS]: the fault is injected
// for the old name, then [ops.Rename] copies and removes when the wrapped file
// system has no Rename.

import (
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/ops"
)

// Rename injects a fault for oldPath and then renames with [ops.Rename], which
// copies and removes when the wrapped file system has no Rename.
func (fsys *faultFS) Rename(oldPath, newPath string) error {
	if err := pathutil.Validate("rename", newPath); err != nil {
		return err
	}
	if err := fsys.maybeInjectFault("rename", oldPath); err != nil {
		return err
	}
	return ops.Rename(fsys.inner, oldPath, newPath)
}
