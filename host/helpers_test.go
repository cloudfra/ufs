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

package host

import (
	"io"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/osutil"
)

func validateClose(tb testing.TB, closer io.Closer) func() {
	return func() {
		tb.Helper()
		if closer != nil {
			if err := closer.Close(); err != nil {
				tb.Errorf("failed to close %s, %s", closer, err)
			}
		}
	}
}

// testAssetsFilesDir is the directory of files that were archived into
// testassets.tar.gz by the test asset generation step.
var testAssetsFilesDir = filepath.Join("..", "testing", "testassets", "files")

// loadTestAssets walks testAssetsFilesDir and returns a path→content map for every file.
func loadTestAssets(tb testing.TB) map[string][]byte {
	tb.Helper()
	src := osutil.DirFS(testAssetsFilesDir)
	result := make(map[string][]byte)
	err := fs.WalkDir(src, ufs.CwdPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		result[p] = data
		return nil
	})
	if err != nil {
		tb.Fatalf("loadTestAssets: %v", err)
	}
	return result
}
