// Copyright 2026 Jeremy Edwards
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

//go:build linux

package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"path"
	"sync"
	"syscall"
	"time"

	fusefs "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// Not implemented FUSE operations (ufs has no support for these):
//   - Setattr  (chmod, chown, truncate, utimes)
//   - Rename with RENAME_EXCHANGE or RENAME_WHITEOUT
//   - Link     (hard links)
//   - Symlink
//   - Xattr    (getxattr, setxattr, listxattr, removexattr)
//   - Locking  (getlk, setlk, setlkw)
//   - Fallocate
//   - CopyFileRange

// roFuseNode is the read-only FUSE node interface: stat, lookup, directory
// listing, file open (read path only), and symlink resolution.
type roFuseNode interface {
	fusefs.NodeGetattrer
	fusefs.NodeLookuper
	fusefs.NodeReaddirer
	fusefs.NodeOpener
	fusefs.NodeReadlinker
}

// rwFuseNode extends roFuseNode with write operations: file creation, directory
// creation, file deletion, directory removal, and renaming.
type rwFuseNode interface {
	roFuseNode
	fusefs.NodeCreater
	fusefs.NodeMkdirer
	fusefs.NodeUnlinker
	fusefs.NodeRmdirer
	fusefs.NodeRenamer
}

// fuseFileHandler provides read, write, and release operations on an open file
// handle within the FUSE mount.
type fuseFileHandler interface {
	fusefs.FileReader
	fusefs.FileWriter
	fusefs.FileReleaser
}

// renameNoReplace is the rename flag RENAME_NOREPLACE, which go-fuse does not
// name.
const renameNoReplace = 0x1

var (
	fuseAttrTimeout = time.Second

	_ rwFuseNode      = (*fuseNode)(nil)
	_ fuseFileHandler = (*fuseFileHandle)(nil)
)

func mount(ctx context.Context, fsys types.ReadFS, mountPath string) (MountServer, error) {
	root := &fuseNode{fsys: fsys}

	// TODO: Accept mount options (e.g. forwarding the URI query parameter
	// "debug" to enable fuse.MountOptions.Debug) so callers can tune behavior.
	opts := &fusefs.Options{
		AttrTimeout:  &fuseAttrTimeout,
		EntryTimeout: &fuseAttrTimeout,
		MountOptions: fuse.MountOptions{
			FsName: fsys.String(),
			Name:   "ufs",
		},
	}

	server, err := fusefs.Mount(mountPath, root, opts)
	if err != nil {
		return nil, err
	}
	if err := server.WaitMount(); err != nil {
		return nil, err
	}

	hostServer := &fuseHostServer{server: server}
	go func() {
		<-ctx.Done()
		if err := hostServer.Close(); err != nil {
			slog.DebugContext(ctx, "fuse mount server is unmounting volume", "error", err)
		}
	}()

	return hostServer, nil
}

type fuseHostServer struct {
	server *fuse.Server

	// fuse.Server.Unmount is not safe to call from two goroutines, and both
	// Close and the context watcher in mount call it.
	unmountOnce sync.Once
	unmountErr  error
}

// Close unmounts the file system. It is safe to call more than once and from
// any goroutine; every call returns the result of the first.
func (s *fuseHostServer) Close() error {
	s.unmountOnce.Do(func() {
		s.unmountErr = s.server.Unmount()
	})
	return s.unmountErr
}

func (s *fuseHostServer) Wait() {
	s.server.Wait()
}

// fuseNode adapts a types.ReadFS path to go-fuse's InodeEmbedder. Its path is
// read from the inode tree rather than stored, so that it follows a rename of
// the node or of a directory above it.
type fuseNode struct {
	fusefs.Inode
	fsys types.ReadFS
}

// fsPath returns the path of n in fsys.
func (n *fuseNode) fsPath() string {
	if p := n.Path(nil); p != "" {
		return p
	}
	return pathutil.CwdPath
}

func (n *fuseNode) childPath(name string) string {
	return path.Join(n.fsPath(), name)
}

