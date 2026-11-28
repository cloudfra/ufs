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

package grpcfs

// The optional interfaces of drivers/types, each one rpc of the FS service
// except OpenFile, which has no handle on the wire to open and is answered
// here from Open and Create. The server answers an rpc through
// drivers/polyfills, so what a call returns is what the polyfill returns in
// the server's process: the served file system's own method, an emulation run
// on the server, or an error wrapping [errors.ErrUnsupported].

import (
	"io/fs"
	"time"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
)

var (
	_ types.FS              = (*grpcFS)(nil)
	_ types.ChmodFS         = (*grpcFS)(nil)
	_ types.ChownFS         = (*grpcFS)(nil)
	_ types.ChtimesFS       = (*grpcFS)(nil)
	_ types.ClosestParentFS = (*grpcFS)(nil)
	_ types.CopyFileFS      = (*grpcFS)(nil)
	_ types.LchownFS        = (*grpcFS)(nil)
	_ types.LinkFS          = (*grpcFS)(nil)
	_ types.MkdirFS         = (*grpcFS)(nil)
	_ types.SymlinkFS       = (*grpcFS)(nil)
	_ types.WriteFileFS     = (*grpcFS)(nil)
	_ types.Watcher         = (*grpcFS)(nil)
)

// Rename moves oldPath to newPath on the server, which never moves the content
// over the network even when the served file system has no rename of its own.
func (fsys *grpcFS) Rename(oldPath, newPath string) error {
	if err := pathutil.ValidateRename(oldPath, newPath); err != nil {
		return err
	}
	if fsys.closed.Load() {
		return ufserrors.NewLinkError("rename", oldPath, newPath, fs.ErrClosed)
	}
	_, err := fsys.client.Rename(fsys.ctx, &proto.RenameRequest{OldName: oldPath, NewName: newPath})
	if err != nil {
		return ufserrors.NewLinkError("rename", oldPath, newPath, fromStatus(err))
	}
	return nil
}

// Mkdir creates the directory name, whose parent must exist.
func (fsys *grpcFS) Mkdir(name string, perm fs.FileMode) error {
	return fsys.pathCall("mkdir", name, func() error {
		_, err := fsys.client.Mkdir(fsys.ctx, &proto.MkdirRequest{Name: name, Mode: uint32(perm)})
		return err
	})
}

// Chmod changes the mode of name.
func (fsys *grpcFS) Chmod(name string, mode fs.FileMode) error {
	return fsys.pathCall("chmod", name, func() error {
		_, err := fsys.client.Chmod(fsys.ctx, &proto.ChmodRequest{Name: name, Mode: uint32(mode)})
		return err
	})
}

// Chown changes the owner of name, following a trailing symbolic link.
func (fsys *grpcFS) Chown(name string, uid, gid int) error {
	return fsys.chown("chown", name, uid, gid, proto.SymlinkHandling_SYMLINK_HANDLING_FOLLOW)
}

// Lchown changes the owner of name without following a trailing symbolic link.
func (fsys *grpcFS) Lchown(name string, uid, gid int) error {
	return fsys.chown("lchown", name, uid, gid, proto.SymlinkHandling_SYMLINK_HANDLING_NO_FOLLOW)
}

func (fsys *grpcFS) chown(op, name string, uid, gid int, links proto.SymlinkHandling) error {
	return fsys.pathCall(op, name, func() error {
		_, err := fsys.client.Chown(fsys.ctx, &proto.ChownRequest{
			Name: name, Uid: int64(uid), Gid: int64(gid), SymlinkHandling: links,
		})
		return err
	})
}

// Chtimes changes the access and modification times of name. A zero time
// leaves that time alone, and travels as an unset one.
func (fsys *grpcFS) Chtimes(name string, atime, mtime time.Time) error {
	return fsys.pathCall("chtimes", name, func() error {
		_, err := fsys.client.Chtimes(fsys.ctx, &proto.ChtimesRequest{
			Name: name, AccessTime: toTimestamp(atime), ModificationTime: toTimestamp(mtime),
		})
		return err
	})
}

