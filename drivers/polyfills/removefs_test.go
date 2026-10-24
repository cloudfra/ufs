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
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// The fakes have to satisfy exactly the interfaces they claim, since which of
// them a file system implements is what decides the dispatch of the standin.
var (
	_ fs.ReadDirFile    = (*deletableDir)(nil)
	_ types.RemoveAllFS = (*removeAllOnlyFS)(nil)
	_ types.RemoveFS    = (*removeAllAndRemoveFS)(nil)
	_ types.RemoveAllFS = (*removeAllAndRemoveFS)(nil)
	_ types.RemoveAllFS = (*unlistableRemoveAllFS)(nil)
	_ types.RemoveAllFS = (*unqueryableRemoveAllFS)(nil)
)

// removeAllLog records the names a fake file system was asked to RemoveAll, which
// is how a test can tell the emulation from the file system's own Remove.
type removeAllLog struct {
	names []string
}

func (l *removeAllLog) add(name string) {
	l.names = append(l.names, name)
}

// seedDeletableTree sets up the state a removal starts from: a file at the root, a
// file directly inside a directory, a file one level deeper and an empty
// directory.
func seedDeletableTree(s *store) {
	s.seedFile("a.txt", "alpha")
	s.seedFile("dir/b.txt", "beta")
	s.seedFile("dir/sub/c.txt", "gamma")
	s.seedDir("empty")
}

// seedFullTree lists the state seedDeletableTree leaves behind, so a case that
// must change nothing can say so in one line.
func seedFullTree() []string {
	return []string{"a.txt", "dir", "dir/b.txt", "dir/sub", "dir/sub/c.txt", "empty"}
}

// childPaths lists the base names of the paths a directory holds, sorted the way
// [fs.ReadDir] returns them.
func childPaths(s *store, dir string) []string {
	prefix := dir + "/"
	if pathutil.IsCwd(dir) {
		prefix = ""
	}
	var names []string
	for name := range s.dirs {
		if name != dir && strings.HasPrefix(name, prefix) && !strings.Contains(name[len(prefix):], "/") {
			names = append(names, path.Base(name))
		}
	}
	for name := range s.files {
		if strings.HasPrefix(name, prefix) && !strings.Contains(name[len(prefix):], "/") {
			names = append(names, path.Base(name))
		}
	}
	sort.Strings(names)
	return names
}

// leftoverPaths lists every path the store still holds except the root, so a test
// can say what a removal left behind in one comparison.
func leftoverPaths(s *store) []string {
	var names []string
	for name := range s.dirs {
		if name != pathutil.CwdPath {
			names = append(names, name)
		}
	}
	for name := range s.files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// deleteSubtree drops name and everything below it in one step, saying nothing
// when the name is not there, as [types.RemoveAllFS] is documented to behave. The
// root is emptied rather than removed, since it is the mount rather than a node
// inside it.
func deleteSubtree(s *store, name string) error {
	if !fs.ValidPath(name) {
		return &fs.PathError{Op: "removeall", Path: name, Err: fs.ErrInvalid}
	}
	prefix := name + "/"
	if pathutil.IsCwd(name) {
		prefix = ""
	}
	for node := range s.dirs {
		if node == pathutil.CwdPath {
			continue
		}
		if node == name || strings.HasPrefix(node, prefix) {
			delete(s.dirs, node)
			delete(s.perms, node)
		}
	}
	for node := range s.files {
		if node == name || strings.HasPrefix(node, prefix) {
			delete(s.files, node)
			delete(s.perms, node)
		}
	}
	return nil
}

// deleteNode removes a single node and refuses a directory that still has
// children, as [types.RemoveFS] is documented to behave. It is what the fake that
// implements both interfaces uses for its own Remove.
func deleteNode(s *store, name string) error {
	if !fs.ValidPath(name) {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrInvalid}
	}
	if pathutil.IsCwd(name) {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrPermission}
	}
	if s.dirs[name] {
		if len(childPaths(s, name)) > 0 {
			return &fs.PathError{Op: "remove", Path: name, Err: ufserrors.ErrDirNotEmpty}
		}
		delete(s.dirs, name)
		delete(s.perms, name)
		return nil
	}
	if _, ok := s.files[name]; !ok {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrNotExist}
	}
	delete(s.files, name)
	delete(s.perms, name)
	return nil
}

// deletableDir is the directory handle these fakes hand out: the shared fakeDir
// cannot be listed, and the emulation has to read a directory to know whether it
// is empty enough to remove.
type deletableDir struct {
	store *store
	name  string
	// readAt counts the children that earlier ReadDir calls already handed out.
	readAt int
}

func (d *deletableDir) Stat() (fs.FileInfo, error) {
	return fakeInfo{name: path.Base(d.name), mode: fs.ModeDir | dirPerm}, nil
}

