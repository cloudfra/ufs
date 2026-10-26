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

package polyfills

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
)

var (
	_ fs.FS              = (*renameFS)(nil)
	_ types.CreateFileFS = (*renameFS)(nil)
	_ types.MkdirAllFS   = (*renameFS)(nil)
	_ types.RemoveAllFS  = (*renameFS)(nil)

	_ types.CreateFileFS = (*renameRemoveFS)(nil)
	_ types.MkdirAllFS   = (*renameRemoveFS)(nil)
	_ types.RemoveFS     = (*renameRemoveFS)(nil)

	_ types.RemoveFS     = (*renameBothRemoversFS)(nil)
	_ types.RemoveAllFS  = (*renameBothRemoversFS)(nil)
	_ types.CreateFileFS = (*renameBothRemoversFS)(nil)

	_ types.CopyFileFS  = (*copyRenameFS)(nil)
	_ types.RemoveAllFS = (*copyRenameFS)(nil)

	_ types.OpenFileFS = (*openMkdirRenameFS)(nil)
	_ types.MkdirFS    = (*openMkdirRenameFS)(nil)

	_ types.CreateFileFS = (*noRemoveRenameFS)(nil)
	_ types.RemoveAllFS  = (*noWriteRenameFS)(nil)
	_ types.CreateFileFS = (*noMkdirRenameFS)(nil)

	_ types.RenameFileFS = (*nativeRenameFS)(nil)

	_ fs.ReadDirFile = (*renameableDir)(nil)
)

// errNoMetadata stands in for a listed child whose metadata cannot be read, the
// way an archive with a damaged directory entry would report it.
var errNoMetadata = errors.New("metadata is unreadable")

// seedRenameTree sets up the state a rename starts from: two files at the root,
// one file directly inside a directory and one a level deeper.
func seedRenameTree(s *store) {
	s.seedFile("a.txt", "alpha")
	s.seedFile("keep.txt", "keep")
	s.seedFile("dir/b.txt", "beta")
	s.seedFile("dir/sub/c.txt", "gamma")
}

// seedContents is the file set seedRenameTree holds, spelled out for the rows
// that must leave the tree alone.
func seedContents() map[string]string {
	return map[string]string{
		"a.txt":         "alpha",
		"keep.txt":      "keep",
		"dir/b.txt":     "beta",
		"dir/sub/c.txt": "gamma",
	}
}

// fileContents is the whole file set the store holds, content included, so one
// comparison says both that the copy has the source's bytes and that the
// original is gone.
func fileContents(s *store) map[string]string {
	contents := make(map[string]string, len(s.files))
	for name, data := range s.files {
		contents[name] = string(data)
	}
	return contents
}

