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
	"reflect"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// Args for conformance tests.
//
// Each argument is optional and calls out a deviation from the conformance test.
type Args struct {
	// ReadOnly indicates that the file system is read-only.
	// If the conformance test would normally exercise a write, it will assert the appropriate error is returned for a read-only file system.
	ReadOnly bool

	// Bucket indicates that the file system may not have an explicit directory structure.
	Bucket bool

	fsType reflect.Type
}

var invalidPaths = []string{
	"/absolute/path",
	"../relative/path",
	"invalid/../path",
}

// FS runs the full read-write conformance suite against file systems
// returned by createFSFunc.
func FS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	exampleFS := createFSFunc(t)
	args.fsType = reflect.TypeOf(exampleFS)
	ufsTesting.ValidateClose(t, exampleFS)()

	copyFileFS(t, args, createFSFunc)
	uriGetFS(t, args, createFSFunc)

	switch {
	case args.fsType.AssignableTo(reflect.TypeFor[ufs.FS]()):
		fsInternal(t, args, func(t *testing.T) ufs.FS {
			return mustFSType[ufs.FS](t, createFSFunc)
		})
	case args.fsType.AssignableTo(reflect.TypeFor[types.WriteFS]()) && !args.ReadOnly:
		writeFS(t, args, func(t *testing.T) types.WriteFS {
			return mustFSType[types.WriteFS](t, createFSFunc)
		})
	case args.fsType.AssignableTo(reflect.TypeFor[types.ReadFS]()):
		readFS(t, args, func(t *testing.T) types.ReadFS {
			return mustFSType[types.ReadFS](t, createFSFunc)
		})
	default:
		t.Fatalf("%q is not a supported file system, %+v", args.fsType, exampleFS)
	}
}

func fsInternal(t *testing.T, args Args, createFSFunc func(t *testing.T) ufs.FS) {
	createWriteFSFunc := func(t *testing.T) types.WriteFS {
		return createFSFunc(t)
	}
	mkdirAll(t, args, createWriteFSFunc)
	ReadFile(t, args, createWriteFSFunc)
	DirFileConflicts(t, args, createWriteFSFunc)
	Rename(t, args, createWriteFSFunc)
	BatchCreate(t, args, createWriteFSFunc)

	exampleFS := createFSFunc(t)
	if _, ok := exampleFS.(types.ReadFS); ok {
		readFS(t, args, func(t *testing.T) types.ReadFS {
			return createFSFunc(t)
		})
	}
}

// WriteFile creates name in fsys with content, stopping the test if Create,
// the write, or Close fails.
func WriteFile(tb testing.TB, fsys types.WriteFS, name, content string) {
	tb.Helper()
	f, err := fsys.Create(name)
	if err != nil {
		tb.Fatalf("Create(%q) = %v", name, err)
	}
	if _, err := f.WriteString(content); err != nil {
		tb.Fatalf("WriteString(%q) = %v", name, err)
	}
	if err := f.Close(); err != nil {
		tb.Fatalf("Close(%q) = %v", name, err)
	}
}

// requireType skips the calling test when the file system under test does not
// implement T. Callers must pass the t of a suite's own subtest because
// skipping a wrapper skips every suite that would run after it.
func requireType[T any](tb testing.TB, args Args) {
	if !args.fsType.AssignableTo(reflect.TypeFor[T]()) {
		tb.Skip("FS type does not implement the interface required for this test")
	}
}

func mustFSType[T types.SimpleFS](t *testing.T, createFSFunc func(t *testing.T) types.SimpleFS) T {
	t.Helper()
	fsys := createFSFunc(t)
	cFsys, ok := fsys.(T)
	if !ok {
		t.Fatalf("%T does not implement %s", fsys, reflect.TypeFor[T]())
	}
	return cFsys
}

func mustFSTypeFunc[T types.SimpleFS](t *testing.T, createFSFunc func(t *testing.T) types.SimpleFS) func(t *testing.T) T {
	t.Helper()
	return func(t *testing.T) T {
		t.Helper()
		fsys := createFSFunc(t)
		cFsys, ok := fsys.(T)
		if !ok {
			t.Fatalf("%T does not implement %s", fsys, reflect.TypeFor[T]())
		}
		return cFsys
	}
}
