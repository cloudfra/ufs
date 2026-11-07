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

package readonlyfs_test

import (
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs/drivers/core"
	"github.com/cloudfra/ufs/drivers/decorators/readonlyfs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
)

func TestReadOnlyFSDriver(t *testing.T) {
	ufsdriversTesting.ReadFS(t, ufsdriversTesting.Args{ReadOnly: true}, func(_ *testing.T) fs.FS {
		return readonlyfs.New(core.MakeMemFS("memory://test"))
	})
}
