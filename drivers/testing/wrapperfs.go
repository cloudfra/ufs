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
	"slices"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// wrapperFS runs the [types.WrapperFS] suite, which checks that the returned
// file system is where the data under test is actually held.
func wrapperFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("wrapperFS", func(t *testing.T) {
		requireType[types.WrapperFS](t, args)
		t.Parallel()
		createWrapperFunc := requireFSTypeFunc[types.WrapperFS](t, createFSFunc)
		innerPresentsSameData(t, createWrapperFunc)
		innerListsSameEntries(t, createWrapperFunc)
		innerSeesWrite(t, args, createWrapperFunc)
		wrapperSeesInnerWrite(t, createWrapperFunc)
	})
}

// innerPresentsSameData asserts a wrapper and the file system it returns hold
// one set of data, which is what keeps a wrapper from hiding the data it
// presents from a comparison.
func innerPresentsSameData(t *testing.T, createFSFunc func(t *testing.T) types.WrapperFS) {
	t.Run("innerPresentsSameData", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		innerFS := fsys.InnerFS()
		if innerFS == nil {
			t.Fatalf("%T.InnerFS() = nil, want the file system whose data this file system presents", fsys)
		}
		if !types.SameFS(fsys, innerFS) {
			t.Errorf("types.SameFS(%T, %T.InnerFS()) = false, want true, because a wrapper stores none of the data it presents", fsys, fsys)
		}
		if !types.SameFS(innerFS, fsys) {
			t.Errorf("types.SameFS(%T.InnerFS(), %T) = false, want true, because a wrapper stores none of the data it presents", fsys, fsys)
		}
	})
}

// innerListsSameEntries asserts the returned file system lists what the wrapper
// lists, so that a file system cannot return an unrelated file system that
// happens to compare as holding the same data.
func innerListsSameEntries(t *testing.T, createFSFunc func(t *testing.T) types.WrapperFS) {
	t.Run("innerListsSameEntries", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		innerFS := fsys.InnerFS()
		names, err := entryNames(t, fsys)
		if err != nil {
			t.Fatalf("fs.ReadDir(%T, %q) = %v", fsys, ".", err)
		}
		innerNames, err := entryNames(t, innerFS)
		if err != nil {
			t.Fatalf("fs.ReadDir(%T.InnerFS(), %q) = %v, want the entries the wrapper lists", innerFS, ".", err)
		}
		if !slices.Equal(names, innerNames) {
			t.Errorf("%T lists %q and %T.InnerFS() lists %q, want the same entries because the wrapper presents that file system", fsys, names, fsys, innerNames)
		}
	})
}

// innerSeesWrite asserts a file created through the wrapper is read from the
// returned file system, which is where a wrapper's writes have to land.
func innerSeesWrite(t *testing.T, args Args, createFSFunc func(t *testing.T) types.WrapperFS) {
	t.Run("innerSeesWrite", func(t *testing.T) {
		t.Parallel()
		if args.ReadOnly {
			t.Skipf("%s is read-only, so a write through it is refused and cannot reach its inner file system", args.fsType)
		}
		requireType[types.CreateFileFS](t, args)
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		const markerPath = "wrapper-inner-marker.txt"
		const markerContent = "written through the wrapper"
		WriteFile(t, fsys.(types.CreateFileFS), markerPath, markerContent)

		content, err := fs.ReadFile(fsys.InnerFS(), markerPath)
		if err != nil {
			t.Fatalf("fs.ReadFile(%T.InnerFS(), %q) = %v, want the file %T created", fsys, markerPath, err, fsys)
		}
		if string(content) != markerContent {
			t.Errorf("fs.ReadFile(%T.InnerFS(), %q) = %q, want %q, because the wrapper presents that file system's data", fsys, markerPath, content, markerContent)
		}
	})
}

// wrapperSeesInnerWrite asserts a file created through the returned file system
// is read from the wrapper, which is what makes the two one set of data rather
// than two stores that were seeded alike.
func wrapperSeesInnerWrite(t *testing.T, createFSFunc func(t *testing.T) types.WrapperFS) {
	t.Run("wrapperSeesInnerWrite", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		innerFS := fsys.InnerFS()
		createInnerFS, ok := innerFS.(types.CreateFileFS)
		if !ok {
			t.Skipf("%T.InnerFS() = %T, which cannot create a file, so a write through it cannot be checked", fsys, innerFS)
		}

		const markerPath = "wrapper-inner-marker.txt"
		const markerContent = "written through the inner file system"
		WriteFile(t, createInnerFS, markerPath, markerContent)

		content, err := fs.ReadFile(fsys, markerPath)
		if err != nil {
			t.Fatalf("fs.ReadFile(%T, %q) = %v, want the file %T.InnerFS() created", fsys, markerPath, err, fsys)
		}
		if string(content) != markerContent {
			t.Errorf("fs.ReadFile(%T, %q) = %q, want %q, because the wrapper presents that file system's data", fsys, markerPath, content, markerContent)
		}
	})
}
