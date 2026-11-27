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

package ufs

// OpenFile is [types.OpenFileFS], which [types.FS] asks for beyond
// [types.WriteFS]: the flags are settled against the directory the nest holds,
// which the file system below knows nothing about.

import (
	"io/fs"
	"os"
	"path"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// isMountPoint reports whether a file system is mounted at name exactly, so
// that name is a directory of the file system holding the mount even when that
// file system's own base has no entry for it.
func (fsys *nestFS) isMountPoint(name string) bool {
	return fsys.mounts.getMount(name) != nil
}

// OpenFile opens name with the os.OpenFile flags in flag and the permission
// bits in perm, as the file system owning name carries them out. A file system
// without types.OpenFileFS gets the flags emulated with what it does offer.
//
// An open that lands on a directory this file system puts in the tree itself,
// either the root of a mounted file system or a directory with a mount somewhere
// below it, is settled against that directory here instead of below: it is a
// directory whatever the file system below has at that name, so a write flag
// reports [fs.ErrInvalid] and [os.O_CREATE] together with [os.O_EXCL] reports
// [fs.ErrExist], which keeps a mount from being replaced by a file. The
// exception is a name that resolves to a file system's own root, which is the
// nest itself or a mount point landing on the root of what it mounts: a root is
// not a file, so every flag is refused there with [fs.ErrInvalid], and
// [nestFS.Open] lists it.
func (fsys *nestFS) OpenFile(name string, flag int, perm fs.FileMode) (types.File, error) {
	if err := fsys.validPath("open", name); err != nil {
		return nil, err
	}

	mountFS, subName, err := fsys.getFSAndSubpath(name)
	if err != nil {
		return nil, err
	}

	// Three names describe a directory of this file system rather than a name the
	// file system below owns: a file system's own root, which is either the one
	// the nest wraps or a mount point landing on the root of what it mounts; a
	// directory with a mount somewhere below it, which the base may have no entry
	// for at all; and a mount point one level deeper, which only the mount map of
	// the mount holding it knows about. nestDirFS stands in for any of the three,
	// so the flags settle against a directory instead of against whatever the
	// file system below happens to hold at that name.
	ownDir := mountFS.isMountParent(subName) ||
		subName == pathutil.CwdPath ||
		mountFS.isMountPoint(subName)
	var f types.File
	if ownDir {
		f, err = polyfills.OpenFile(nestDirFS{fsys: mountFS, name: subName}, subName, flag, perm)
	} else {
		// The closed check and the open through the file system holding name
		// are one critical section with respect to that nest's Close.
		mountFS.mu.RLock()
		if mountFS.closed.Load() {
			mountFS.mu.RUnlock()
			return nil, ufserrors.NewPathError("open", name, fs.ErrClosed)
		}
		f, err = polyfills.OpenFile(mountFS.fsys, subName, flag, perm)
		mountFS.mu.RUnlock()
	}
	if err != nil {
		return nil, rewritePathError(err, name, subName)
	}
	if ownDir {
		// The listing already carries the mounts, and the handle already holds
		// the access mode the flags asked for, so nothing is left to add.
		return f, nil
	}

	// A directory opened by the file system below has to list this file
	// system's mounts too, which only the mount map here knows about, so its
	// listing is swapped for this file system's, as [nestFS.Open] does. The
	// polyfill already settled the access mode of the handle, and
	// [polyfills.OpenFile] cannot do this part: it has no mounts to look at.
	if rdf, ok := f.(fs.ReadDirFile); ok {
		info, statErr := f.Stat()
		if statErr == nil && info.IsDir() {
			return polyfills.ToFile(makeNestReadDirFile(mountFS, subName, rdf)), nil
		}
	}

	return polyfills.ToBufferedFile(f, flag&(os.O_WRONLY|os.O_RDWR) == 0, mountFS.args.BufMode)
}

var _ fs.StatFS = nestDirFS{}

// nestDirFS reports one of a nest's own directories to [polyfills.OpenFile],
// which settles the open flags against what it is given. A directory holding a
// mount, and the root of a mounted file system, is a directory here whatever
// the file system below has at that name, so it is described as one: a
// directory nothing may be created over.
type nestDirFS struct {
	fsys *nestFS
	name string
}

func (view nestDirFS) Stat(name string) (fs.FileInfo, error) {
	if name != view.name {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return types.NewDirEntry(path.Base(view.name)), nil
}

func (view nestDirFS) Open(name string) (fs.File, error) {
	if name != view.name {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return makeReadDirFile(view.fsys, view.name), nil
}
