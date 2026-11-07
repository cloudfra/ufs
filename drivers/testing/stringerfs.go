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

// Package testing provides shared conformance tests that drivers run against their
// types.ReadFS, types.WriteFS, and ufs.FS implementations.
package testing

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// String verifies that the file system's String method returns a URI-like
// description containing "//".
func String(t *testing.T, _ Args, createFSFunc func(t *testing.T) fs.FS) {
	t.Run("String", func(t *testing.T) {
		t.Parallel()
		fsys := mustStringerFS(t, createFSFunc(t))
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))
		if got := fsys.String(); !strings.Contains(got, "//") {
			t.Errorf("%s.String() should contain %q: got: %q", fsys, "//", got)
		}
	})
}

func mustStringerFS(t *testing.T, fsys fs.FS) types.StringerFS {
	t.Helper()
	readFS, ok := fsys.(types.StringerFS)
	if !ok {
		t.Fatalf("fsys %T does not implement types.StringerFS", fsys)
	}
	return readFS
}
