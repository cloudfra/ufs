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
	"archive/zip"
	"bytes"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// mountedArchiveDirExt is the suffix of the virtual directory that holds an
// archive's content next to the archive itself, as [types.MountedArchiveDirFS]
// describes it.
const mountedArchiveDirExt = ".d"

// mountedArchiveDirDir is the directory the seeded cases work in, so that the
// archive names they use do not collide with another suite's fixtures.
const mountedArchiveDirDir = "a"

// mountedArchiveDirZip is a real archive written into the file system by the
// listing case, and mountedArchiveDirZipDir is the virtual directory next to it
// that a mounted archive gives its content.
const (
	mountedArchiveDirZip    = mountedArchiveDirDir + "/archive.zip"
	mountedArchiveDirZipDir = mountedArchiveDirZip + mountedArchiveDirExt
)

// mountedArchiveDirRealDir is a directory of the underlying file system whose
// name ends in the same suffix without being backed by an archive: there is no
// mountedArchiveDirRealBase file next to it.
const mountedArchiveDirRealDir = mountedArchiveDirDir + "/plain" + mountedArchiveDirExt

// mountedArchiveDirAbsent is an archive directory name whose archive is
// confirmed absent, which the interface says is reported false.
const mountedArchiveDirAbsent = mountedArchiveDirDir + "/absent.zip" + mountedArchiveDirExt

// mountedArchiveDirSeededFS is what the two seeded cases need: they build a
// tree and list it back, so they need the writing interfaces as well as the
// capability under test.
type mountedArchiveDirSeededFS interface {
	types.MountedArchiveDirFS
	types.CreateFileFS
	types.MkdirAllFS
	fs.ReadDirFS
}

// mountedArchiveDirFS runs the conformance suite for [types.MountedArchiveDirFS]
// against the file system returned by createFSFunc.
//
// The method only reads, so [Args.Bucket] does not matter to it. [Args.ReadOnly]
// matters only to the cases that need an archive to inspect: a read-only file
// system cannot be given one, so those cases report themselves as skipped rather
// than quietly passing.
//
// Two clauses of the interface are out of reach for these fixtures and go
// unchecked: that a file system which cannot tell whether the archive exists
// answers true, and that a file system wrapping another forwards the method to
// the one it wraps. The first is not a missing fixture but a disagreement
// between them: asked about a name whose archive part runs through a regular
// file, a memory-backed nest reads the Stat behind the answer as absent and
// says false, while a file-backed nest reads it as not a directory and says
// true, so no single expectation holds. The second needs a wrapper over a file
// system holding an archive, and the fixtures here that wrap another file
// system are read-only, where the cases that could see one skip.
// ArchiveInListing is the one case in which a fixture may decline to answer at
// all, and it logs that it did.
func mountedArchiveDirFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("MountedArchiveDirFS", func(t *testing.T) {
		requireType[types.MountedArchiveDirFS](t, args)
		t.Parallel()
		createMadFSFunc := requireFSTypeFunc[types.MountedArchiveDirFS](t, createFSFunc)
		createSeededFSFunc := requireFSTypeFunc[mountedArchiveDirSeededFS](t, createFSFunc)

		notInTree(t, createMadFSFunc)
		ordinaryNames(t, args, createSeededFSFunc)
		archiveInListing(t, args, createSeededFSFunc)
	})
}

// notInTree requires the answer for names that cannot be archive directories to
// be false: invalid paths, the root, and an archive directory whose archive the
// file system can see is not there.
func notInTree(t *testing.T, createFSFunc func(t *testing.T) types.MountedArchiveDirFS) {
	t.Run("NotInTree", func(t *testing.T) {
		t.Parallel()
		fsys := newMountedArchiveDirFS(t, createFSFunc)

		// The .d suffix is kept on the invalid paths so that a file system that
		// checks the suffix first still reaches the path check.
		names := []string{
			"/archive.zip" + mountedArchiveDirExt,
			"../archive.zip" + mountedArchiveDirExt,
			"invalid/../archive.zip" + mountedArchiveDirExt,
		}
		for _, name := range append(names, invalidPaths...) {
			if fsys.IsMountedArchiveDir(name) {
				t.Errorf("IsMountedArchiveDir(%q) = true, want false: the interface reports false for a path that does not satisfy [fs.ValidPath]", name)
			}
		}

		// The suffix on its own does satisfy [fs.ValidPath], so it is refused
		// for leaving nothing that could name an archive, not for its path.
		if fsys.IsMountedArchiveDir(mountedArchiveDirExt) {
			t.Errorf(`IsMountedArchiveDir(%q) = true, want false: removing the archive directory suffix leaves no archive name, so no archive can back this`, mountedArchiveDirExt)
		}

		if fsys.IsMountedArchiveDir(".") {
			t.Errorf(`IsMountedArchiveDir(".") = true, want false: the root of a file system is not an archive's content directory`)
		}

		if fsys.IsMountedArchiveDir(mountedArchiveDirAbsent) {
			t.Errorf("IsMountedArchiveDir(%q) = true, want false: the interface reports true only when it cannot tell whether the archive exists, and this one has no archive of that name", mountedArchiveDirAbsent)
		}
	})
}

