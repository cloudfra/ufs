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

package tempmountfs_test

import (
	"path/filepath"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/tempmountfs"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestAbsPath verifies that a tempMountFS resolves host paths, both bare and
// under the nested file system that ufs.New wraps around every backend.
func TestAbsPath(t *testing.T) {
	testCases := []struct {
		name string
		wrap func(*testing.T, ufs.WriteFS) ufs.WriteFS
	}{
		{name: "tempMountFS", wrap: func(_ *testing.T, fsys ufs.WriteFS) ufs.WriteFS { return fsys }},
		{name: "nestFS.tempMountFS", wrap: func(t *testing.T, fsys ufs.WriteFS) ufs.WriteFS {
			return ufs.WrapNestFS(t.Context(), fsys)
		}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inner, err := tempmountfs.New(t.Context(), "test://", func(string) error { return nil })
			if err != nil {
				t.Fatalf("New() = %v, want nil", err)
			}
			fsys := tc.wrap(t, inner)
			defer ufsTesting.ValidateClose(t, fsys)()

			baseDir, err := ufs.AbsPath(fsys, ".")
			if err != nil {
				t.Fatalf("AbsPath(.) = %v, want nil", err)
			}
			if !filepath.IsAbs(baseDir) {
				t.Errorf("AbsPath(.) = %q, want absolute path", baseDir)
			}
			got, err := ufs.AbsPath(fsys, "sub/dir/deep.txt")
			if err != nil {
				t.Fatalf("AbsPath(sub/dir/deep.txt) = %v, want nil", err)
			}
			if want := filepath.Join(baseDir, "sub", "dir", "deep.txt"); got != want {
				t.Errorf("AbsPath(sub/dir/deep.txt) = %q, want %q", got, want)
			}
		})
	}
}
