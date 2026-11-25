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

import (
	"testing"

	"github.com/cloudfra/ufs/drivers/decorators/readonlyfs"
	ufsdecoratorsTesting "github.com/cloudfra/ufs/drivers/decorators/testing"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
)

// TestTelemetryFSDriver runs the shared file system tests through the
// decorator, which must not change how the file system behaves.
func TestTelemetryFSDriver(t *testing.T) {
	ufsdriversTesting.WrapperFS(t, ufsdriversTesting.Args{ReadOnly: true}, seededInnerFS, func(t *testing.T, inner types.SimpleFS) types.WrapperFS {
		rfsys, ok := inner.(types.ReadFS)
		if !ok {
			t.Fatalf("inner file system is not a ReadFS: %T", inner)
		}
		wfsys, ok := readonlyfs.New(rfsys).(types.WrapperFS)
		if !ok {
			t.Fatalf("wrapped file system is not a WrapperFS: %T", wfsys)
		}
		return wfsys
	})

	ufsdriversTesting.WrapperFS(t, ufsdriversTesting.Args{}, ufsdecoratorsTesting.InnerFS, func(t *testing.T, inner types.SimpleFS) types.WrapperFS {
		writeFsys, ok := inner.(types.WriteFS)
		if !ok {
			t.Fatalf("inner file system is not a WriteFS: %T", inner)
		}
		fsys := newRecorder().wrap(t.Context(), t, writeFsys, both)
		wrapFsys, ok := fsys.(types.WrapperFS)
		if !ok {
			t.Fatalf("wrapped file system is not a WrapperFS: %T", wrapFsys)
		}
		return wrapFsys
	})
}

// seededInnerFS is the shared seeded inner file system with one more file, the
// one the [types.ReleaseFS] suite reads either side of a release. That suite
// seeds it by writing, which the read-only wrapper it is handed to refuses, so
// the file goes into the file system before it is wrapped.
func seededInnerFS(t *testing.T) types.WriteFS {
	t.Helper()
	fsys := ufsdecoratorsTesting.SeededInnerFS(t)
	ufsdriversTesting.SeedReleaseFS(t, fsys)
	return fsys
}