// TestRename checks the emulation against a file system that has never heard of
// renaming, for files and for directories alike. The file set afterwards says
// what the move did, and the removal log says how: one call for a file system
// that can drop a subtree, a bottom-up walk for one that only has Remove.
func TestRename(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		wrap     func(*store, *removalLog) fs.FS
		seed     func(*store)
		seedDirs []string
		oldPath  string
		newPath  string
		// wantFiles is every file the store holds afterwards.
		wantFiles map[string]string
		// wantDirs is every directory it holds except the root.
		wantDirs []string
		// wantLog is the order the file system was asked to remove names, which is
		// either the original going away or a half-written copy being taken back.
		wantLog []string
		wantErr string
	}{
		{
			name:      "a file is moved",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   "moved.txt",
			wantFiles: map[string]string{"moved.txt": "alpha", "keep.txt": "keep", "dir/b.txt": "beta", "dir/sub/c.txt": "gamma"},
			wantDirs:  []string{"dir", "dir/sub"},
			wantLog:   []string{"a.txt"},
		},
		{
			name:      "a directory is moved",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			seed:      seedRenameTree,
			oldPath:   "dir",
			newPath:   "moved",
			wantFiles: map[string]string{"a.txt": "alpha", "keep.txt": "keep", "moved/b.txt": "beta", "moved/sub/c.txt": "gamma"},
			wantDirs:  []string{"moved", "moved/sub"},
			wantLog:   []string{"dir"},
		},
		{
			// A directory destination brings its own parents with it, because the
			// directory arm is MkdirAll.
			name:      "the parents of a directory destination are created",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			seed:      seedRenameTree,
			oldPath:   "dir",
			newPath:   "deep/new",
			wantFiles: map[string]string{"a.txt": "alpha", "keep.txt": "keep", "deep/new/b.txt": "beta", "deep/new/sub/c.txt": "gamma"},
			wantDirs:  []string{"deep", "deep/new", "deep/new/sub"},
			wantLog:   []string{"dir"},
		},
		{
			// Create makes no parents, so a file destination several levels deep fails
			// where a directory destination succeeds. This is the divergence the doc
			// comment calls out, kept rather than papered over with an MkdirAll that a
			// file system's own Create would not have done.
			name:      "the parents of a file destination are not created",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   "deep/deeper/moved.txt",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   "file does not exist",
		},
		{
			// OpenFile makes no parents either, so the divergence shows on the arm that
			// can carry the source's permission bits as well as on Create.
			name:      "the parents of a file destination are not created by OpenFile either",
			wrap:      func(s *store, l *removalLog) fs.FS { return &openMkdirRenameFS{store: s, log: l} },
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   "deep/deeper/moved.txt",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   "file does not exist",
		},
		{
			name:      "an empty directory is moved",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			seed:      seedRenameTree,
			seedDirs:  []string{"empty"},
			oldPath:   "empty",
			newPath:   "moved",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub", "moved"},
			wantLog:   []string{"empty"},
		},
		{
			name:      "a file is moved by a file system that can only Remove",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameRemoveFS{store: s, log: l} },
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   "moved.txt",
			wantFiles: map[string]string{"moved.txt": "alpha", "keep.txt": "keep", "dir/b.txt": "beta", "dir/sub/c.txt": "gamma"},
			wantDirs:  []string{"dir", "dir/sub"},
			wantLog:   []string{"a.txt"},
		},
		{
			// Remove refuses a directory that still has children, so the original of a
			// renamed directory is emptied deepest first and the directory itself goes
			// last.
			name:      "a directory is emptied from the bottom up by a file system that can only Remove",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameRemoveFS{store: s, log: l} },
			seed:      seedRenameTree,
			oldPath:   "dir",
			newPath:   "moved",
			wantFiles: map[string]string{"a.txt": "alpha", "keep.txt": "keep", "moved/b.txt": "beta", "moved/sub/c.txt": "gamma"},
			wantDirs:  []string{"moved", "moved/sub"},
			wantLog:   []string{"dir/b.txt", "dir/sub/c.txt", "dir/sub", "dir"},
		},
		{
			// A file system that can drop a subtree in one call must not be walked, even
			// though it also has Remove.
			name:      "RemoveAll wins over Remove",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameBothRemoversFS{store: s, log: l} },
			seed:      seedRenameTree,
			oldPath:   "dir",
			newPath:   "moved",
			wantFiles: map[string]string{"a.txt": "alpha", "keep.txt": "keep", "moved/b.txt": "beta", "moved/sub/c.txt": "gamma"},
			wantDirs:  []string{"moved", "moved/sub"},
			wantLog:   []string{"dir"},
		},
		{
			// Renaming onto a name that is taken is refused before anything is copied,
			// so the destination's content is still there.
			name:      "a destination that is taken is refused",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   "keep.txt",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   "already exists",
		},
		{
			// A name renamed onto itself reaches the same check, since the destination
			// is taken by the very name being moved. A native rename of a file onto
			// itself is a no-op on Unix; refusing it keeps the emulation from leaving
			// the content in two places when the removal half deletes it.
			name:      "a name renamed onto itself is refused",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   "a.txt",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   "already exists",
		},
		{
			// A directory renamed into itself would be copied into the copy of itself,
			// so the pair is refused before the query.
			name:      "a directory renamed into itself is refused",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			seed:      seedRenameTree,
			oldPath:   "dir",
			newPath:   "dir/sub/inner",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   "into itself",
		},
		{
			name:      "the root cannot be renamed",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			seed:      seedRenameTree,
			oldPath:   pathutil.CwdPath,
			newPath:   "moved",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   "permission denied",
		},
		{
			name:      "the root cannot be a destination",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   pathutil.CwdPath,
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   "permission denied",
		},
		{
			name:      "an invalid path is refused",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   "../escape",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   "is not a valid path",
		},
		{
			name:      "a source that is not there is reported",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			seed:      seedRenameTree,
			oldPath:   "ghost",
			newPath:   "moved",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   "file does not exist",
		},
		{
			// A source the file system will not answer for is not a source that is
			// missing, so the refusal is reported instead of the missing name.
			name: "a source the file system will not answer for is reported",
			wrap: func(s *store, l *removalLog) fs.FS {
				return &renameFS{store: s, log: l, listable: true, denyOn: "a.txt"}
			},
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   "moved.txt",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   "permission denied",
		},
		{
			// The same goes for the destination: an unanswered query is not a free pass
			// to write over whatever is there.
			name: "a destination the file system will not answer for is reported",
			wrap: func(s *store, l *removalLog) fs.FS {
				return &renameFS{store: s, log: l, listable: true, denyOn: "moved.txt"}
			},
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   "moved.txt",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   "permission denied",
		},
		{
			// The deletion half is probed first, so a file system that cannot delete is
			// refused before the copy rather than after it has put the same content in
			// two places.
			name:      "a file system with nothing to delete with is refused before anything is copied",
			wrap:      func(s *store, _ *removalLog) fs.FS { return &noRemoveRenameFS{store: s} },
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   "moved.txt",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   `does not support "rename"`,
		},
		{
			// The same file system can delete, it just has nothing to write through,
			// which only shows when the copy is attempted.
			name:      "a file system with nothing to write through is refused",
			wrap:      func(s *store, l *removalLog) fs.FS { return &noWriteRenameFS{store: s, log: l} },
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   "moved.txt",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   `does not support "copyfile"`,
		},
		{
			name:      "a directory with no directory call is refused",
			wrap:      func(s *store, l *removalLog) fs.FS { return &noMkdirRenameFS{store: s, log: l} },
			seed:      seedRenameTree,
			oldPath:   "dir",
			newPath:   "moved",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   `does not support "mkdirall"`,
		},
		{
			// A file system that only has Mkdir gets its parents the way the MkdirAll
			// standin makes them, one level at a time, so the parents a directory
			// destination needs come with it whichever directory call the file system
			// has.
			name:      "the parents of a directory destination are created through Mkdir too",
			wrap:      func(s *store, l *removalLog) fs.FS { return &openMkdirRenameFS{store: s, log: l} },
			seed:      seedRenameTree,
			oldPath:   "dir",
			newPath:   "deep/new",
			wantFiles: map[string]string{"a.txt": "alpha", "keep.txt": "keep", "deep/new/b.txt": "beta", "deep/new/sub/c.txt": "gamma"},
			wantDirs:  []string{"deep", "deep/new", "deep/new/sub"},
			wantLog:   []string{"dir"},
		},
		{
			// Without a way to list the source the copy cannot be made, so the
			// destination directory the copy had started is taken back and the original
			// stays whole.
			name:      "a source directory that cannot be listed is reported",
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l} },
			seed:      seedRenameTree,
			oldPath:   "dir",
			newPath:   "moved",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantLog:   []string{"moved"},
			wantErr:   "not implemented",
		},
		{
			// A listed child whose metadata cannot be read stops the copy for the whole
			// tree, which is what keeps a half-copied directory from being moved.
			name: "a listed child whose metadata cannot be read is reported",
			wrap: func(s *store, l *removalLog) fs.FS {
				return &renameFS{store: s, log: l, listable: true, infoErrOn: "dir/b.txt"}
			},
			seed:      seedRenameTree,
			oldPath:   "dir",
			newPath:   "moved",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantLog:   []string{"moved"},
			wantErr:   "metadata is unreadable",
		},
		{
			// A device node has no content that could be read back.
			name: "a mode that is neither a regular file nor a directory cannot be copied",
			seed: func(s *store) {
				seedRenameTree(s)
				s.seedFile("dev/x.bin", "raw")
				s.perms["dev/x.bin"] = fs.ModeDevice | 0o600
			},
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			oldPath:   "dev/x.bin",
			newPath:   "moved.bin",
			wantFiles: map[string]string{"a.txt": "alpha", "keep.txt": "keep", "dir/b.txt": "beta", "dir/sub/c.txt": "gamma", "dev/x.bin": "raw"},
			wantDirs:  []string{"dev", "dir", "dir/sub"},
			wantErr:   "unsupported operation",
		},
		{
			// A native copy that failed is left as the file system left it: only the
			// file system knows what its own copy managed to write, so the standin takes
			// back only the copies it made itself.
			name: "a failed native copy is left to the file system",
			wrap: func(s *store, l *removalLog) fs.FS {
				return &copyRenameFS{store: s, log: l, copyFailOn: "moved.txt", copyFailErr: errNoSpace}
			},
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   "moved.txt",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantErr:   "no space left on device",
		},
		{
			// The copy is what fails here, so it is taken back and nothing is lost. The
			// names come off in the order they were made, last first, which is what
			// lets even a file system that has only Remove empty what it made.
			name: "a copy that fails partway is taken back",
			seed: func(s *store) {
				seedRenameTree(s)
				s.writeErrOn = "moved/sub/c.txt"
			},
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			oldPath:   "dir",
			newPath:   "moved",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantLog:   []string{"moved/sub/c.txt", "moved/sub", "moved/b.txt", "moved"},
			wantErr:   "no space left on device",
		},
		{
			// Taking a copy back goes through the same deletion standin as the
			// original would, so a file system that only has Remove is walked here
			// too: the half-written file it was given is the one name that has to go.
			name: "a copy that fails partway is taken back by a file system that can only Remove",
			seed: func(s *store) {
				seedRenameTree(s)
				s.writeErrOn = "moved.txt"
			},
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameRemoveFS{store: s, log: l} },
			oldPath:   "a.txt",
			newPath:   "moved.txt",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantLog:   []string{"moved.txt"},
			wantErr:   "no space left on device",
		},
		{
			// The same failure one level deeper leaves the parent MkdirAll had to create
			// standing, since the copy only knows the names it was asked for.
			name: "a parent a MkdirAll created is left standing when the copy is taken back",
			seed: func(s *store) {
				seedRenameTree(s)
				s.writeErrOn = "deep/new/sub/c.txt"
			},
			wrap:      func(s *store, l *removalLog) fs.FS { return &renameFS{store: s, log: l, listable: true} },
			oldPath:   "dir",
			newPath:   "deep/new",
			wantFiles: seedContents(),
			wantDirs:  []string{"deep", "dir", "dir/sub"},
			wantLog:   []string{"deep/new/sub/c.txt", "deep/new/sub", "deep/new/b.txt", "deep/new"},
			wantErr:   "no space left on device",
		},
		{
			// Here the copy is done and the removal of the original is what fails, part
			// of the way through the original's own tree. The copy is kept: taking it
			// back as well would throw away the only complete copy of the tree there
			// now is.
			name: "a failed removal of a directory keeps the copy",
			wrap: func(s *store, l *removalLog) fs.FS {
				return &renameRemoveFS{store: s, log: l, failOn: "dir/sub", failErr: fs.ErrPermission}
			},
			seed:      seedRenameTree,
			oldPath:   "dir",
			newPath:   "moved",
			wantFiles: map[string]string{"a.txt": "alpha", "keep.txt": "keep", "moved/b.txt": "beta", "moved/sub/c.txt": "gamma"},
			wantDirs:  []string{"dir", "dir/sub", "moved", "moved/sub"},
			wantLog:   []string{"dir/b.txt", "dir/sub/c.txt", "dir/sub"},
			wantErr:   "permission denied",
		},
		{
			// A file cannot be half moved, so a failed removal of the original takes the
			// copy with it and only the original is left.
			name: "a failed removal of a file takes the copy with it",
			wrap: func(s *store, l *removalLog) fs.FS {
				return &renameFS{store: s, log: l, listable: true, failOn: "a.txt", failErr: fs.ErrPermission}
			},
			seed:      seedRenameTree,
			oldPath:   "a.txt",
			newPath:   "moved.txt",
			wantFiles: seedContents(),
			wantDirs:  []string{"dir", "dir/sub"},
			wantLog:   []string{"a.txt", "moved.txt"},
			wantErr:   "permission denied",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			log := &removalLog{}
			if tc.seed != nil {
				tc.seed(s)
			}
			for _, name := range tc.seedDirs {
				s.seedDir(name)
			}
			err := Rename(tc.wrap(s, log), tc.oldPath, tc.newPath)
			checkWantErr(t, "Rename", err, tc.wantErr)

			if got := fileContents(s); !reflect.DeepEqual(got, tc.wantFiles) {
				t.Errorf("Rename(%q, %q) left files %v, want %v", tc.oldPath, tc.newPath, got, tc.wantFiles)
			}
			if got := createdDirs(s); !reflect.DeepEqual(got, tc.wantDirs) {
				t.Errorf("Rename(%q, %q) left directories %v, want %v", tc.oldPath, tc.newPath, got, tc.wantDirs)
			}
			if !reflect.DeepEqual(log.names, tc.wantLog) {
				t.Errorf("Rename(%q, %q) removed %v, want %v", tc.oldPath, tc.newPath, log.names, tc.wantLog)
			}
		})
	}
}

