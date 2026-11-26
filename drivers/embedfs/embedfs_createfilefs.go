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

package embedfs

// Create is the [types.CreateFileFS] half of [types.WriteFS], refused
// because an [embed.FS] cannot be written.

import (
	"fmt"
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *embedFS) Create(name string) (types.File, error) {
	if err := pathutil.Validate("create", name); err != nil {
		return nil, err
	}
	return nil, ufserrors.NewPathError("create", name, fmt.Errorf("embedFS is read-only, cannot create file %q: %w", name, fs.ErrPermission))
}
