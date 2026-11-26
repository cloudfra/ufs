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

// BatchCreate is the [types.BatchCreateFS] half of the decorator: it reports a
// whole batch as one operation and writes it with [polyfills.BatchCreate], which
// uses the wrapped file system's own implementation when it has one. The
// decorator claims batch support whatever it wraps, so the atomicity that
// [types.BatchCreateFS] asks for belongs to the wrapped file system.

import (
	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
)

// BatchCreate writes files as one operation reported as ufs.batchcreate, named
// after its first file with ufs.files holding how many there are; an empty batch
// reports none. Checking the names is left to [polyfills.BatchCreate], so a bad
// name is reported as an invalid operation before any file is written.
func (fsys *telemetryFS) BatchCreate(files []types.BatchFile) error {
	span, start := fsys.begin(opBatchCreate)
	err := polyfills.BatchCreate(fsys.inner, files)
	var name string
	if len(files) > 0 {
		name = files[0].Name
	}
	fsys.end(opBatchCreate, name, span, start, err, filesKey.Int(len(files)))
	return err
}