// TestRenameNativeWins checks that a file system which can rename is asked to,
// and that the emulation is never reached: its listing, its writes and its
// removals all stay unused.
func TestRenameNativeWins(t *testing.T) {
	t.Parallel()

	t.Run("a file", func(t *testing.T) {
		t.Parallel()

		s := newStore()
		seedRenameTree(s)
		fsys := &nativeRenameFS{store: s}
		if err := Rename(fsys, "a.txt", "moved.txt"); err != nil {
			t.Fatalf("Rename() = %v, want nil", err)
		}
		wantCalls := []renameCall{{oldPath: "a.txt", newPath: "moved.txt"}}
		if !reflect.DeepEqual(fsys.calls, wantCalls) {
			t.Errorf("Rename() called Rename with %v, want %v", fsys.calls, wantCalls)
		}
		want := map[string]string{"moved.txt": "alpha", "keep.txt": "keep", "dir/b.txt": "beta", "dir/sub/c.txt": "gamma"}
		if got := fileContents(s); !reflect.DeepEqual(got, want) {
			t.Errorf("Rename() left files %v, want %v", got, want)
		}
		checkNoEmulation(t, s)
	})

	t.Run("a directory", func(t *testing.T) {
		t.Parallel()

		s := newStore()
		seedRenameTree(s)
		fsys := &nativeRenameFS{store: s}
		if err := Rename(fsys, "dir", "moved"); err != nil {
			t.Fatalf("Rename() = %v, want nil", err)
		}
		wantCalls := []renameCall{{oldPath: "dir", newPath: "moved"}}
		if !reflect.DeepEqual(fsys.calls, wantCalls) {
			t.Errorf("Rename() called Rename with %v, want %v", fsys.calls, wantCalls)
		}
		want := map[string]string{"a.txt": "alpha", "keep.txt": "keep", "moved/b.txt": "beta", "moved/sub/c.txt": "gamma"}
		if got := fileContents(s); !reflect.DeepEqual(got, want) {
			t.Errorf("Rename() left files %v, want %v", got, want)
		}
		wantDirs := []string{"moved", "moved/sub"}
		if got := createdDirs(s); !reflect.DeepEqual(got, wantDirs) {
			t.Errorf("Rename() left directories %v, want %v", got, wantDirs)
		}
		checkNoEmulation(t, s)
	})
}

