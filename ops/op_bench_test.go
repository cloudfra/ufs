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
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// buildTree creates a directory tree on local disk with nFiles files of fileBytes bytes each,
// spread across ~depth directory levels, and returns it opened with ufs.New, so the benchmarks
// that use it measure through the nested file system that New wraps around localFS.
func buildTree(t testing.TB, nFiles int, depth int, fileBytes int) types.WriteFS {
	t.Helper()
	dir := t.TempDir()
	lfs, err := ufs.New(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range nFiles {
		level := i % depth
		subdir := ""
		for l := range level {
			subdir += fmt.Sprintf("d%d/", l)
		}
		dirPath := dir + "/" + subdir
		if err := osutil.MkdirAll(dirPath); err != nil {
			t.Fatal(err)
		}
		name := subdir + fmt.Sprintf("file_%d.dat", i)
		data := ufsTesting.SeedData(byte(i%256), fileBytes)
		lfsPath := dir + "/" + name
		if err := osutil.WriteFile(lfsPath, data); err != nil {
			t.Fatal(err)
		}
	}
	return lfs
}

// mustMemFS creates a memFS and attaches t.Cleanup to close it.
func mustMemFS(tb testing.TB, name string) types.WriteFS {
	fsys, err := newMemFS(name)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			tb.Fatal(err)
		}
	})
	return fsys
}
