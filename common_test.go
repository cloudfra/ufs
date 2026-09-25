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

package ufs

import (
	"strings"
	"testing"
	"testing/fstest"

	driverTesting "github.com/cloudfra/ufs/drivers/testing"

	"github.com/cloudfra/ufs/internal/pathutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func TestInvalidPath(t *testing.T) {
	for _, tc := range getAllTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			driverTesting.InvalidPaths[File](t, tc.factory())
		})
	}
}

func TestFSConventions(t *testing.T) {
	srcFS, err := newLocalFS(t.Context(), testLocalFSName)
	if err != nil {
		t.Fatalf("cannot mount localFS(%q), %s", testLocalFSName, err)
	}
	t.Cleanup(ufsTesting.ValidateClose(t, srcFS))
	for _, fsysTC := range getReadWriteTestCaseList() {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)
			if err := Rsync(srcFS, fsys, pathutil.CwdPath); err != nil {
				t.Errorf("rsync failed with error, %s", err)
			}

			allFilenames, err := List(srcFS, pathutil.CwdPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(allFilenames) == 0 {
				t.Fatal("expected at least 1 file name")
			}
			if err := fstest.TestFS(fsys, allFilenames...); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestFSClose(t *testing.T) {
	for _, tc := range getAllRegularTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			driverTesting.Close[File](t, tc.factory())
		})
	}
}

func TestFSString(t *testing.T) {
	for _, tc := range getAllTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			if got := fsys.String(); !strings.Contains(got, tc.wantString) {
				t.Errorf("%s.String() should contain %q: got: %q", fsys, tc.wantString, got)
			}
		})
	}
}

func TestFSReadDir(t *testing.T) {
	for _, tc := range getAllRegularTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			driverTesting.ReadDir(t, tc.factory())
		})
	}
}

func TestFSCreate(t *testing.T) {
	for _, tc := range getAllRegularTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			driverTesting.CreateAndRead[File](t, tc.factory())
		})
	}
}
