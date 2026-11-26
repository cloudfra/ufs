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

package core

// OpenFile is the [types.OpenFileFS] half of [types.FS], which this package asserts separately from [types.WriteFS].

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"time"

	"github.com/cloudfra/ufs/drivers/common/buffile"
	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// OpenFile opens name with the os.OpenFile flags in flag and the permission
// bits in perm, creating the file when os.O_CREATE asks for it and keeping the
// content already there when the flags do not truncate it. The rules follow what
// package os does on Linux, which is the platform the localFS driver measures
// against. The places where the two platforms this repository runs on disagree
// are named below.
//
// The access mode is the low two bits of flag — O_ACCMODE on Linux, which is
// what os.O_RDONLY|os.O_WRONLY|os.O_RDWR masks — so os.O_CREATE, os.O_EXCL,
// os.O_TRUNC and os.O_APPEND never change which methods the handle allows. The
// fourth value those two bits hold, os.O_WRONLY together with os.O_RDWR, names
// no mode: Linux opens it and hands back a descriptor that refuses every read
// and every write, and memFS gives a handle that refuses both directions.
//
// os.O_TRUNC clears the stored content whatever the access mode says, which is
// what Linux does: a read-only open truncates there too, and so does the value
// that names no mode. Package os refuses a read-only truncate on Windows, and
// the polyfill standing in for a file system without this interface keeps the
// content, so the shared conformance suite states no rule for the combination
// and memFS follows Linux.
//
// A new file keeps the permission bits it is asked for, unchanged: memFS has no
// umask, so no masking step is applied here or by Create, which has no
// permission argument and uses every bit set.
//
// The root is a directory like any other name here: a read-only open returns a
// listable handle, a writable open is refused as a directory, and
// os.O_CREATE|os.O_EXCL on an existing directory is refused as an existing name.
// A read-only open that carries os.O_TRUNC is refused as a directory too,
// because Linux counts the truncate as a write when it validates an open. Those
// are the answers Linux gives for a directory. The polyfill refuses the root
// outright because it can only emulate a file.
//
// A name that is missing gets its parent directories created, which is what
// Create does; a local open reports the parent is missing instead.
//
// Every handle holds a private copy of the content, and each write publishes
// that copy, so two open handles overwrite one another rather than each other's
// changes. This is how Create already behaves; it reaches OpenFile through the
// same handle. Linux keeps one file behind every descriptor, so this is where a
// memFS handle and a local handle diverge in content rather than only in the
// error they report.
func (fsys *memFS) OpenFile(name string, flag int, perm fs.FileMode) (types.File, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("open", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}

	access := flag & (os.O_RDONLY | os.O_WRONLY | os.O_RDWR)
	reads, writes := polyfills.OpenFileAccess(flag)
	// A directory is refused whenever the access bits ask for anything but a
	// plain read, including the value that names no mode, which is how open(2)
	// answers EISDIR. os.O_TRUNC is applied to a stored file on its own,
	// whatever these bits say, but it refuses a directory on its own too.
	writable := access != os.O_RDONLY
	now := time.Now()

	fsys.mu.Lock()
	node, existed := fsys.nodes[name]
	switch {
	case existed && flag&(os.O_CREATE|os.O_EXCL) == os.O_CREATE|os.O_EXCL:
		fsys.mu.Unlock()
		return nil, ufserrors.NewPathError("open", name, fs.ErrExist)
	case !existed && flag&os.O_CREATE == 0:
		fsys.mu.Unlock()
		return nil, ufserrors.NewPathError("open", name, fs.ErrNotExist)
	case existed && node.isDir:
		fsys.mu.Unlock()
		// The truncate is a write to the name even when the access bits ask
		// only to read, so a read-only open of a directory that carries
		// os.O_TRUNC is refused the way a writable one is.
		if writable || flag&os.O_TRUNC != 0 {
			return nil, ufserrors.NewPathError("open", name, fmt.Errorf("is a directory: %w", fs.ErrInvalid))
		}
		dir, err := fsys.openDir(name)
		if err != nil {
			return nil, err
		}
		return polyfills.ToFile(dir), nil
	case !existed:
		if dir := path.Dir(name); dir != pathutil.CwdPath {
			if err := fsys.checkDirsLocked(dir); err != nil {
				fsys.mu.Unlock()
				return nil, ufserrors.NewPathError("open", name, err)
			}
		}
		node = &memNode{
			name:    path.Base(name),
			mode:    perm,
			modTime: now,
		}
		fsys.nodes[name] = node
		fsys.ensureParentsLocked(name, now)
		fsys.notify(types.NotifyCreate, name)
	case flag&os.O_TRUNC != 0:
		node.content = nil
		node.modTime = now
		fsys.notify(types.NotifyWrite, name)
	}

	f := &memOpenFile{
		memFile: &memFile{
			fsys: fsys,
			File: buffile.New(name, bytes.Clone(node.content), node.mode, node.modTime),
		},
		reads:   reads,
		writes:  writes,
		appends: flag&os.O_APPEND != 0,
	}
	fsys.mu.Unlock()
	return f, nil
}
