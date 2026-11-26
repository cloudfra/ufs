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

// Rename is [types.RenameFS].

import (
	"github.com/cloudfra/ufs/internal/pathutil"
)

// Rename discards the rename, as nullFS discards every write.
func (fsys *nullFS) Rename(oldPath, newPath string) error {
	return pathutil.ValidateRename(oldPath, newPath)
}
