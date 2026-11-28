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
	"errors"
	"io/fs"
	"reflect"
	"strings"
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

// invalidPaths are the name spellings every file system must refuse: one per
// class of invalid name, each written against no suite's own tree so that every
// suite that validates a name asserts the same set.
var invalidPaths = []string{
	"/absolute/path",
	"../relative/path",
	"invalid/../path",
	"a/b/../c",
	"",
	"/",
	"a//b",
	"a/",
	"a/b/",
	"a/./b",
	"./a",
}

// pathVariations returns the spellings of name that a file system normalizing
// "." and ".." elements would read as naming the same file. None of them is a
// valid ufs path, so an operation given a pair of names must refuse each one
// before it decides anything about the pair.
func pathVariations(name string) []string {
	// A directory hierarchy added in front of name and cancelled by the same
	// number of ".." elements, one level deep and two, names the same file.
	variations := []string{"./" + name, name + "/", "x/../" + name, "x/y/../../" + name}
	for i := range len(name) {
		if name[i] != '/' {
			continue
		}
		prefix, suffix := name[:i+1], name[i+1:]
		variations = append(variations,
			prefix+"./"+suffix,
			prefix+"//"+suffix,
			prefix+"../"+name,
			prefix+"x/../"+suffix,
			prefix+"x/y/../../"+suffix,
		)
	}
	return variations
}

// assertPairPathVariations calls op over srcPath and dstPath for every pairing
// of a pathVariations spelling of one with a pathVariations spelling of the
// other, each of which must be refused as an invalid name. afterCall runs after
// every call so that the caller can assert the file system was left as it was.
// opName labels op in failures and subtest names.
func assertPairPathVariations(t *testing.T, opName string, op func(srcPath, dstPath string) error, afterCall func(t *testing.T), srcPath, dstPath string) {
	t.Helper()

	for _, src := range pathVariations(srcPath) {
		for _, dst := range pathVariations(dstPath) {
			t.Run(pathLabel(src)+"_"+pathLabel(dst), func(t *testing.T) {
				err := op(src, dst)
				if !errors.Is(err, fs.ErrInvalid) {
					t.Errorf("%s(%q, %q) = %v, want an error wrapping %v", opName, src, dst, err, fs.ErrInvalid)
				}
				if _, ok := errors.AsType[*fs.PathError](err); !ok {
					t.Errorf("%s(%q, %q) = %T, want an *fs.PathError", opName, src, dst, err)
				}
				afterCall(t)
			})
		}
	}
}

// pathLabel returns name spelled so that it can be a subtest name.
func pathLabel(name string) string {
	if name == "" {
		return "empty"
	}
	return strings.ReplaceAll(name, "/", "_")
}

// WrapperFS runs the full read-write conformance suite against file systems
// returned by createFSFunc.
func WrapperFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.WriteFS, createWrapperFSFunc func(t *testing.T, inner types.SimpleFS) types.WrapperFS) {
	FS(t, args, func(t *testing.T) types.SimpleFS {
		inner := createFSFunc(t)
		return createWrapperFSFunc(t, inner)
	})
}

// FS runs the full read-write conformance suite against file systems
// returned by createFSFunc.
func FS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	exampleFS := createFSFunc(t)
	args.fsType = reflect.TypeOf(exampleFS)
	ufsTesting.ValidateClose(t, exampleFS)()

	copyFileFS(t, args, createFSFunc)
	uriGetFS(t, args, createFSFunc)
	comparableFS(t, args, createFSFunc)
	wrapperFS(t, args, createFSFunc)
	mkdirAllFS(t, args, createFSFunc)
	createFileFS(t, args, createFSFunc)
	renameFS(t, args, createFSFunc)
	removeFS(t, args, createFSFunc)
	removeAllFS(t, args, createFSFunc)
	releaseFS(t, args, createFSFunc)
	watcher(t, args, createFSFunc)
	mountedArchiveDirFS(t, args, createFSFunc)
	stringerFS(t, args, createFSFunc)
	deviceInfoGet(t, args, createFSFunc)
	chtimesFS(t, args, createFSFunc)

	// Which suite family a file system is judged by comes from what it is,
	// except that Args.ReadOnly settles it first for a read-only file system.
	// One may answer with the full [ufs.FS] method set while refusing every
	// write, as the read-only decorator does, and the write suites seed the
	// tree they then assert on, which is not something a file system refusing
	// writes can be asked to do. Those fixtures are seeded by their own driver
	// tests and judged by the read suites instead, alongside the refusal cases
	// run unconditionally above.
	switch {
	case args.ReadOnly:
		readFS(t, args, func(t *testing.T) types.ReadFS {
			return mustFSType[types.ReadFS](t, createFSFunc)
		})
	case args.fsType.AssignableTo(reflect.TypeFor[ufs.FS]()):
		fsInternal(t, args, func(t *testing.T) ufs.FS {
			return mustFSType[ufs.FS](t, createFSFunc)
		})
	case args.fsType.AssignableTo(reflect.TypeFor[types.WriteFS]()):
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
	writeFSInternal(t, args, func(t *testing.T) types.WriteFS {
		return createFSFunc(t)
	})
}

// WriteFile creates name in fsys with content, stopping the test if Create,
// the write, or Close fails.
func WriteFile(tb testing.TB, fsys types.CreateFileFS, name, content string) {
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

func requireFSTypeFunc[T types.SimpleFS](t *testing.T, createFSFunc func(t *testing.T) types.SimpleFS) func(t *testing.T) T {
	t.Helper()
	return func(t *testing.T) T {
		t.Helper()
		fsys := createFSFunc(t)
		cFsys, ok := fsys.(T)
		if !ok {
			t.Skipf("%T does not implement %s so this test cannot proceed", fsys, reflect.TypeFor[T]())
		}
		return cFsys
	}
}
