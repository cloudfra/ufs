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

package ufs_test

import (
	"testing"

	"github.com/cloudfra/ufs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
)

func TestNestedMemFSDriver(t *testing.T) {
	ufsdriversTesting.FS(t, ufsdriversTesting.Args{}, func(t *testing.T) types.SimpleFS {
		name := "memory://test"
		fsys, err := ufs.NewNestFS(t.Context(), name)
		if err != nil {
			t.Fatalf("cannot create nestFS %q, %s", name, err)
		}
		return fsys
	})
}

// TestNestedLocalFSOpenFileDriver runs the shared OpenFile conformance suite
// through nestFS instead of only against the file system underneath it, which
// is what checks that the mount layer keeps the flag contract intact.
func TestNestedLocalFSOpenFileDriver(t *testing.T) {
	ufsdriversTesting.OpenFile(t, ufsdriversTesting.Args{}, func(t *testing.T) types.OpenFileFS {
		name := "file://" + t.TempDir()
		fsys, err := ufs.NewNestFS(t.Context(), name)
		if err != nil {
			t.Fatalf("cannot create nestFS %q, %s", name, err)
		}
		openFileFS, ok := fsys.(types.OpenFileFS)
		if !ok {
			t.Fatalf("nestFS %q does not implement types.OpenFileFS", name)
		}
		return openFileFS
	})
}
