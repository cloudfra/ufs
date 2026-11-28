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

// MkdirAll is the [types.MkdirAllFS] half of [types.WriteFS]: it refuses,
// because a document cannot gain a directory.

import (
	"fmt"
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *yamlFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := pathutil.Validate("mkdir", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("mkdir", name, fmt.Errorf("yamlFS is read-only, cannot create directory %q: %w", name, fs.ErrPermission))
}