// checkNoEmulation fails the test if any of the primitives the emulation would
// have used were touched.
func checkNoEmulation(t *testing.T, s *store) {
	t.Helper()

	if len(s.mkdirAllCalls) != 0 || len(s.mkdirCalls) != 0 || len(s.copyCalls) != 0 {
		t.Errorf("Rename() made mkdir calls %v and %v and copy calls %v, want none",
			s.mkdirCalls, s.mkdirAllCalls, s.copyCalls)
	}
	if s.openCalls != 0 || s.closeCalls != 0 {
		t.Errorf("Rename() opened %d and closed %d handles, want none", s.openCalls, s.closeCalls)
	}
}

// TestRenameUsesNativeCopyFile checks that a file system with CopyFileFS is
// asked to copy rather than having its bytes carried through a pair of handles,
// and that its own copy keeps the source's permissions.
func TestRenameUsesNativeCopyFile(t *testing.T) {
	t.Parallel()

	s := newStore()
	seedRenameTree(s)
	s.perms["a.txt"] = srcMode
	log := &removalLog{}
	fsys := &copyRenameFS{store: s, log: log}
	if err := Rename(fsys, "a.txt", "moved.txt"); err != nil {
		t.Fatalf("Rename() = %v, want nil", err)
	}
	wantCalls := []copyCall{{srcPath: "a.txt", dstPath: "moved.txt"}}
	if !reflect.DeepEqual(s.copyCalls, wantCalls) {
		t.Errorf("Rename() copied %v, want %v", s.copyCalls, wantCalls)
	}
	// openCalls counts the writable handles the store handed out, which is what a
	// copy through handles would have needed. The one closed handle is the query
	// that guards the destination, which no copy method can avoid.
	if s.openCalls != 0 {
		t.Errorf("Rename() opened %d writable handles, want none", s.openCalls)
	}
	if s.closeCalls != 1 {
		t.Errorf("Rename() closed %d handles, want the one the destination query needed", s.closeCalls)
	}
	if got := s.files["moved.txt"]; string(got) != "alpha" {
		t.Errorf("Rename() wrote %q at the destination, want %q", got, "alpha")
	}
	if got := s.perms["moved.txt"]; got != srcMode {
		t.Errorf("Rename() gave the destination mode %v, want the source's %v", got, srcMode)
	}
	wantLog := []string{"a.txt"}
	if !reflect.DeepEqual(log.names, wantLog) {
		t.Errorf("Rename() removed %v, want %v", log.names, wantLog)
	}
}

