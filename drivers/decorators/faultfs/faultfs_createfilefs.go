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

// Create is the [types.CreateFileFS] half of [types.WriteFS], faulted and
// then delegated to the wrapped file system.

import (
	"github.com/cloudfra/ufs/drivers/types"
)

func (fsys *faultFS) Create(name string) (types.File, error) {
	if err := fsys.maybeInjectFault("create", name); err != nil {
		return nil, err
	}
	return fsys.inner.Create(name)
}
