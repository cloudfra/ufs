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

// releaseFSName is the file the cases that need content read either side of
// Release so that they can tell a released file system from a closed one: it has
// to still read back afterwards.
const releaseFSName = "r/release.txt"

// releaseFSContent is what releaseFSName holds across Release.
const releaseFSContent = "before release"

// releaseAgainName is written after Release, which is what checks that a file
// system that let go of a resource can acquire it again.
const releaseAgainName = "r/after.txt"

// releaseSeededTestFS is the set of interfaces the cases that need content use:
// they put a file in the file system, release, and read it back through Open.
// ReadFS rather than [fs.ReadFileFS] is what lets a read-only file system that
// only answers reads be judged by them too.
type releaseSeededTestFS interface {
	types.ReleaseFS
	types.CreateFileFS
	types.MkdirAllFS
	types.ReadFS
}

// releaseFS runs the conformance suite for [types.ReleaseFS] against the file
// system returned by createFSFunc.
//
// The four cases run whatever [Args.Bucket] and [Args.ReadOnly] say: releasing
// is about the handles and mount locks a file system holds on its own
// resources, which a read-only file system holds just the same and answers for
// just the same. What [Args.ReadOnly] settles is only how the file the two last
// cases read gets where it has to be, since a file system refusing writes cannot
// be seeded through itself — see [SeedReleaseFS].
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
		releaseKeepsUsable(t, args, createSeededFSFunc)
		releaseReacquire(t, args, createSeededFSFunc)
	})
}

// SeedReleaseFS puts the file the [types.ReleaseFS] suite reads either side of
// Release into fsys.
//
// A file system that accepts writes is seeded by the suite through itself, which
// puts the file wherever that file system keeps its content. One that refuses
// writes cannot be, so the driver test for one calls this on the file system it
// is about to wrap: the content a release has to survive sits in the store
// underneath a read-only view of it, which is where the view is asked for it.
func SeedReleaseFS(tb testing.TB, fsys types.CreateFileFS) {
	tb.Helper()
	WriteFile(tb, fsys, releaseFSName, releaseFSContent)
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
func releaseKeepsUsable(t *testing.T, args Args, createFSFunc func(t *testing.T) releaseSeededTestFS) {
	t.Run("ReleaseKeepsUsable", func(t *testing.T) {
		t.Parallel()
		fsys := newReleaseFS(t, createFSFunc)

		seedRelease(t, args, fsys)

		if err := fsys.Release(); err != nil {
			t.Fatalf("Release() = %v, want nil", err)
		}

		assertReleaseSeed(t, fsys, "")

		if _, err := fs.Stat(fsys, releaseFSName); err != nil {
			t.Errorf("Stat(%q) after Release() = %v, want nil: a released file system still has to answer for the names it holds", releaseFSName, err)
		}
	})
}

// releaseReacquire requires a file system that dropped a resource to acquire it
// again on the next operation instead of failing it. A file system that cannot
// reacquire on its own drops the part of its tree that depended on it, which
// the interface allows, but a name it still lists has to work.
func releaseReacquire(t *testing.T, args Args, createFSFunc func(t *testing.T) releaseSeededTestFS) {
	t.Run("ReleaseReacquire", func(t *testing.T) {
		t.Parallel()
		fsys := newReleaseFS(t, createFSFunc)

		seedRelease(t, args, fsys)

		if err := fsys.Release(); err != nil {
			t.Fatalf("Release() = %v, want nil", err)
		}

		// Creating the file is the operation that has to acquire again whatever
		// the release let go of. A file system refusing writes cannot be asked
		// for that, so for one the read is the reacquisition, and the release is
		// repeated so that a second acquisition is required of it too.
		if args.ReadOnly {
			assertReleaseSeed(t, fsys, "after Release()")

			if err := fsys.Release(); err != nil {
				t.Fatalf("Release() after a read = %v, want nil", err)
			}
			assertReleaseSeed(t, fsys, "after a second Release()")
			return
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

// seedRelease puts releaseFSName where the cases read it back: through the file
// system under test unless that refuses writes, in which case its driver test
// was given [SeedReleaseFS] to put it in the file system being wrapped.
func seedRelease(t *testing.T, args Args, fsys releaseSeededTestFS) {
	t.Helper()
	if !args.ReadOnly {
		// The directory the two names are written into is made here: Create is
		// only asked to place a file, and whether it builds the path above it is
		// the back end's choice (memFS does, localFS does not), which this suite
		// is not about. MkdirAll is part of what the fixture already requires.
		if err := fsys.MkdirAll(path.Dir(releaseFSName), fs.ModePerm); err != nil {
			t.Fatalf("MkdirAll(%q) = %v, want nil: the fixture writes %q and %q into it", path.Dir(releaseFSName), err, releaseFSName, releaseAgainName)
		}
		SeedReleaseFS(t, fsys)
		return
	}

	// Not a check given up on: a read-only file system with nothing to read has
	// not been seeded, which is a fault in its driver test and is reported as one.
	if _, err := fs.Stat(fsys, releaseFSName); err != nil {
		t.Fatalf("Stat(%q) before Release() = %v, want the file [SeedReleaseFS] puts in the file system being wrapped: a file system that refuses writes can only be seeded where its content is", releaseFSName, err)
	}
}

// assertReleaseSeed requires fsys to answer releaseFSName with the content it
// was seeded with. when says where in the case the read happened, and is empty
// for the read that comes before a release.
func assertReleaseSeed(t *testing.T, fsys releaseSeededTestFS, when string) {
	t.Helper()

	got, err := fs.ReadFile(fsys, releaseFSName)
	if err != nil {
		t.Fatalf("ReadFile(%q) %s= %v, want %q: [types.ReleaseFS] says Release leaves the file system usable, so only Close may take the content away", releaseFSName, when, err, releaseFSContent)
	}
	if string(got) != releaseFSContent {
		t.Errorf("ReadFile(%q) %s= %q, want %q: releasing lets go of resources, not of the file system's content", releaseFSName, when, got, releaseFSContent)
	}
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
