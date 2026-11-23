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
	"fmt"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

// releaseContentFS is the set of interfaces the cases that need content use:
// they take the tree a file system already holds and read it back through
// Release, so a read-only file system is judged by them exactly like a writable
// one. Nothing here writes, which is what keeps the whole suite runnable for a
// file system that refuses every write.
type releaseContentFS interface {
	types.ReleaseFS
	types.ReadFS
}

// releaseFS runs the conformance suite for [types.ReleaseFS] against the file
// system returned by createFSFunc.
//
// It does not branch on [Args.ReadOnly] or [Args.Bucket]: releasing is about the
// handles and mount locks a file system holds on its own resources, which a
// read-only file system holds just the same and answers for just the same.
//
// Two clauses of the interface are out of reach for these fixtures and go
// unchecked: that every resource is attempted and the errors joined, and that a
// file system handed in by the caller is left alone.
func releaseFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("ReleaseFS", func(t *testing.T) {
		requireType[types.ReleaseFS](t, args)
		t.Parallel()
		createReleaseFSFunc := requireFSTypeFunc[types.ReleaseFS](t, createFSFunc)
		createContentFSFunc := requireFSTypeFunc[releaseContentFS](t, createFSFunc)

		releaseNothingHeld(t, createReleaseFSFunc)
		releaseTwice(t, createReleaseFSFunc)
		releaseKeepsUsable(t, createContentFSFunc)
		releaseReacquire(t, createContentFSFunc)
	})
}

// releaseNothingHeld requires Release on a file system that has done nothing
// yet to succeed: it has no resource of its own to let go of, and releasing is
// not the operation that finishes the file system.
func releaseNothingHeld(t *testing.T, createFSFunc func(t *testing.T) types.ReleaseFS) {
	t.Run("ReleaseNothingHeld", func(t *testing.T) {
		t.Parallel()
		fsys := newReleaseFS(t, createFSFunc)

		if err := fsys.Release(); err != nil {
			t.Errorf("Release() on a file system nothing has been asked of it = %v, want nil: [types.ReleaseFS] lets go of the resources a file system acquired for itself, and it has acquired none", err)
		}
	})
}

// releaseTwice requires Release a second and third time to succeed. Close is
// the operation that may only happen once; the interface documents repeated
// release as not an error so that a caller can hand a resource back without
// knowing whether someone already did.
func releaseTwice(t *testing.T, createFSFunc func(t *testing.T) types.ReleaseFS) {
	t.Run("ReleaseTwice", func(t *testing.T) {
		t.Parallel()
		fsys := newReleaseFS(t, createFSFunc)

		for attempt := range 3 {
			if err := fsys.Release(); err != nil {
				t.Errorf("Release() attempt %d = %v, want nil: [types.ReleaseFS] says calling it repeatedly is not an error", attempt+1, err)
			}
		}
	})
}

// releaseKeepsUsable requires the tree a file system holds to still be readable
// after Release, which is the difference between Release and Close. A handle
// left open across the call is not checked: the interface lets a file system
// drop a resource it can reacquire, so no fixture can require a live handle to
// survive.
func releaseKeepsUsable(t *testing.T, createFSFunc func(t *testing.T) releaseContentFS) {
	t.Run("ReleaseKeepsUsable", func(t *testing.T) {
		t.Parallel()
		fsys := newReleaseFS(t, createFSFunc)

		before, err := releaseTree(t, fsys)
		if err != nil {
			t.Fatalf("reading the tree before Release() = %v", err)
		}

		if err := fsys.Release(); err != nil {
			t.Fatalf("Release() = %v, want nil", err)
		}

		after, err := releaseTree(t, fsys)
		if err != nil {
			t.Fatalf("reading the tree after Release() = %v, want nil: [types.ReleaseFS] leaves the file system usable, so only Close may take away what it holds", err)
		}
		if diff := cmp.Diff(before, after); diff != "" {
			t.Errorf("the tree readable after Release() changed (-want +got):\n%s", diff)
		}
	})
}

// releaseReacquire requires the first operation after a release to acquire again
// whatever the release let go of instead of failing it. The release comes before
// any read here, which is what separates this case from releaseKeepsUsable, and
// the acquisition is repeated so that a file system that dropped the resource
// again on the second release has to be able to pick it up a second time.
func releaseReacquire(t *testing.T, createFSFunc func(t *testing.T) releaseContentFS) {
	t.Run("ReleaseReacquire", func(t *testing.T) {
		t.Parallel()
		fsys := newReleaseFS(t, createFSFunc)

		if err := fsys.Release(); err != nil {
			t.Fatalf("Release() = %v, want nil", err)
		}

		first, err := releaseTree(t, fsys)
		if err != nil {
			t.Fatalf("reading the tree after Release() = %v, want nil: [types.ReleaseFS] says a later operation may have to acquire a released resource again, which is not an error for the caller", err)
		}

		if err := fsys.Release(); err != nil {
			t.Fatalf("Release() after a read = %v, want nil", err)
		}
		second, err := releaseTree(t, fsys)
		if err != nil {
			t.Fatalf("reading the tree after a second Release() = %v, want nil", err)
		}
		if diff := cmp.Diff(first, second); diff != "" {
			t.Errorf("the tree read after the second Release() differs from the tree read after the first (-want +got):\n%s", diff)
		}
	})
}

// releaseTree returns every regular file fsys holds, keyed by name and read
// through ReadFile. Walking is what forces a file system that let go of a
// resource to acquire it again, which is the operation these cases judge, so a
// file system whose tree stops answering fails here rather than later.
func releaseTree(t *testing.T, fsys releaseContentFS) (map[string]string, error) {
	t.Helper()
	tree := make(map[string]string)
	err := fs.WalkDir(fsys, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// A link or a device is answered by ReadLink or another call, not by
		// ReadFile, so the tree only compares what ReadFile is for.
		if !entry.Type().IsRegular() {
			return nil
		}
		content, err := fsys.ReadFile(name)
		if err != nil {
			return fmt.Errorf("ReadFile(%q) = %w", name, err)
		}
		tree[name] = string(content)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tree, nil
}

// newReleaseFS builds a file system that can release its resources, for one
// sub-suite. Close is left to the cleanup, which also checks that a released
// file system can still be closed.
func newReleaseFS[T types.ReleaseFS](t *testing.T, createFS func(*testing.T) T) T {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	return fsys
}
