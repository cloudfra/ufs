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

import (
	"testing"

	"github.com/cloudfra/ufs/drivers/core"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
)

// TestFaultFSDriver runs the shared conformance suites over a fault wrapper
// with no faults configured, pinning the path validation and delegation that
// every operation performs before it injects.
func TestFaultFSDriver(t *testing.T) {
	ufsdriversTesting.FS(t, ufsdriversTesting.Args{}, func(t *testing.T) types.SimpleFS {
		fsys, err := newFaultFS(core.MakeMemFS("memory://test"), Options{})
		if err != nil {
			t.Fatalf("cannot create faultFS: %v", err)
		}
		return fsys
	})
}
