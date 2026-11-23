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

// Package testing provides the file systems a decorator's conformance test
// wraps: an empty one for a suite that is allowed to write its own names, and
// one that already holds a small tree for a suite that is not.
package testing

import (
	"io/fs"
	"maps"
	"slices"
	"testing"

	"github.com/cloudfra/ufs/drivers/core"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
)

// InnerFS creates an empty memory file system, which is what a decorator whose
// suites may write needs: every suite starts from a store no earlier suite
// touched, so a name one suite created cannot satisfy the next.
func InnerFS(t *testing.T) types.WriteFS {
	t.Helper()
	return core.MakeMemFS("memory://inner")
}

// SeededInnerFS creates a memory file system that already holds a small tree of
// directories and files, so a read-only decorator is checked against content it
// would be refused if it tried to create it itself.
func SeededInnerFS(t *testing.T) types.WriteFS {
	t.Helper()
	fsys := core.MakeMemFS("memory://seeded-inner")
	for _, dir := range []string{"readconf", "readconf/nested"} {
		if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
			t.Fatalf("MkdirAll(%q) = %v", dir, err)
		}
	}
	files := map[string]string{
		"readconf/alpha.txt":       "alpha",
		"readconf/nested/beta.txt": "beta content",
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		ufsdriversTesting.WriteFile(t, fsys, name, files[name])
	}
	return fsys
}
