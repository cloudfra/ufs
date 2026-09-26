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

package testing_test

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/cloudfra/ufs"
	driverTesting "github.com/cloudfra/ufs/drivers/testing"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestRunMemory runs the whole suite through the public ufs API, the way an
// external driver would.
func TestRunMemory(t *testing.T) {
	driverTesting.Run[ufs.File](t, func(tb testing.TB) fs.FS {
		fsys, err := ufs.New(tb.Context(), "memory:")
		if err != nil {
			tb.Fatalf("ufs.New(memory:) = %v", err)
		}
		tb.Cleanup(ufsTesting.ValidateClose(tb, fsys))
		return fsys
	})
}

// TestWriteTestsSkipReadOnlyFS verifies that tests needing capabilities a file
// system lacks are skipped rather than failed.
func TestWriteTestsSkipReadOnlyFS(t *testing.T) {
	newFS := func(testing.TB) fs.FS {
		return fstest.MapFS{"a.txt": &fstest.MapFile{Data: []byte("a")}}
	}
	for name, test := range map[string]func(*testing.T, driverTesting.Factory){
		"RoundTrip":        driverTesting.RoundTrip[ufs.File],
		"Close":            driverTesting.Close[ufs.File],
		"MkdirAll":         driverTesting.MkdirAll,
		"ReadDir":          driverTesting.ReadDir,
		"CreateAndRead":    driverTesting.CreateAndRead[ufs.File],
		"ReadFile":         driverTesting.ReadFile[ufs.File],
		"DirFileConflicts": driverTesting.DirFileConflicts[ufs.File],
	} {
		var ran *testing.T
		if !t.Run(name, func(t *testing.T) {
			ran = t
			test(t, newFS)
		}) {
			t.Errorf("%s failed on a read-only fs.FS, want it skipped", name)
		}
		if ran != nil && !ran.Skipped() {
			t.Errorf("%s ran on a read-only fs.FS, want it skipped", name)
		}
	}
}