func (n *fuseNode) newChild(ctx context.Context, fi fs.FileInfo) *fusefs.Inode {
	child := &fuseNode{fsys: n.fsys}
	return n.NewPersistentInode(ctx, child, fusefs.StableAttr{Mode: fuseMode(fi.Mode())})
}

func (n *fuseNode) Getattr(_ context.Context, _ fusefs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	fi, err := n.fsys.Stat(n.fsPath())
	if err != nil {
		return fuseErrno(err)
	}
	fuseAttrFromFileInfo(fi, &out.Attr)
	return 0
}

func (n *fuseNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fusefs.Inode, syscall.Errno) {
	childPath := n.childPath(name)
	fi, err := n.fsys.Stat(childPath)
	if err != nil {
		return nil, fuseErrno(err)
	}
	child := n.newChild(ctx, fi)
	fuseAttrFromFileInfo(fi, &out.Attr)
	return child, 0
}

func (n *fuseNode) Readdir(_ context.Context) (fusefs.DirStream, syscall.Errno) {
	entries, err := n.fsys.ReadDir(n.fsPath())
	if err != nil {
		return nil, fuseErrno(err)
	}
	fuseEntries := make([]fuse.DirEntry, 0, len(entries))
	for _, e := range entries {
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		fuseEntries = append(fuseEntries, fuse.DirEntry{
			Name: e.Name(),
			Mode: fuseMode(info.Mode()),
		})
	}
	return fusefs.NewListDirStream(fuseEntries), 0
}

func (n *fuseNode) Open(_ context.Context, flags uint32) (fusefs.FileHandle, uint32, syscall.Errno) {
	if flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_TRUNC) != 0 {
		wfs, ok := n.fsys.(types.WriteFS)
		if !ok {
			return nil, 0, syscall.EROFS
		}
		// TODO: Support write-opens without O_TRUNC by introducing an
		// OpenFile/Append method on FS (see github.com/cloudfra/ufs/issues/218).
		if flags&syscall.O_TRUNC == 0 {
			return nil, 0, syscall.ENOTSUP
		}
		f, err := wfs.Create(n.fsPath())
		if err != nil {
			return nil, 0, fuseErrno(err)
		}
		return &fuseFileHandle{file: f}, 0, 0
	}
	f, err := n.fsys.Open(n.fsPath())
	if err != nil {
		return nil, 0, fuseErrno(err)
	}
	return &fuseFileHandle{file: f}, 0, 0
}

func (n *fuseNode) Readlink(_ context.Context) ([]byte, syscall.Errno) {
	target, err := n.fsys.ReadLink(n.fsPath())
	if err != nil {
		return nil, fuseErrno(err)
	}
	return []byte(target), 0
}

func (n *fuseNode) Create(ctx context.Context, name string, _ uint32, _ uint32, out *fuse.EntryOut) (*fusefs.Inode, fusefs.FileHandle, uint32, syscall.Errno) {
	wfs, ok := n.fsys.(types.WriteFS)
	if !ok {
		return nil, nil, 0, syscall.EROFS
	}
	childPath := n.childPath(name)
	f, err := wfs.Create(childPath)
	if err != nil {
		return nil, nil, 0, fuseErrno(err)
	}
	fi, statErr := n.fsys.Stat(childPath)
	if statErr != nil {
		if err := f.Close(); err != nil {
			slog.WarnContext(ctx, "error closing file", "error", err, "stat.error", statErr, "name", childPath)
		}
		return nil, nil, 0, fuseErrno(statErr)
	}
	child := n.newChild(ctx, fi)
	fuseAttrFromFileInfo(fi, &out.Attr)
	return child, &fuseFileHandle{file: f}, 0, 0
}