// TestRenameFileMode checks which copy arm the new file's mode came from:
// OpenFile is given the source's permission bits, Create takes none and so
// leaves the file system's own default.
func TestRenameFileMode(t *testing.T) {
	t.Parallel()

	t.Run("OpenFile carries the source's bits", func(t *testing.T) {
		t.Parallel()

		s := newStore()
		seedRenameTree(s)
		s.perms["a.txt"] = srcMode
		fsys := &openMkdirRenameFS{store: s, log: &removalLog{}}
		if err := Rename(fsys, "a.txt", "moved.txt"); err != nil {
			t.Fatalf("Rename() = %v, want nil", err)
		}
		if got := s.perms["moved.txt"]; got != srcMode {
			t.Errorf("Rename() gave the destination mode %v, want the source's %v", got, srcMode)
		}
	})

	t.Run("Create carries none", func(t *testing.T) {
		t.Parallel()

		s := newStore()
		seedRenameTree(s)
		s.perms["a.txt"] = srcMode
		fsys := &renameFS{store: s, log: &removalLog{}, listable: true}
		if err := Rename(fsys, "a.txt", "moved.txt"); err != nil {
			t.Fatalf("Rename() = %v, want nil", err)
		}
		if got := s.perms["moved.txt"]; got != 0 {
			t.Errorf("Rename() gave the destination mode %v, want the file system's default", got)
		}
	})
}

