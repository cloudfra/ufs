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

// End-to-end check that a mount spec reports errors with the path the caller
// used, kept in the external (ufs_test) package because it needs the readOnly
// decorator, which imports ufs.
package ufs_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs"
	_ "github.com/cloudfra/ufs/drivers/core"
	_ "github.com/cloudfra/ufs/drivers/decorators/readonlyfs"
	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestReadOnlyMountErrorNamesCallerPath reproduces the report in issue #387: a
// write through a read-only mount used to fail with the name the decorator
// saw, which is relative to the mount instead of to the caller.
func TestReadOnlyMountErrorNamesCallerPath(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "."
- source: "memory://"
  mountPoint: "frozen"
  options:
    - readOnly: true`
	fsys, err := ufs.New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	cases := []struct {
		name     string
		op       func(fsys types.WriteFS) error
		wantPath string
		wantMsg  string
	}{
		{
			name:     "create",
			op:       func(fsys types.WriteFS) error { _, err := fsys.Create("frozen/x.txt"); return err },
			wantPath: "frozen/x.txt",
			wantMsg:  "create frozen/x.txt: permission denied",
		},
		{
			name:     "mkdir",
			op:       func(fsys types.WriteFS) error { return fsys.MkdirAll("frozen/dir", 0o700) },
			wantPath: "frozen/dir",
			wantMsg:  "mkdir frozen/dir: permission denied",
		},
		{
			name:     "remove",
			op:       func(fsys types.WriteFS) error { return fsys.Remove("frozen/x.txt") },
			wantPath: "frozen/x.txt",
			wantMsg:  "remove frozen/x.txt: permission denied",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.op(fsys)
			if err == nil {
				t.Fatalf("%s through the read-only mount returned no error", tc.name)
			}
			if !errors.Is(err, fs.ErrPermission) {
				t.Errorf("%s through the read-only mount returned %v, want fs.ErrPermission", tc.name, err)
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Fatalf("%s through the read-only mount returned %v, want an *fs.PathError", tc.name, err)
			}
			if pathErr.Path != tc.wantPath {
				t.Errorf("%s through the read-only mount reported the path %q, want %q", tc.name, pathErr.Path, tc.wantPath)
			}
			if got := pathErr.Error(); got != tc.wantMsg {
				t.Errorf("%s through the read-only mount reported %q, want %q", tc.name, got, tc.wantMsg)
			}
		})
	}
}