func (n *fuseNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fusefs.Inode, syscall.Errno) {
	wfs, ok := n.fsys.(types.WriteFS)
	if !ok {
		return nil, syscall.EROFS
	}
	childPath := n.childPath(name)
	if _, err := n.fsys.Stat(childPath); err == nil {
		return nil, syscall.EEXIST
	}
	// TODO: Introduce Mkdir (single-level) to the FS interface so that we
	// don't automatically create all intermediate directories.
	if err := wfs.MkdirAll(childPath, fs.FileMode(mode)); err != nil {
		return nil, fuseErrno(err)
	}
	fi, err := n.fsys.Stat(childPath)
	if err != nil {
		return nil, fuseErrno(err)
	}
	child := n.newChild(ctx, fi)
	fuseAttrFromFileInfo(fi, &out.Attr)
	return child, 0
}

func (n *fuseNode) Unlink(_ context.Context, name string) syscall.Errno {
	wfs, ok := n.fsys.(types.WriteFS)
	if !ok {
		return syscall.EROFS
	}
	return fuseErrno(wfs.Remove(n.childPath(name)))
}

func (n *fuseNode) Rmdir(_ context.Context, name string) syscall.Errno {
	wfs, ok := n.fsys.(types.WriteFS)
	if !ok {
		return syscall.EROFS
	}
	return fuseErrno(wfs.Remove(n.childPath(name)))
}

// Rename moves name to newName in newParent. Without RENAME_NOREPLACE an
// existing newName is replaced, as rename(2) does (see renameReplacing).
// RENAME_EXCHANGE and RENAME_WHITEOUT are not supported.
func (n *fuseNode) Rename(ctx context.Context, name string, newParent fusefs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	wfs, ok := n.fsys.(types.WriteFS)
	if !ok {
		return syscall.EROFS
	}
	parent, ok := newParent.(*fuseNode)
	if !ok {
		return syscall.EXDEV
	}
	oldPath, newPath := n.childPath(name), parent.childPath(newName)
	switch flags {
	case 0:
		return fuseErrno(renameReplacing(ctx, wfs, oldPath, newPath))
	case renameNoReplace:
		return fuseErrno(ufs.Rename(wfs, oldPath, newPath))
	default:
		return syscall.EINVAL
	}
}

// renameReplacing renames oldPath to newPath as rename(2) does: an existing
// newPath is replaced when it is a file and oldPath is too, or when it is an
// empty directory and oldPath is a directory. Renaming a path to itself does
// nothing.
//
// [ufs.Rename] never replaces, so an existing file is first renamed aside and
// removed once the rename succeeds, or renamed back if it fails, and an empty
// directory is removed and made again if the rename fails. Unlike rename(2)
// this is not atomic: newPath is missing for a moment.
func renameReplacing(ctx context.Context, fsys types.WriteFS, oldPath, newPath string) error {
	err := ufs.Rename(fsys, oldPath, newPath)
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	newInfo, statErr := fsys.Lstat(newPath)
	if statErr != nil {
		// Not an existing newPath, such as a parent of newPath that is a file.
		return err
	}
	oldInfo, statErr := fsys.Lstat(oldPath)
	if statErr != nil {
		return statErr
	}
	switch {
	case oldPath == newPath:
		return nil
	case oldInfo.IsDir() && !newInfo.IsDir():
		return syscall.ENOTDIR
	case !oldInfo.IsDir() && newInfo.IsDir():
		return syscall.EISDIR
	case newInfo.IsDir():
		entries, err := fsys.ReadDir(newPath)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return syscall.ENOTEMPTY
		}
		if err := fsys.Remove(newPath); err != nil {
			return err
		}
		if err := ufs.Rename(fsys, oldPath, newPath); err != nil {
			return ufserrors.Join(err, fsys.MkdirAll(newPath, newInfo.Mode().Perm()))
		}
		return nil
	}

	aside := path.Join(path.Dir(newPath), fmt.Sprintf(".%s.ufs-rename-%016x", path.Base(newPath), rand.Uint64())) //nolint:gosec // a unique name, not a secret
	if err := ufs.Rename(fsys, newPath, aside); err != nil {
		return err
	}
	if err := ufs.Rename(fsys, oldPath, newPath); err != nil {
		return ufserrors.Join(err, ufs.Rename(fsys, aside, newPath))
	}
	if err := fsys.Remove(aside); err != nil {
		// The rename itself succeeded, so it is not reported as failed.
		slog.WarnContext(ctx, "cannot remove the file replaced by a rename", "path", aside, "error", err)
	}
	return nil
}