func (d *deletableDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: errors.New("is a directory")}
}

func (d *deletableDir) Close() error {
	return nil
}

func (d *deletableDir) ReadDir(n int) ([]fs.DirEntry, error) {
	children := childPaths(d.store, d.name)
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
		entries = append(entries, deletableEntry{store: d.store, name: name})
	}
	d.readAt = end
	return entries, nil
}

// deletableEntry is the [fs.DirEntry] a listed child is reported as.
type deletableEntry struct {
	store *store
	name  string
}

func (e deletableEntry) Name() string {
	return path.Base(e.name)
}

func (e deletableEntry) IsDir() bool {
	return e.store.dirs[e.name]
}

func (e deletableEntry) Type() fs.FileMode {
	if e.IsDir() {
		return fs.ModeDir
	}
	return 0
}

func (e deletableEntry) Info() (fs.FileInfo, error) {
	mode := e.store.perms[e.name]
	if e.IsDir() {
		mode = fs.ModeDir | dirPerm
	}
	return fakeInfo{name: path.Base(e.name), size: int64(len(e.store.files[e.name])), mode: mode}, nil
}

// removeAllOnlyFS implements [types.RemoveAllFS] and nothing else, the one
// primitive an emulated Remove can be built from.
type removeAllOnlyFS struct {
	*store
	log *removeAllLog
}

func (f *removeAllOnlyFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &deletableDir{store: f.store, name: name}, nil
	}
	return f.open(name)
}

func (f *removeAllOnlyFS) RemoveAll(name string) error {
	f.log.add(name)
	return deleteSubtree(f.store, name)
}

// removeAllAndRemoveFS implements [types.RemoveFS] and [types.RemoveAllFS], so an
// empty log is what shows the file system's own Remove ran rather than the
// emulation.
type removeAllAndRemoveFS struct {
	*store
	log *removeAllLog
}

func (f *removeAllAndRemoveFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &deletableDir{store: f.store, name: name}, nil
	}
	return f.open(name)
}

func (f *removeAllAndRemoveFS) Remove(name string) error {
	return deleteNode(f.store, name)
}

func (f *removeAllAndRemoveFS) RemoveAll(name string) error {
	f.log.add(name)
	return deleteSubtree(f.store, name)
}

// unlistableRemoveAllFS implements [types.RemoveAllFS] over directories it cannot
// list, the way an archive whose directory handles are opaque would be.
type unlistableRemoveAllFS struct {
	*store
	log *removeAllLog
}

func (f *unlistableRemoveAllFS) Open(name string) (fs.File, error) {
	return f.open(name)
}

func (f *unlistableRemoveAllFS) RemoveAll(name string) error {
	f.log.add(name)
	return deleteSubtree(f.store, name)
}

// unqueryableRemoveAllFS implements [types.RemoveAllFS] but cannot be queried at
// all, so the standin learns nothing about the name it is asked to remove.
type unqueryableRemoveAllFS struct {
	*store
	log *removeAllLog
}

func (f *unqueryableRemoveAllFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
}

func (f *unqueryableRemoveAllFS) RemoveAll(name string) error {
	f.log.add(name)
	return deleteSubtree(f.store, name)
}

