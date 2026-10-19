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

package ops_test

import (
	"fmt"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/ops"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

// rsyncDestTestCaseList are the writable file systems Rsync is verified
// against. localFS is opened with [ufs.New], so it is always nested.
var rsyncDestTestCaseList = []struct {
	name     string
	createFS func(tb testing.TB) types.WriteFS
}{
	{name: "nestFS.localFS", createFS: newTestLocalFS},
	{name: "tempMountFS", createFS: newTestTempMountFS},
	{name: "memFS", createFS: newTestMemFS},
	{name: "nestFS.tempMountFS", createFS: func(tb testing.TB) types.WriteFS {
		return ufs.WrapNestFS(tb.Context(), newTestTempMountFS(tb))
	}},
	{name: "nestFS.memFS", createFS: func(tb testing.TB) types.WriteFS {
		return ufs.WrapNestFS(tb.Context(), newTestMemFS(tb))
	}},
}

func TestRsync(t *testing.T) {
	srcFS := testAssetsFS(t)
	for _, fsysTC := range rsyncDestTestCaseList {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)
			t.Cleanup(ufsTesting.ValidateClose(t, fsys))
			if err := ops.Rsync(srcFS, fsys, pathutil.CwdPath); err != nil {
				t.Errorf("rsync failed with error, %s", err)
			}

			err := ops.ForEachFilename(srcFS, pathutil.CwdPath, func(name string) error {
				srcData, err := fs.ReadFile(srcFS, name)
				if err != nil {
					return fmt.Errorf("cannot read srcFS(%q), %w", name, err)
				}
				gotData, err := fs.ReadFile(fsys, name)
				if err != nil {
					return fmt.Errorf("cannot read destFS(%q), %w", name, err)
				}
				wantString := string(srcData)
				gotString := string(gotData)
				if diff := cmp.Diff(wantString, gotString); diff != "" {
					return fmt.Errorf("%q mismatch got %s, want %s diff(-want,+got):\n %v", name, gotString, wantString, diff)
				}
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		})
	}
}

func TestRsyncAngry(t *testing.T) {
	srcFS := testAssetsFS(t)

	destFS, err := newAngryFS("angry://")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.WantCloseError(t, destFS)()

	if err := ops.Rsync(srcFS, destFS, pathutil.CwdPath); err == nil {
		t.Error("rsync expected to fail got nil error")
	}
}

func TestRsyncNull(t *testing.T) {
	srcFS := testAssetsFS(t)

	destFS, err := ufs.New(t.Context(), "null://")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, destFS)()

	if err := ops.Rsync(srcFS, destFS, pathutil.CwdPath); err != nil {
		t.Errorf("rsync expected to succeed, failed with error: %s", err)
	}

	entries, err := destFS.ReadDir(pathutil.CwdPath)
	if err != nil {
		t.Error(err)
	}
	if len(entries) > 0 {
		t.Errorf("nullFS should have 0 entries, got: %v", entries)
	}
}
