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

package testing

import (
	"io/fs"
	"path"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// MkdirAll verifies that MkdirAll creates every missing directory in a path,
// that each one is reported as a directory and listed by its parent next to
// its siblings, and that repeating the call on existing directories succeeds.
func MkdirAll(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	createWriteFSFunc, ok := asWriteFS(t, args, createFSFunc)
	if !ok {
		return
	}
	mkdirAll(t, args, createWriteFSFunc)
}

func mkdirAll(t *testing.T, args Args, createFSFunc func(t *testing.T) types.WriteFS) {
	if args.Bucket {
		t.Run("MkdirAll", func(t *testing.T) {
			t.Parallel()
			fsys := createFSFunc(t)
			defer ufsTesting.ValidateClose(t, fsys)()
			for _, dir := range mkdirAllLeaves {
				if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
					t.Fatalf("MkdirAll(%q) = %v, want nil", dir, err)
				}
				name := path.Join(dir, "file")
				f, err := fsys.Create(name)
				if err != nil {
					t.Fatalf("Create(%q) = %v, want nil", name, err)
				}
				if err := f.Close(); err != nil {
					t.Fatalf("Close() = %v, want nil", err)
				}
			}
			ufsTesting.AssertDirsInOrder(t, fsys, wantDirTree(mkdirAllLeaves, "file"))
		})
	} else {
		t.Run("MkdirAll", func(t *testing.T) {
			t.Parallel()
			fsys := createFSFunc(t)
			defer ufsTesting.ValidateClose(t, fsys)()
			for _, dir := range mkdirAllLeaves {
				if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
					t.Fatalf("MkdirAll(%q) = %v, want nil", dir, err)
				}
			}
			ufsTesting.AssertDirsInOrder(t, fsys, wantDirTree(mkdirAllLeaves))
			for _, dir := range mkdirAllLeaves {
				if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
					t.Errorf("MkdirAll(%q) on existing directories = %v, want nil", dir, err)
				}
			}
		})
	}
}
