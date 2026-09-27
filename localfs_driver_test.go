// Copyright 2026 Jeremy Edwards
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

package ufs_test

import (
	"testing"

	"github.com/cloudfra/ufs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
)

func TestLocalFSDriver(t *testing.T) {
	ufsdriversTesting.WriteFS(t, func(t *testing.T) ufs.WriteFS {
		dir := t.TempDir()
		fsys, err := ufs.MakeLocalFS(dir)
		if err != nil {
			t.Fatalf("cannot create localFS %q, %s", dir, err)
		}
		return fsys
	})
}