// TestRenameDirectoryMode checks that the recreated directories keep the source's
// permission bits, which the directory arm can be given.
func TestRenameDirectoryMode(t *testing.T) {
	t.Parallel()

	s := newStore()
	seedRenameTree(s)
	s.seedDir("under")
	s.perms["dir"] = 0o700
	s.perms["dir/sub"] = 0o755
	// The rename goes through a file system that only has Mkdir, where the MkdirAll
	// standin makes one level per call and leaves an ancestor that exists alone, so
	// each recreated directory is checked against its own source mode rather than
	// against the mode the deepest call happened to hand down.
	fsys := &openMkdirRenameFS{store: s, log: &removalLog{}}
	if err := Rename(fsys, "dir", "under/new"); err != nil {
		t.Fatalf("Rename() = %v, want nil", err)
	}
	wantModes := map[string]fs.FileMode{"under/new": 0o700, "under/new/sub": 0o755}
	for name, want := range wantModes {
		if got := s.perms[name]; got != want {
			t.Errorf("Rename() gave the directory %q mode %v, want the source's %v", name, got, want)
		}
	}
}

// TestRenameLinkError checks that the errors the emulation reports are the
// [*os.LinkError] values the native method promises, naming both paths and the
// operation.
func TestRenameLinkError(t *testing.T) {
	t.Parallel()

	s := newStore()
	seedRenameTree(s)
	fsys := &renameFS{store: s, log: &removalLog{}, listable: true}
	err := Rename(fsys, "a.txt", "keep.txt")
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) {
		t.Fatalf("Rename() = %v, want an [*os.LinkError]", err)
	}
	if linkErr.Op != "rename" || linkErr.Old != "a.txt" || linkErr.New != "keep.txt" {
		t.Errorf("Rename() reported %s %q -> %q, want rename %q -> %q",
			linkErr.Op, linkErr.Old, linkErr.New, "a.txt", "keep.txt")
	}
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("Rename() = %v, want an error wrapping [fs.ErrExist]", err)
	}
}

// renameCall records one native Rename call.
type renameCall struct {
	oldPath string
	newPath string
}

// renameFS is the emulation surface of a writable file system that has never
// heard of renaming: it writes through Create, recreates directories with
// MkdirAll and drops a subtree with RemoveAll.
type renameFS struct {
	*store
	log *removalLog
	// listable says whether its directory handles can be listed. The shared fakeDir
	// cannot, which is how the unlistable case is reached.
	listable bool
	// denyOn makes that one name unqueryable, the way a name the process may not
	// reach is: its open fails with [fs.ErrPermission] rather than [fs.ErrNotExist].
	denyOn string
	// infoErrOn makes that one name in a listing fail to report its metadata.
	infoErrOn string
	// failOn makes RemoveAll report failErr for that name.
	failOn  string
	failErr error
}

func (f *renameFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		if !f.listable {
			return f.open(name)
		}
		return &renameableDir{store: f.store, name: name, infoErrOn: f.infoErrOn}, nil
	}
	if name == f.denyOn {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return f.open(name)
}

func (f *renameFS) Create(name string) (types.File, error) {
	return f.create(name)
}

func (f *renameFS) MkdirAll(name string, perm fs.FileMode) error {
	return f.mkdirAll(name, perm)
}

func (f *renameFS) RemoveAll(name string) error {
	f.log.add(name)
	if name == f.failOn {
		return &fs.PathError{Op: "removeall", Path: name, Err: f.failErr}
	}
	return removeSubtree(f.store, name)
}

// renameRemoveFS has only [types.RemoveFS] to delete with, so the original of a
// renamed directory has to be emptied one node at a time. failOn makes that one
// name fail with failErr, and failErr says how: a nested child may be refused by
// the file system rather than simply be undeletable.
type renameRemoveFS struct {
	*store
	log     *removalLog
	failOn  string
	failErr error
}

