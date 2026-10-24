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
	_ fs.ReadDirFile    = (*removableDir)(nil)
	_ types.RemoveFS    = (*removeOnlyFS)(nil)
	_ types.RemoveAllFS = (*removeAllFS)(nil)
	_ types.RemoveFS    = (*removeAndRemoveAllFS)(nil)
	_ types.RemoveAllFS = (*removeAndRemoveAllFS)(nil)
	_ types.RemoveFS    = (*unlistableRemoveFS)(nil)
	_ types.RemoveFS    = (*deniedChildRemoveFS)(nil)
)

// removalLog records the names a fake file system was asked to remove, in the
// order it was asked. What was deleted says whether the removal worked; the order
// says whether the standin walked the tree or a native method did it in one call.
type removalLog struct {
	names []string
}

func (l *removalLog) add(name string) {
	l.names = append(l.names, name)
}

// seedRemovableTree sets up the state a removal starts from: a file at the root,
// a file directly inside a directory and a file one level deeper.
func seedRemovableTree(s *store) {
	s.seedFile("a.txt", "alpha")
	s.seedFile("dir/b.txt", "beta")
	s.seedFile("dir/sub/c.txt", "gamma")
}

// directChildren lists the base names of the paths a directory holds, sorted the
// way [fs.ReadDir] returns them.
func directChildren(s *store, dir string) []string {
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

// remainingPaths lists every path the store still holds except the root, so a
// test can say what a removal left behind in one comparison.
func remainingPaths(s *store) []string {
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

// removeNode deletes a single node, refusing a name that is not there and a
// directory that still has children, as [types.RemoveFS] is documented to behave.
func removeNode(s *store, name string) error {
	if !fs.ValidPath(name) {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrInvalid}
	}
	if pathutil.IsCwd(name) {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrPermission}
	}
	if s.dirs[name] {
		if len(directChildren(s, name)) > 0 {
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

// removeSubtree drops name and everything below it in one step, leaving no trace
// and saying nothing when the name is not there, as [types.RemoveAllFS] is
// documented to behave. The root is emptied rather than removed, since it is the
// mount rather than a node inside it.
func removeSubtree(s *store, name string) error {
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

// removableDir is the directory handle an emulated RemoveAll needs: the shared
// fakeDir cannot be listed, so a standin that has to empty a directory before it
// can remove it would only ever see a read failure.
type removableDir struct {
	store *store
	name  string
	// readAt counts the children that earlier ReadDir calls already handed out.
	readAt int
}

func (d *removableDir) Stat() (fs.FileInfo, error) {
	return fakeInfo{name: path.Base(d.name), mode: fs.ModeDir | dirPerm}, nil
}

func (d *removableDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: errors.New("is a directory")}
}

func (d *removableDir) Close() error {
	return nil
}

func (d *removableDir) ReadDir(n int) ([]fs.DirEntry, error) {
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
		entries = append(entries, removableEntry{store: d.store, name: name})
	}
	d.readAt = end
	return entries, nil
}

// removableEntry is the [fs.DirEntry] a listed child is reported as.
type removableEntry struct {
	store *store
	name  string
}

func (e removableEntry) Name() string {
	return path.Base(e.name)
}

func (e removableEntry) IsDir() bool {
	return e.store.dirs[e.name]
}

func (e removableEntry) Type() fs.FileMode {
	if e.IsDir() {
		return fs.ModeDir
	}
	return 0
}

func (e removableEntry) Info() (fs.FileInfo, error) {
	mode := e.store.perms[e.name]
	if e.IsDir() {
		mode = fs.ModeDir | dirPerm
	}
	return fakeInfo{name: path.Base(e.name), size: int64(len(e.store.files[e.name])), mode: mode}, nil
}

// removeOnlyFS implements [types.RemoveFS] and nothing else, the one primitive an
// emulated RemoveAll can be built from.
type removeOnlyFS struct {
	*store
	log *removalLog
}

func (f *removeOnlyFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &removableDir{store: f.store, name: name}, nil
	}
	return f.open(name)
}

func (f *removeOnlyFS) Remove(name string) error {
	f.log.add(name)
	return removeNode(f.store, name)
}

// removeAllFS implements [types.RemoveAllFS] and nothing else, dropping a whole
// subtree in the single call an object store's prefix delete would be.
type removeAllFS struct {
	*store
	log *removalLog
}

func (f *removeAllFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &removableDir{store: f.store, name: name}, nil
	}
	return f.open(name)
}

func (f *removeAllFS) RemoveAll(name string) error {
	f.log.add(name)
	return removeSubtree(f.store, name)
}

// removeAndRemoveAllFS implements [types.RemoveFS] and [types.RemoveAllFS], so
// only the log can tell the native RemoveAll from the walk the standin would do.
type removeAndRemoveAllFS struct {
	*store
	log *removalLog
}

