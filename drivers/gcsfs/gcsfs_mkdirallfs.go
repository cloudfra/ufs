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

package gcsfs

// MkdirAll is the [types.MkdirAllFS] half of [types.WriteFS]: it does nothing, because a GCS directory is only an object prefix.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
)

func (fsys *gcsFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := pathutil.Validate("mkdir", name); err != nil {
		return err
	}
	// GCS has no real directories; virtual directories emerge from object prefixes.
	return nil
}
