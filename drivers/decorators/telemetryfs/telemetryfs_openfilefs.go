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

// OpenFile is the [types.OpenFileFS] half of [types.FS], reported as the
// "openfile" operation around [polyfills.OpenFile], which uses the wrapped file
// system's own OpenFile when it has one.

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
)

// OpenFile opens with [polyfills.OpenFile], which uses the wrapped file system's
// own OpenFile when it has one, and reports it as the "openfile" operation.
func (fsys *telemetryFS) OpenFile(name string, flag int, perm fs.FileMode) (types.File, error) {
	span, start := fsys.begin(opOpenFile)
	f, err := polyfills.OpenFile(fsys.inner, name, flag, perm)
	fsys.end(opOpenFile, name, span, start, err)
	return f, err
}