func (f *renameRemoveFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &renameableDir{store: f.store, name: name}, nil
	}
	return f.open(name)
}

func (f *renameRemoveFS) Create(name string) (types.File, error) {
	return f.create(name)
}

func (f *renameRemoveFS) MkdirAll(name string, perm fs.FileMode) error {
	return f.mkdirAll(name, perm)
}

func (f *renameRemoveFS) Remove(name string) error {
	f.log.add(name)
	if name == f.failOn {
		return &fs.PathError{Op: "remove", Path: name, Err: f.failErr}
	}
	return removeNode(f.store, name)
}

// renameBothRemoversFS implements [types.RemoveFS] and [types.RemoveAllFS], so
// only the log can tell the single native call from the walk the standin would
// have needed.
type renameBothRemoversFS struct {
	*store
	log *removalLog
}

func (f *renameBothRemoversFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &renameableDir{store: f.store, name: name}, nil
	}
	return f.open(name)
}

func (f *renameBothRemoversFS) Create(name string) (types.File, error) {
	return f.create(name)
}

func (f *renameBothRemoversFS) MkdirAll(name string, perm fs.FileMode) error {
	return f.mkdirAll(name, perm)
}

func (f *renameBothRemoversFS) Remove(name string) error {
	f.log.add(name)
	return removeNode(f.store, name)
}

func (f *renameBothRemoversFS) RemoveAll(name string) error {
	f.log.add(name)
	return removeSubtree(f.store, name)
}

// copyRenameFS implements [types.CopyFileFS], so the copy half of the move is one
// call rather than a round trip per buffer. failOn makes that one destination fail
// with failErr.
type copyRenameFS struct {
	*store
	log         *removalLog
	copyFailOn  string
	copyFailErr error
}

func (f *copyRenameFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &renameableDir{store: f.store, name: name}, nil
	}
	return f.open(name)
}

func (f *copyRenameFS) CopyFile(srcPath, dstPath string) error {
	if dstPath == f.copyFailOn {
		return &fs.PathError{Op: "copyfile", Path: dstPath, Err: f.copyFailErr}
	}
	return f.copy(srcPath, dstPath)
}

func (f *copyRenameFS) Create(name string) (types.File, error) {
	return f.create(name)
}

func (f *copyRenameFS) MkdirAll(name string, perm fs.FileMode) error {
	return f.mkdirAll(name, perm)
}

func (f *copyRenameFS) RemoveAll(name string) error {
	f.log.add(name)
	return removeSubtree(f.store, name)
}

// openMkdirRenameFS writes through OpenFile and makes one directory level at a
// time, the pair a bare archive offers: OpenFile is the arm that can give the new
// file the source's permission bits, and Mkdir the arm that makes no parents.
type openMkdirRenameFS struct {
	*store
	log *removalLog
}

func (f *openMkdirRenameFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &renameableDir{store: f.store, name: name}, nil
	}
	return f.open(name)
}

func (f *openMkdirRenameFS) OpenFile(name string, flag int, perm fs.FileMode) (types.File, error) {
	return f.openFile(name, flag, perm)
}

func (f *openMkdirRenameFS) Mkdir(name string, perm fs.FileMode) error {
	return f.mkdir(name, perm)
}

func (f *openMkdirRenameFS) RemoveAll(name string) error {
	f.log.add(name)
	return removeSubtree(f.store, name)
}

// noRemoveRenameFS can write but has nothing to delete with, which is a file
// system whose names cannot be moved at all.
type noRemoveRenameFS struct {
	*store
}

func (f *noRemoveRenameFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &renameableDir{store: f.store, name: name}, nil
	}
	return f.open(name)
}

func (f *noRemoveRenameFS) Create(name string) (types.File, error) {
	return f.create(name)
}

func (f *noRemoveRenameFS) MkdirAll(name string, perm fs.FileMode) error {
	return f.mkdirAll(name, perm)
}

// noWriteRenameFS can delete but has nothing to write the copy through.
type noWriteRenameFS struct {
	*store
	log *removalLog
}

func (f *noWriteRenameFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &renameableDir{store: f.store, name: name}, nil
	}
	return f.open(name)
}

func (f *noWriteRenameFS) RemoveAll(name string) error {
	f.log.add(name)
	return removeSubtree(f.store, name)
}

// noMkdirRenameFS can copy a file and delete a tree but has no directory call,
// so it cannot recreate a directory at all.
type noMkdirRenameFS struct {
	*store
	log *removalLog
}

func (f *noMkdirRenameFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &renameableDir{store: f.store, name: name}, nil
	}
	return f.open(name)
}

func (f *noMkdirRenameFS) Create(name string) (types.File, error) {
	return f.create(name)
}

