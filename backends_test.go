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

package ufs

import (
	"context"
	"testing"
)

// The backends live under drivers/ and import this package, so the tests of
// this package cannot import them back. backends_import_test.go links them
// into the test binary from package ufs_test, and the helpers below open
// them through the driver registry.

const (
	nullFSPrefix  = "null:"
	angryFSPrefix = "angry:"
	memFSPrefix   = "memory:"
)

// mustBaseFS opens the bare file system for name, without the nestFS layer.
func mustBaseFS(name string) WriteFS {
	fsys, err := newBaseFS(context.Background(), name)
	if err != nil {
		panic(err)
	}
	return fsys
}

func makeNullFS(name string) WriteFS {
	return mustBaseFS(name)
}

func mustNullFS(_ testing.TB) WriteFS {
	return makeNullFS(nullFSPrefix)
}

func newNullFS(ctx context.Context, name string) (WriteFS, error) {
	return newBaseFS(ctx, name)
}

func makeAngryFS(name string) WriteFS {
	return mustBaseFS(name)
}

func makeMemFS(name string) WriteFS {
	return mustBaseFS(name)
}

func newMemFS(ctx context.Context, name string) (WriteFS, error) {
	return newBaseFS(ctx, name)
}

func newLocalFS(ctx context.Context, name string) (WriteFS, error) {
	return newBaseFS(ctx, name)
}

func newArchiveFSFromLocalFS(ctx context.Context, name string) (WriteFS, error) {
	return newBaseFS(ctx, "archive://"+name)
}

// mustArchiveFS opens the test archive and closes it when the test ends.
func mustArchiveFS(t *testing.T) WriteFS {
	t.Helper()
	const testArchive = "testing/testassets/archives/testassets.tar.gz"
	fsys, err := newArchiveFSFromLocalFS(context.Background(), testArchive)
	if err != nil {
		t.Fatalf("newArchiveFSFromLocalFS(%q) = %v, want nil", testArchive, err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("failed to close archive FS: %v", err)
		}
	})
	return fsys
}