func TestRemove(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		wrap   func(*store, *removeAllLog) fs.FS
		seed   func(*store)
		target string
		// wantLeft is what survives, in sorted order.
		wantLeft []string
		// wantLog is what the file system was asked to RemoveAll, which is empty
		// whenever the file system's own Remove did the work.
		wantLog []string
		wantErr string
	}{
		{
			// A file system that can remove one node knows the difference between an
			// empty directory and a full one itself, so its own method must win even
			// though RemoveAll is available too.
			name:     "native RemoveFS wins over the emulation",
			wrap:     func(s *store, l *removeAllLog) fs.FS { return &removeAllAndRemoveFS{store: s, log: l} },
			seed:     seedDeletableTree,
			target:   "a.txt",
			wantLeft: []string{"dir", "dir/b.txt", "dir/sub", "dir/sub/c.txt", "empty"},
		},
		{
			name:     "a file is removed by the emulation",
			wrap:     func(s *store, l *removeAllLog) fs.FS { return &removeAllOnlyFS{store: s, log: l} },
			seed:     seedDeletableTree,
			target:   "a.txt",
			wantLeft: []string{"dir", "dir/b.txt", "dir/sub", "dir/sub/c.txt", "empty"},
			wantLog:  []string{"a.txt"},
		},
		{
			name:     "an empty directory is removed by the emulation",
			wrap:     func(s *store, l *removeAllLog) fs.FS { return &removeAllOnlyFS{store: s, log: l} },
			seed:     seedDeletableTree,
			target:   "empty",
			wantLeft: []string{"a.txt", "dir", "dir/b.txt", "dir/sub", "dir/sub/c.txt"},
			wantLog:  []string{"empty"},
		},
		{
			// RemoveAll takes the subtree, which is exactly what Remove promises not
			// to do, so the directory has to survive the call.
			name:     "a directory with children is refused",
			wrap:     func(s *store, l *removeAllLog) fs.FS { return &removeAllOnlyFS{store: s, log: l} },
			seed:     seedDeletableTree,
			target:   "dir",
			wantLeft: seedFullTree(),
			wantErr:  "directory not empty",
		},
		{
			// RemoveAll answers nil for a name that is not there; Remove reports it.
			name:     "a name that is not there reports it is missing",
			wrap:     func(s *store, l *removeAllLog) fs.FS { return &removeAllOnlyFS{store: s, log: l} },
			seed:     seedDeletableTree,
			target:   "ghost",
			wantLeft: seedFullTree(),
			wantErr:  "file does not exist",
		},
		{
			// The root is the mount, and RemoveAll would empty it.
			name:     "the root is refused",
			wrap:     func(s *store, l *removeAllLog) fs.FS { return &removeAllOnlyFS{store: s, log: l} },
			seed:     seedDeletableTree,
			target:   pathutil.CwdPath,
			wantLeft: seedFullTree(),
			wantErr:  "permission denied",
		},
		{
			// An empty directory cannot be told from a full one without reading it, so
			// a directory that cannot be listed is left where it is.
			name:     "a directory that cannot be listed is refused",
			wrap:     func(s *store, l *removeAllLog) fs.FS { return &unlistableRemoveAllFS{store: s, log: l} },
			seed:     seedDeletableTree,
			target:   "empty",
			wantLeft: seedFullTree(),
			wantErr:  "not implemented",
		},
		{
			// Nothing is known about the name, so nothing is deleted on a guess.
			name:     "a name that cannot be queried is not removed",
			wrap:     func(s *store, l *removeAllLog) fs.FS { return &unqueryableRemoveAllFS{store: s, log: l} },
			seed:     seedDeletableTree,
			target:   "a.txt",
			wantLeft: seedFullTree(),
			wantErr:  "permission denied",
		},
		{
			name:     "no remove interface at all",
			wrap:     func(s *store, _ *removeAllLog) fs.FS { return plainFS{s} },
			seed:     seedDeletableTree,
			target:   "a.txt",
			wantLeft: seedFullTree(),
			wantErr:  `does not support "remove"`,
		},
		{
			name:     "an invalid path is refused",
			wrap:     func(s *store, l *removeAllLog) fs.FS { return &removeAllOnlyFS{store: s, log: l} },
			seed:     seedDeletableTree,
			target:   "../escape",
			wantLeft: seedFullTree(),
			wantErr:  "is not a valid path",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			log := &removeAllLog{}
			if tc.seed != nil {
				tc.seed(s)
			}
			err := Remove(tc.wrap(s, log), tc.target)
			checkWantErr(t, "Remove", err, tc.wantErr)

			if got := leftoverPaths(s); !reflect.DeepEqual(got, tc.wantLeft) {
				t.Errorf("Remove(%q) left %v, want %v", tc.target, got, tc.wantLeft)
			}
			if !reflect.DeepEqual(log.names, tc.wantLog) {
				t.Errorf("Remove(%q) called RemoveAll for %v, want %v", tc.target, log.names, tc.wantLog)
			}
		})
	}
}

// TestRemoveNestedEmptyDir checks the emptiness guard one level below the one the
// table reaches: the deepest directory is empty and goes, its parent still holds
// it and does not.
func TestRemoveNestedEmptyDir(t *testing.T) {
	t.Parallel()

	s := newStore()
	seedDeletableTree(s)
	s.seedDir("dir/sub/deep")
	log := &removeAllLog{}
	fsys := &removeAllOnlyFS{store: s, log: log}

	if err := Remove(fsys, "dir/sub/deep"); err != nil {
		t.Fatalf("Remove(dir/sub/deep) = %v, want nil", err)
	}
	if err := Remove(fsys, "dir/sub"); !errors.Is(err, ufserrors.ErrDirNotEmpty) {
		t.Errorf("Remove(dir/sub) = %v, want the directory not to be empty", err)
	}
	wantLeft := seedFullTree()
	if got := leftoverPaths(s); !reflect.DeepEqual(got, wantLeft) {
		t.Errorf("Remove() left %v, want %v", got, wantLeft)
	}
	if !reflect.DeepEqual(log.names, []string{"dir/sub/deep"}) {
		t.Errorf("Remove() called RemoveAll for %v, want %v", log.names, []string{"dir/sub/deep"})
	}
}