func (f *noMkdirRenameFS) RemoveAll(name string) error {
	f.log.add(name)
	return removeSubtree(f.store, name)
}

// nativeRenameFS implements [types.RenameFileFS], moving a name in one step the
// way a file system that keeps its own index would. The log of what it was asked
// is what separates it from the emulation.
type nativeRenameFS struct {
	*store
	calls []renameCall
}

func (f *nativeRenameFS) Open(name string) (fs.File, error) {
	return f.open(name)
}

func (f *nativeRenameFS) Rename(oldPath, newPath string) error {
	f.calls = append(f.calls, renameCall{oldPath: oldPath, newPath: newPath})
	if data, ok := f.files[oldPath]; ok {
		if _, taken := f.files[newPath]; taken {
			return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: fs.ErrExist}
		}
		delete(f.files, oldPath)
		f.files[newPath] = data
		f.perms[newPath] = f.perms[oldPath]
		delete(f.perms, oldPath)
		return nil
	}
	if !f.dirs[oldPath] {
		return &fs.PathError{Op: "rename", Path: oldPath, Err: fs.ErrNotExist}
	}
	moved := make([]string, 0, len(f.dirs))
	for name := range f.dirs {
		if name == oldPath || strings.HasPrefix(name, oldPath+"/") {
			moved = append(moved, name)
		}
	}
	// Shortest first, so that a directory exists before anything inside it is
	// moved into it.
	sort.Strings(moved)
	for _, name := range moved {
		target := newPath + strings.TrimPrefix(name, oldPath)
		delete(f.dirs, name)
		f.dirs[target] = true
		f.perms[target] = f.perms[name]
		delete(f.perms, name)
	}
	for _, name := range moved {
		prefix := name + "/"
		for file, data := range f.files {
			if !strings.HasPrefix(file, prefix) {
				continue
			}
			target := newPath + strings.TrimPrefix(file, oldPath)
			delete(f.files, file)
			f.files[target] = data
			f.perms[target] = f.perms[file]
			delete(f.perms, file)
		}
	}
	return nil
}

// renameableDir is the listable directory handle these fakes return: the shared
// fakeDir cannot be listed, so the copy half of a rename could never get past its
// first listing. infoErrOn makes one listed child fail to report its metadata.
type renameableDir struct {
	store *store
	name  string
	// readAt counts the children that earlier ReadDir calls already handed out.
	readAt    int
	infoErrOn string
}

func (d *renameableDir) Stat() (fs.FileInfo, error) {
	return fakeInfo{name: path.Base(d.name), mode: fs.ModeDir | dirMode(d.store, d.name)}, nil
}

// dirMode is the mode a seeded directory reports: a real file system has its own
// bits for every directory, while the seeded ones only have the package default.
func dirMode(s *store, name string) fs.FileMode {
	if perm := s.perms[name]; perm != 0 {
		return perm
	}
	return dirPerm
}

func (d *renameableDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: errors.New("is a directory")}
}

func (d *renameableDir) Close() error {
	return nil
}

func (d *renameableDir) ReadDir(n int) ([]fs.DirEntry, error) {
	children := directChildren(d.store, d.name)
	if d.readAt >= len(children) {
		if n > 0 {
			return nil, io.EOF
		}
		return nil, nil
	}
	end := len(children)
	if n > 0 && n < end-d.readAt {
		end = d.readAt + n
	}
	entries := make([]fs.DirEntry, 0, end-d.readAt)
	for _, child := range children[d.readAt:end] {
		name := child
		if !pathutil.IsCwd(d.name) {
			name = d.name + "/" + child
		}
		entries = append(entries, renameEntry{store: d.store, name: name, infoErr: name == d.infoErrOn})
	}
	d.readAt = end
	return entries, nil
}

// renameEntry is the [fs.DirEntry] a listed child is reported as.
type renameEntry struct {
	store   *store
	name    string
	infoErr bool
}

func (e renameEntry) Name() string {
	return path.Base(e.name)
}

func (e renameEntry) IsDir() bool {
	return e.store.dirs[e.name]
}

func (e renameEntry) Type() fs.FileMode {
	if e.IsDir() {
		return fs.ModeDir
	}
	return 0
}

func (e renameEntry) Info() (fs.FileInfo, error) {
	if e.infoErr {
		return nil, &fs.PathError{Op: "stat", Path: e.name, Err: errNoMetadata}
	}
	mode := e.store.perms[e.name]
	if e.IsDir() {
		mode = fs.ModeDir | dirMode(e.store, e.name)
	}
	return fakeInfo{name: path.Base(e.name), size: int64(len(e.store.files[e.name])), mode: mode}, nil
}