func (f *removeAndRemoveAllFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &removableDir{store: f.store, name: name}, nil
	}
	return f.open(name)
}

func (f *removeAndRemoveAllFS) Remove(name string) error {
	f.log.add(name)
	return removeNode(f.store, name)
}

func (f *removeAndRemoveAllFS) RemoveAll(name string) error {
	f.log.add(name)
	return removeSubtree(f.store, name)
}

// unlistableRemoveFS implements [types.RemoveFS] over directories it cannot list,
// the way an archive whose directory handles are opaque would be.
type unlistableRemoveFS struct {
	*store
	log *removalLog
}

func (f *unlistableRemoveFS) Open(name string) (fs.File, error) {
	return f.open(name)
}

func (f *unlistableRemoveFS) Remove(name string) error {
	f.log.add(name)
	return removeNode(f.store, name)
}

func TestRemoveAll(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		wrap   func(*store, *removalLog) fs.FS
		seed   func(*store)
		target string
		// wantLeft is what survives, in sorted order; nil means the tree is empty
		// apart from the root.
		wantLeft []string
		// wantLog is the order the file system was asked to remove names, which is
		// what separates the walk from a native call.
		wantLog []string
		wantErr string
	}{
		{
			// A file system that can drop a subtree in one call knows how to do it
			// without a round trip per node, so its own method must win even though
			// the walk is available.
			name:     "native RemoveAllFS wins over the walk",
			wrap:     func(s *store, l *removalLog) fs.FS { return &removeAndRemoveAllFS{store: s, log: l} },
			seed:     seedRemovableTree,
			target:   "dir",
			wantLeft: []string{"a.txt"},
			wantLog:  []string{"dir"},
		},
		{
			name:     "a native RemoveAll of a file",
			wrap:     func(s *store, l *removalLog) fs.FS { return &removeAllFS{store: s, log: l} },
			seed:     seedRemovableTree,
			target:   "a.txt",
			wantLeft: []string{"dir", "dir/b.txt", "dir/sub", "dir/sub/c.txt"},
			wantLog:  []string{"a.txt"},
		},
		{
			// Remove only deletes an empty node, so the deepest paths have to go first
			// and the directory itself goes last.
			name:     "a subtree is taken apart from the bottom up",
			wrap:     func(s *store, l *removalLog) fs.FS { return &removeOnlyFS{store: s, log: l} },
			seed:     seedRemovableTree,
			target:   "dir",
			wantLeft: []string{"a.txt"},
			wantLog:  []string{"dir/b.txt", "dir/sub/c.txt", "dir/sub", "dir"},
		},
		{
			name:     "a file is removed",
			wrap:     func(s *store, l *removalLog) fs.FS { return &removeOnlyFS{store: s, log: l} },
			seed:     seedRemovableTree,
			target:   "a.txt",
			wantLeft: []string{"dir", "dir/b.txt", "dir/sub", "dir/sub/c.txt"},
			wantLog:  []string{"a.txt"},
		},
		{
			name:     "an empty directory is removed",
			wrap:     func(s *store, l *removalLog) fs.FS { return &removeOnlyFS{store: s, log: l} },
			seed:     func(s *store) { seedRemovableTree(s); s.seedDir("empty") },
			target:   "empty",
			wantLeft: []string{"a.txt", "dir", "dir/b.txt", "dir/sub", "dir/sub/c.txt"},
			wantLog:  []string{"empty"},
		},
		{
			// RemoveAll is documented as a no-op for a name that is not there, while
			// Remove reports it as an error, so the walk must not start at Remove.
			name:     "a name that is not there is a no-op",
			wrap:     func(s *store, l *removalLog) fs.FS { return &removeOnlyFS{store: s, log: l} },
			seed:     seedRemovableTree,
			target:   "ghost",
			wantLeft: []string{"a.txt", "dir", "dir/b.txt", "dir/sub", "dir/sub/c.txt"},
		},
		{
			// The root is emptied rather than removed: it is the mount, and Remove
			// refuses it, so the walk stops at its children.
			name:     "the root is emptied and left standing",
			wrap:     func(s *store, l *removalLog) fs.FS { return &removeOnlyFS{store: s, log: l} },
			seed:     seedRemovableTree,
			target:   pathutil.CwdPath,
			wantLeft: nil,
			wantLog:  []string{"a.txt", "dir/b.txt", "dir/sub/c.txt", "dir/sub", "dir"},
		},
		{
			// Without a way to list a directory the standin cannot empty it, so it
			// reports the read failure rather than deleting what it cannot see.
			name:     "a directory that cannot be listed is reported",
			wrap:     func(s *store, l *removalLog) fs.FS { return &unlistableRemoveFS{store: s, log: l} },
			seed:     seedRemovableTree,
			target:   "dir",
			wantLeft: []string{"a.txt", "dir", "dir/b.txt", "dir/sub", "dir/sub/c.txt"},
			wantErr:  "not implemented",
		},
		{
			// A nested file the file system will not let anyone open cannot even be
			// queried. That refusal is not the name being missing, so the walk stops
			// with the file system's error instead of emptying around it.
			name: "permission denied on a nested file that cannot be queried",
			wrap: func(s *store, l *removalLog) fs.FS {
				return &deniedChildRemoveFS{store: s, log: l, denyOn: "dir/sub/c.txt"}
			},
			seed:     seedRemovableTree,
			target:   "dir",
			wantLeft: []string{"a.txt", "dir", "dir/sub", "dir/sub/c.txt"},
			wantLog:  []string{"dir/b.txt"},
			wantErr:  "permission denied",
		},
		{
			// The same refusal can come from Remove itself, the way a file inside a
			// directory that cannot be unlinked would be.
			name: "permission denied on a nested file that cannot be removed",
			wrap: func(s *store, l *removalLog) fs.FS {
				return &failRemoveFS{store: s, log: l, failOn: "dir/b.txt", failErr: fs.ErrPermission}
			},
			seed:     seedRemovableTree,
			target:   "dir",
			wantLeft: []string{"a.txt", "dir", "dir/b.txt", "dir/sub", "dir/sub/c.txt"},
			wantLog:  []string{"dir/b.txt"},
			wantErr:  "permission denied",
		},
		{
			name:     "no remove interface at all",
			wrap:     func(s *store, _ *removalLog) fs.FS { return plainFS{s} },
			seed:     seedRemovableTree,
			target:   "dir",
			wantLeft: []string{"a.txt", "dir", "dir/b.txt", "dir/sub", "dir/sub/c.txt"},
			wantErr:  `does not support "removeall"`,
		},
		{
			name:     "an invalid path is refused",
			wrap:     func(s *store, l *removalLog) fs.FS { return &removeOnlyFS{store: s, log: l} },
			seed:     seedRemovableTree,
			target:   "../escape",
			wantLeft: []string{"a.txt", "dir", "dir/b.txt", "dir/sub", "dir/sub/c.txt"},
			wantErr:  "is not a valid path",
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
			err := RemoveAll(tc.wrap(s, log), tc.target)
			checkWantErr(t, "RemoveAll", err, tc.wantErr)

			if got := remainingPaths(s); !reflect.DeepEqual(got, tc.wantLeft) {
				t.Errorf("RemoveAll(%q) left %v, want %v", tc.target, got, tc.wantLeft)
			}
			if !reflect.DeepEqual(log.names, tc.wantLog) {
				t.Errorf("RemoveAll(%q) removed %v, want %v", tc.target, log.names, tc.wantLog)
			}
		})
	}
}