// Symlink creates newname as a symbolic link to oldname.
func (fsys *grpcFS) Symlink(oldname, newname string) error {
	return fsys.pathCall("symlink", newname, func() error {
		_, err := fsys.client.Symlink(fsys.ctx, &proto.SymlinkRequest{Target: oldname, Name: newname})
		return err
	})
}

// Link creates newname as a hard link to oldname.
func (fsys *grpcFS) Link(oldname, newname string) error {
	if err := pathutil.ValidateRename(oldname, newname); err != nil {
		return err
	}
	if fsys.closed.Load() {
		return ufserrors.NewLinkError("link", oldname, newname, fs.ErrClosed)
	}
	if _, err := fsys.client.Link(fsys.ctx, &proto.LinkRequest{OldName: oldname, NewName: newname}); err != nil {
		return ufserrors.NewLinkError("link", oldname, newname, fromStatus(err))
	}
	return nil
}

// CopyFile copies srcPath to dstPath on the server, so the content does not
// travel to the client and back. Either end being the root is refused with
// [fs.ErrPermission], as [types.CopyFileFS] asks.
func (fsys *grpcFS) CopyFile(srcPath, dstPath string) error {
	const op = "copy"
	if err := fsys.closedErr(op, srcPath); err != nil {
		return err
	}
	for _, name := range []string{srcPath, dstPath} {
		if err := pathutil.Validate(op, name); err != nil {
			return err
		}
		if name == pathutil.CwdPath {
			return ufserrors.NewPathError(op, name, fs.ErrPermission)
		}
	}
	_, err := fsys.client.CopyFile(fsys.ctx, &proto.CopyFileRequest{SourceName: srcPath, DestinationName: dstPath})
	if err != nil {
		return pathError(op, srcPath, err)
	}
	return nil
}

// ClosestParent returns the closest node at or above name that exists, in one
// round trip.
func (fsys *grpcFS) ClosestParent(name string) (string, bool, error) {
	if err := fsys.closedErr("closestparent", name); err != nil {
		return "", false, err
	}
	if err := pathutil.ValidateFileName("closestparent", name); err != nil {
		return "", false, err
	}
	resp, err := fsys.client.ClosestParent(fsys.ctx, &proto.ClosestParentRequest{Name: name})
	if err != nil {
		return "", false, pathError("closestparent", name, err)
	}
	return resp.GetName(), resp.GetIsDir(), nil
}

// OpenFile opens name with the generalized flags of [os.OpenFile]. The service
// carries no handle the server holds open, so this is the polyfill over Open
// and Create: a read-only open reads through the ReadFile rpc, a create or a
// truncating write uploads through the WriteFile rpc, and a write that would
// keep the existing content (no O_TRUNC) is refused with an error wrapping
// [errors.ErrUnsupported], because the service has no partial write.
func (fsys *grpcFS) OpenFile(name string, flag int, perm fs.FileMode) (types.File, error) {
	if err := fsys.closedErr("openfile", name); err != nil {
		return nil, err
	}
	return polyfills.OpenFile(writeFSOnly{fsys}, name, flag, perm)
}

// writeFSOnly hides every method of grpcFS but those of types.WriteFS, so the
// polyfill OpenFile is handed builds the handle from Open and Create rather
// than calling back into grpcFS.OpenFile.
type writeFSOnly struct{ types.WriteFS }

// pathCall validates name, refuses a closed file system, and runs one rpc
// whose error is reported as a *fs.PathError for name.
func (fsys *grpcFS) pathCall(op, name string, call func() error) error {
	if err := fsys.closedErr(op, name); err != nil {
		return err
	}
	if err := pathutil.Validate(op, name); err != nil {
		return err
	}
	if err := call(); err != nil {
		return pathError(op, name, err)
	}
	return nil
}
