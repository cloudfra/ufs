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
}

var invalidPaths = []string{
	"/absolute/path",
	"../relative/path",
	"invalid/../path",
}

// FS runs the conformance suite of every contract that the file system returned
// by createFSFunc supports, at whatever depth that support goes.
func FS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Cleanup(ufsTesting.ValidateClose(t, createFSFunc(t)))
	contracts(t, args, createFSFunc)

	// A file system that writes is tested again through a nestFS view of
	// itself, which needs a types.WriteFS to wrap.
	//
	// TODO: This should work for all writing file systems, but some backends
	// (like gcsfs) don't support this yet, so buckets are left out.
	createWriteFSFunc, ok := asWriteFS(t, args, createFSFunc)
	if ok && !args.Bucket {
		contracts(t, args, func(t *testing.T) types.SimpleFS {
			return ufs.WrapNestFS(t.Context(), createWriteFSFunc(t))
		})
	}
}

// contracts runs the public variant of each contract. Every variant checks for
// itself whether the file system supports it, so this list stays unconditional.
func contracts(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	MkdirAll(t, args, createFSFunc)
	ReadFile(t, args, createFSFunc)
	DirFileConflicts(t, args, createFSFunc)
	InvalidPathsForWriteFS(t, args, createFSFunc)
	ReadDir(t, args, createFSFunc)
	Close(t, args, createFSFunc)
	Create(t, args, createFSFunc)
	Rename(t, args, createFSFunc)
	BatchCreate(t, args, createFSFunc)
	Conventions(t, args, createFSFunc)
	CopyFileFS(t, args, createFSFunc)
	ReadFS(t, args, createFSFunc)
}

// asWriteFS narrows createFSFunc to a constructor for [types.WriteFS],
// reporting false when the write contracts do not apply to the file system.
//
// A read-only file system's writes have no observable result, so there is
// nothing for the write contracts to check even when it implements one.
func asWriteFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) (func(t *testing.T) types.WriteFS, bool) {
	t.Helper()
	if args.ReadOnly {
		return nil, false
	}
	return writableFS(t, createFSFunc)
}

// writableFS narrows createFSFunc to a constructor for [types.WriteFS],
// reporting false when the file system does not implement it.
func writableFS(t *testing.T, createFSFunc func(t *testing.T) types.SimpleFS) (func(t *testing.T) types.WriteFS, bool) {
	t.Helper()
	if _, ok := createFSFunc(t).(types.WriteFS); !ok {
		return nil, false
	}
	return func(t *testing.T) types.WriteFS {
		return mustWriteFS(t, createFSFunc(t))
	}, true
}

// asReadFS narrows createFSFunc to a constructor for [types.ReadFS], reporting
// false when the file system does not implement it.
func asReadFS(t *testing.T, createFSFunc func(t *testing.T) types.SimpleFS) (func(t *testing.T) types.ReadFS, bool) {
	t.Helper()
	if _, ok := createFSFunc(t).(types.ReadFS); !ok {
		return nil, false
	}
	return func(t *testing.T) types.ReadFS {
		return mustReadFS(t, createFSFunc(t))
	}, true
}

// asCopyFileFS narrows createFSFunc to a constructor for [types.CopyFileFS],
// reporting false when the file system does not implement it.
func asCopyFileFS(t *testing.T, createFSFunc func(t *testing.T) types.SimpleFS) (func(t *testing.T) types.CopyFileFS, bool) {
	t.Helper()
	if _, ok := createFSFunc(t).(types.CopyFileFS); !ok {
		return nil, false
	}
	return func(t *testing.T) types.CopyFileFS {
		return mustCopyFileFS(t, createFSFunc)
	}, true
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