// TestRemoveAllNotAtomic checks what a walk that fails partway leaves behind: the
// nodes it already removed stay removed, since Remove cannot be rolled back.
func TestRemoveAllNotAtomic(t *testing.T) {
	t.Parallel()

	s := newStore()
	seedRemovableTree(s)
	fsys := &failRemoveFS{store: s, log: &removalLog{}, failOn: "dir/sub", failErr: errNoSpace}
	if err := RemoveAll(fsys, "dir"); !errors.Is(err, errNoSpace) {
		t.Fatalf("RemoveAll() = %v, want the error the file system reported", err)
	}
	// The children of dir/sub were already gone when its own removal failed, and
	// nothing above it was attempted, which is what a non-atomic walk looks like.
	want := []string{"a.txt", "dir", "dir/sub"}
	if got := remainingPaths(s); !reflect.DeepEqual(got, want) {
		t.Errorf("RemoveAll() left %v, want %v", got, want)
	}
}

// failRemoveFS implements [types.RemoveFS] but fails one name with failErr, so a
// walk stops in the middle of a subtree.
type failRemoveFS struct {
	*store
	log     *removalLog
	failOn  string
	failErr error
}

func (f *failRemoveFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &removableDir{store: f.store, name: name}, nil
	}
	return f.open(name)
}

func (f *failRemoveFS) Remove(name string) error {
	f.log.add(name)
	if name == f.failOn {
		return &fs.PathError{Op: "remove", Path: name, Err: f.failErr}
	}
	return removeNode(f.store, name)
}

// deniedChildRemoveFS implements [types.RemoveFS] over a tree in which one nested
// file cannot be queried at all: its open fails with [fs.ErrPermission] rather
// than [fs.ErrNotExist], which is how a file the process may not reach looks.
type deniedChildRemoveFS struct {
	*store
	log    *removalLog
	denyOn string
}

func (f *deniedChildRemoveFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &removableDir{store: f.store, name: name}, nil
	}
	if name == f.denyOn {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return f.open(name)
}

func (f *deniedChildRemoveFS) Remove(name string) error {
	f.log.add(name)
	return removeNode(f.store, name)
}
