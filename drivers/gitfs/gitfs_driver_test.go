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

//go:build !aix && !wasip1 && !plan9

package gitfs

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfra/ufs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/internal/osutil"
)

func TestGitFSDriver(t *testing.T) {
	ufsdriversTesting.WriteFSWithBuckets(t, func(t *testing.T) ufs.WriteFS {
		srcDir, err := osutil.MkdirTemp("", "gitfssrc*.git")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := osutil.RemoveAll(srcDir); err != nil {
				t.Errorf("osutil.RemoveAll(%q) = %v", srcDir, err)
			}
		}()

		if err := initTestGitRepo(t, srcDir, map[string]string{
			"hello.txt": "hello world",
			"readme.md": "# Test Repo",
		}); err != nil {
			t.Fatalf("initTestGitRepo: %v", err)
		}

		// Use a file:// URI so String() reports a URI rather than a bare path.
		srcURI := (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(srcDir), "/")}).String()
		fsys, err := New(t.Context(), srcURI)
		if err != nil {
			t.Fatalf("cannot create gitFS %q, %s", srcURI, err)
		}
		return fsys
	})
}
