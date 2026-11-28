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

package readonlyfs

// Chtimes is [types.ChtimesFS], which [types.WriteFS] does not ask for: a
// caller asks for it with a type assertion. It is refused because a node's times
// are one of its properties, and moving them is a write to the node. The wrapped
// file system is not asked, even when it can change times: a wrapper that
// refuses writes does not pass a write through to what it wraps.

import (
	"io/fs"
	"time"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *readOnlyFS) Chtimes(name string, _ time.Time, _ time.Time) error {
	if err := pathutil.Validate("chtimes", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("chtimes", name, fs.ErrPermission)
}