// ordinaryNames requires directories and files that the file system itself
// holds to be reported as not archive directories, including a real directory
// whose name happens to end in the archive directory suffix.
func ordinaryNames(t *testing.T, args Args, createFSFunc func(t *testing.T) mountedArchiveDirSeededFS) {
	t.Run("OrdinaryNames", func(t *testing.T) {
		t.Parallel()
		if args.ReadOnly {
			t.Skip("the file system is declared read-only, so this case cannot create the tree it inspects")
		}
		fsys := newMountedArchiveDirSeededFS(t, createFSFunc)

		mkdirForTest(t, fsys, mountedArchiveDirDir)
		mkdirForTest(t, fsys, mountedArchiveDirRealDir)
		WriteFile(t, fsys, mountedArchiveDirZip, zipContent(t))

		for _, name := range []string{
			mountedArchiveDirDir,
			mountedArchiveDirRealDir,
			mountedArchiveDirZip,
			mountedArchiveDirDir + "/plain" + mountedArchiveDirExt + "/inside.txt",
		} {
			if fsys.IsMountedArchiveDir(name) {
				t.Errorf("IsMountedArchiveDir(%q) = true, want false: the interface reports whether a directory is backed by an archive, and this is a name of the file system's own", name)
			}
		}

		// The interface names the content directory by appending the suffix to
		// the archive name, so a name that only runs off the end of that rule
		// is not an archive directory even next to a real archive.
		if fsys.IsMountedArchiveDir(mountedArchiveDirZip + "d") {
			t.Errorf("IsMountedArchiveDir(%q) = true, want false: the content directory of %q is %q, made by appending %q to the archive name", mountedArchiveDirZip+"d", mountedArchiveDirZip, mountedArchiveDirZipDir, mountedArchiveDirExt)
		}
	})
}

// archiveInListing requires a file system that shows an archive's content
// directory in its listing to report it as one, which is what lets ops.Walk skip
// it instead of walking the same content twice.
func archiveInListing(t *testing.T, args Args, createFSFunc func(t *testing.T) mountedArchiveDirSeededFS) {
	t.Run("ArchiveInListing", func(t *testing.T) {
		t.Parallel()
		if args.ReadOnly {
			t.Skip("the file system is declared read-only, so this case cannot create the archive it inspects")
		}
		fsys := newMountedArchiveDirSeededFS(t, createFSFunc)

		mkdirForTest(t, fsys, mountedArchiveDirDir)
		WriteFile(t, fsys, mountedArchiveDirZip, zipContent(t))

		entries, err := fs.ReadDir(fsys, mountedArchiveDirDir)
		if err != nil {
			t.Fatalf("ReadDir(%q) = %v, want the listing that holds %q", mountedArchiveDirDir, err, mountedArchiveDirZip)
		}

		found := false
		for _, entry := range entries {
			if entry.Name() == "archive.zip"+mountedArchiveDirExt {
				found = true
				break
			}
		}
		if !found {
			// A file system that does not mount archives never lists such a
			// directory, and ops.Walk never asks about one it does not list, so
			// there is nothing to require of it here.
			t.Logf("ReadDir(%q) does not list %q, so this file system shows no archive directory to report", mountedArchiveDirDir, mountedArchiveDirZipDir)
			return
		}

		if !fsys.IsMountedArchiveDir(mountedArchiveDirZipDir) {
			t.Errorf("IsMountedArchiveDir(%q) = false, want true: %q lists it as the content directory of %q, and [types.MountedArchiveDirFS] exists so that a walker can tell it from a directory of the underlying file system", mountedArchiveDirZipDir, mountedArchiveDirDir, mountedArchiveDirZip)
		}

		if fsys.IsMountedArchiveDir(mountedArchiveDirZip) {
			t.Errorf("IsMountedArchiveDir(%q) = true, want false: the archive file is not its own content directory", mountedArchiveDirZip)
		}
	})
}

// newMountedArchiveDirFS builds a file system that answers for archive
// directories, for one sub-suite.
func newMountedArchiveDirFS(t *testing.T, createFS func(*testing.T) types.MountedArchiveDirFS) types.MountedArchiveDirFS {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	return fsys
}

// newMountedArchiveDirSeededFS builds a file system that can be written to and
// listed, for one sub-suite.
func newMountedArchiveDirSeededFS(t *testing.T, createFS func(*testing.T) mountedArchiveDirSeededFS) mountedArchiveDirSeededFS {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	return fsys
}

// zipContent returns the bytes of an archive that holds one file, written into
// the file system under test to give it something to mount.
func zipContent(tb testing.TB) string {
	tb.Helper()

	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	entry, err := archive.Create("inside.txt")
	if err != nil {
		tb.Fatalf("zip.NewWriter.Create = %v", err)
	}
	if _, err := entry.Write([]byte("inside")); err != nil {
		tb.Fatalf("write the archive entry = %v", err)
	}
	if err := archive.Close(); err != nil {
		tb.Fatalf("zip.Writer.Close = %v", err)
	}
	return buf.String()
}