// fuseFileHandle wraps a ufs file for go-fuse.
type fuseFileHandle struct {
	file fs.File
}

func (fh *fuseFileHandle) Read(_ context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if ra, ok := fh.file.(io.ReaderAt); ok {
		n, err := ra.ReadAt(dest, off)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fuseErrno(err)
		}
		return fuse.ReadResultData(dest[:n]), 0
	}
	if s, ok := fh.file.(io.Seeker); ok {
		if _, err := s.Seek(off, io.SeekStart); err != nil {
			return nil, fuseErrno(err)
		}
		n, err := fh.file.Read(dest)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fuseErrno(err)
		}
		return fuse.ReadResultData(dest[:n]), 0
	}
	if off != 0 {
		return nil, syscall.ENOTSUP
	}
	n, err := fh.file.Read(dest)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fuseErrno(err)
	}
	return fuse.ReadResultData(dest[:n]), 0
}

func (fh *fuseFileHandle) Write(_ context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	if wa, ok := fh.file.(io.WriterAt); ok {
		n, err := wa.WriteAt(data, off)
		return clampToUint32(n), fuseErrno(err)
	}
	if s, ok := fh.file.(io.Seeker); ok {
		if _, err := s.Seek(off, io.SeekStart); err != nil {
			return 0, fuseErrno(err)
		}
	}
	w, ok := fh.file.(io.Writer)
	if !ok {
		return 0, syscall.EBADF
	}
	n, err := w.Write(data)
	return clampToUint32(n), fuseErrno(err)
}

func (fh *fuseFileHandle) Release(_ context.Context) syscall.Errno {
	return fuseErrno(fh.file.Close())
}

func fuseErrno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	if errors.Is(err, fs.ErrNotExist) {
		return syscall.ENOENT
	}
	if errors.Is(err, fs.ErrPermission) {
		return syscall.EACCES
	}
	if errors.Is(err, fs.ErrExist) {
		return syscall.EEXIST
	}
	if errors.Is(err, fs.ErrInvalid) {
		return syscall.EINVAL
	}
	if errors.Is(err, fs.ErrClosed) {
		return syscall.EBADF
	}
	if errors.Is(err, ufserrors.ErrDirNotEmpty) {
		return syscall.ENOTEMPTY
	}
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return errno
	}
	return syscall.EIO
}

func fuseAttrFromFileInfo(fi fs.FileInfo, attr *fuse.Attr) {
	attr.Size = clampToUint64(fi.Size())
	attr.Mode = fuseMode(fi.Mode())
	mt := fi.ModTime()
	attr.SetTimes(&mt, &mt, &mt)
	if fi.IsDir() {
		attr.Nlink = 2
	} else {
		attr.Nlink = 1
		attr.Blocks = (attr.Size + 511) / 512
	}
}

// fuseMode converts Go's fs.FileMode to POSIX mode bits expected by go-fuse.
func fuseMode(m fs.FileMode) uint32 {
	mode := uint32(m.Perm())
	switch {
	case m.IsDir():
		mode |= syscall.S_IFDIR
	case m&fs.ModeSymlink != 0:
		mode |= syscall.S_IFLNK
	case m&fs.ModeNamedPipe != 0:
		mode |= syscall.S_IFIFO
	case m&fs.ModeSocket != 0:
		mode |= syscall.S_IFSOCK
	case m&fs.ModeDevice != 0:
		mode |= syscall.S_IFBLK
	case m&fs.ModeCharDevice != 0:
		mode |= syscall.S_IFCHR
	default:
		mode |= syscall.S_IFREG
	}
	if m&fs.ModeSetuid != 0 {
		mode |= syscall.S_ISUID
	}
	if m&fs.ModeSetgid != 0 {
		mode |= syscall.S_ISGID
	}
	if m&fs.ModeSticky != 0 {
		mode |= syscall.S_ISVTX
	}
	return mode
}
