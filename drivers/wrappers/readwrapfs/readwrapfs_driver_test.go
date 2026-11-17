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

package readwrapfs_test

import (
	"io"
	"io/fs"
	"maps"
	"slices"
	"testing"

	"github.com/cloudfra/ufs/drivers/core"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/drivers/wrappers/readwrapfs"
)

func TestReadWrapFSDriver(t *testing.T) {
	ufsdriversTesting.FS(t, ufsdriversTesting.Args{ReadOnly: true}, func(t *testing.T) types.SimpleFS {
		return readwrapfs.FromFS(seededMemFS(t))
	})
}

// seededMemFS holds a small tree, so the read-only wrapper is checked against
// content instead of an empty file system.
func seededMemFS(t *testing.T) types.WriteFS {
	t.Helper()
	fsys := core.MakeMemFS("memory://test")
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
		file, err := fsys.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) = %v", name, err)
		}
		if _, err := io.WriteString(file, files[name]); err != nil {
			t.Fatalf("writing %q = %v", name, err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("Close() on %q = %v", name, err)
		}
	}
	return fsys
}
