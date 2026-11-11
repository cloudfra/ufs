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
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// releaseFSName is the file releaseSeeded writes before Release so that the
// cases can tell a released file system from a closed one: it has to still read
// back afterwards.
const releaseFSName = "r/release.txt"

// releaseFSContent is what releaseFSName holds before Release.
const releaseFSContent = "before release"

// releaseAgainName is written after Release, which is what checks that a file
// system that let go of a resource can acquire it again.
const releaseAgainName = "r/after.txt"

// releaseSeededTestFS is the minimum set of interfaces releaseKeepsUsable and
// releaseReacquire need: they write a file, release, and read the tree back
// through Open.
type releaseSeededTestFS interface {
	types.ReleaseFS
	types.CreateFileFS
	types.MkdirAllFS
	fs.ReadFileFS
}

// releaseFS runs the conformance suite for [types.ReleaseFS] against the file
// system returned by createFSFunc.
//
// It does not branch on [Args.ReadOnly] or [Args.Bucket]: releasing is about
// the handles and mount locks a file system holds on its own resources, which a
// read-only file system holds just the same.
//
// Two clauses of the interface are out of reach for these fixtures and go
// unchecked: that every resource is attempted and the errors joined, and that a
// file system handed in by the caller is left alone.
func releaseFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("ReleaseFS", func(t *testing.T) {
		requireType[types.ReleaseFS](t, args)
		t.Parallel()
		createReleaseFSFunc := requireFSTypeFunc[types.ReleaseFS](t, createFSFunc)
		createSeededFSFunc := requireFSTypeFunc[releaseSeededTestFS](t, createFSFunc)

		releaseNothingHeld(t, createReleaseFSFunc)
		releaseTwice(t, createReleaseFSFunc)
		releaseKeepsUsable(t, createSeededFSFunc)
		releaseReacquire(t, createSeededFSFunc)
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

// releaseKeepsUsable requires the file system to still answer reads and stats
// after Release, including for a file written before it. This is the difference
// between Release and Close. A handle left open across the call is not checked:
// the interface lets a file system drop a resource it can reacquire, so no
// fixture can require a live handle to survive.
func releaseKeepsUsable(t *testing.T, createFSFunc func(t *testing.T) releaseSeededTestFS) {
	t.Run("ReleaseKeepsUsable", func(t *testing.T) {
		t.Parallel()
		fsys := newReleaseSeededFS(t, createFSFunc)

		WriteFile(t, fsys, releaseFSName, releaseFSContent)

		if err := fsys.Release(); err != nil {
			t.Fatalf("Release() = %v, want nil", err)
		}

		got, err := fs.ReadFile(fsys, releaseFSName)
		if err != nil {
			t.Fatalf("ReadFile(%q) after Release() = %v, want %q: [types.ReleaseFS] says Release leaves the file system usable, so only Close may take the content away", releaseFSName, err, releaseFSContent)
		}
		if string(got) != releaseFSContent {
			t.Errorf("ReadFile(%q) after Release() = %q, want %q: releasing lets go of resources, not of the file system's content", releaseFSName, got, releaseFSContent)
		}

		if _, err := fs.Stat(fsys, releaseFSName); err != nil {
			t.Errorf("Stat(%q) after Release() = %v, want nil: a released file system still has to answer for the names it holds", releaseFSName, err)
		}
	})
}

// releaseReacquire requires a file system that dropped a resource to acquire it
// again on the next operation instead of failing it. A file system that cannot
// reacquire on its own drops the part of its tree that depended on it, which
// the interface allows, but a name it still lists has to work.
func releaseReacquire(t *testing.T, createFSFunc func(t *testing.T) releaseSeededTestFS) {
	t.Run("ReleaseReacquire", func(t *testing.T) {
		t.Parallel()
		fsys := newReleaseSeededFS(t, createFSFunc)

		if err := fsys.Release(); err != nil {
			t.Fatalf("Release() = %v, want nil", err)
		}

		WriteFile(t, fsys, releaseAgainName, "after release")

		got, err := fs.ReadFile(fsys, releaseAgainName)
		if err != nil {
			t.Fatalf("ReadFile(%q) written after Release() = %v, want its content: [types.ReleaseFS] says a later operation may have to acquire a released resource again, which is not an error for the caller", releaseAgainName, err)
		}
		if string(got) != "after release" {
			t.Errorf("ReadFile(%q) written after Release() = %q, want %q", releaseAgainName, got, "after release")
		}
	})
}

// newReleaseFS builds a file system that can release its resources, for one
// sub-suite. Close is left to the cleanup, which also checks that a released
// file system can still be closed.
func newReleaseFS(t *testing.T, createFS func(*testing.T) types.ReleaseFS) types.ReleaseFS {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	return fsys
}

// newReleaseSeededFS builds a file system that can be written to and released,
// for one sub-suite.
func newReleaseSeededFS(t *testing.T, createFS func(*testing.T) releaseSeededTestFS) releaseSeededTestFS {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	return fsys
}
