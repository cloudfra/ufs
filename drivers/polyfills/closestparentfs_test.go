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

package polyfills_test

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/cloudfra/ufs/drivers/polyfills"
)

// TestClosestParentFallback walks up a file system with no ClosestParent of its
// own and checks that the first level that exists answers, file or directory.
func TestClosestParentFallback(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a/b/file.txt": &fstest.MapFile{Data: []byte("x")},
	}
	for _, tc := range []struct {
		name, want string
		isDir      bool
	}{
		{"a/b/file.txt", "a/b/file.txt", false},
		{"a/b/missing", "a/b", true},
		{"a/missing/deeper/still", "a", true},
		{"nothing/here", ".", true},
		{"a/b/file.txt/under", "a/b/file.txt", false},
	} {
		parent, isDir, err := polyfills.ClosestParent(fsys, tc.name)
		if err != nil || parent != tc.want || isDir != tc.isDir {
			t.Errorf("ClosestParent(%q) = %q, %v, %v, want %q, %v, nil", tc.name, parent, isDir, err, tc.want, tc.isDir)
		}
	}
	if _, _, err := polyfills.ClosestParent(fsys, "."); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("ClosestParent(.) = %v, want an error wrapping fs.ErrInvalid", err)
	}
}
