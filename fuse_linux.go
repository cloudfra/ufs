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

package ufs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"syscall"
	"time"

	fusefs "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Not implemented FUSE operations (ufs has no support for these):
//   - Setattr  (chmod, chown, truncate, utimes)
//   - Rename
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
// creation, file deletion, and directory removal.
type rwFuseNode interface {
	roFuseNode
	fusefs.NodeCreater
	fusefs.NodeMkdirer
	fusefs.NodeUnlinker
	fusefs.NodeRmdirer
}

// fuseFileHandler provides read, write, and release operations on an open file
// handle within the FUSE mount.
type fuseFileHandler interface {
	fusefs.FileReader
	fusefs.FileWriter
	fusefs.FileReleaser
}

var (
	fuseAttrTimeout = time.Second

	_ rwFuseNode      = (*fuseNode)(nil)
	_ fuseFileHandler = (*fuseFileHandle)(nil)
)

func hostMount(ctx context.Context, fsys ReadFS, mountPath string) (MountServer, error) {
	root := &fuseNode{
		fsys: fsys,
		path: cwdPath,
	}

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

	go func() {
		<-ctx.Done()
		_ = server.Unmount()
	}()

	return &fuseHostServer{server: server}, nil
}

type fuseHostServer struct {
	server *fuse.Server
}

func (s *fuseHostServer) Close() error {
	return s.server.Unmount()
}

func (s *fuseHostServer) Wait() {
	s.server.Wait()
}

// fuseNode adapts a ufs.ReadFS path to go-fuse's InodeEmbedder.
type fuseNode struct {
	fusefs.Inode
	fsys ReadFS
	path string
}

func (n *fuseNode) childPath(name string) string {
	return path.Join(n.path, name)
}

func (n *fuseNode) newChild(ctx context.Context, childPath string, fi fs.FileInfo) *fusefs.Inode {
	child := &fuseNode{
		fsys: n.fsys,
		path: childPath,
	}
	return n.NewPersistentInode(ctx, child, fusefs.StableAttr{Mode: fuseMode(fi.Mode())})
}

func (n *fuseNode) Getattr(ctx context.Context, fh fusefs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	fi, err := n.fsys.Stat(n.path)
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
	child := n.newChild(ctx, childPath, fi)
	fuseAttrFromFileInfo(fi, &out.Attr)
	return child, 0
}

func (n *fuseNode) Readdir(ctx context.Context) (fusefs.DirStream, syscall.Errno) {
	entries, err := n.fsys.ReadDir(n.path)
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

func (n *fuseNode) Open(ctx context.Context, flags uint32) (fusefs.FileHandle, uint32, syscall.Errno) {
	if flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_TRUNC) != 0 {
		wfs, ok := n.fsys.(FS)
		if !ok {
			return nil, 0, syscall.EROFS
		}
		// TODO: Support write-opens without O_TRUNC by introducing an
		// OpenFile/Append method on FS (see github.com/cloudfra/ufs/issues/218).
		if flags&syscall.O_TRUNC == 0 {
			return nil, 0, syscall.ENOTSUP
		}
		f, err := wfs.Create(n.path)
		if err != nil {
			return nil, 0, fuseErrno(err)
		}
		return &fuseFileHandle{file: f}, 0, 0
	}
	f, err := n.fsys.Open(n.path)
	if err != nil {
		return nil, 0, fuseErrno(err)
	}
	return &fuseFileHandle{file: f}, 0, 0
}

func (n *fuseNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	target, err := n.fsys.ReadLink(n.path)
	if err != nil {
		return nil, fuseErrno(err)
	}
	return []byte(target), 0
}

func (n *fuseNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fusefs.Inode, fusefs.FileHandle, uint32, syscall.Errno) {
	wfs, ok := n.fsys.(FS)
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
		_ = f.Close()
		return nil, nil, 0, fuseErrno(statErr)
	}
	child := n.newChild(ctx, childPath, fi)
	fuseAttrFromFileInfo(fi, &out.Attr)
	return child, &fuseFileHandle{file: f}, 0, 0
}

func (n *fuseNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fusefs.Inode, syscall.Errno) {
	wfs, ok := n.fsys.(FS)
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
	child := n.newChild(ctx, childPath, fi)
	fuseAttrFromFileInfo(fi, &out.Attr)
	return child, 0
}

func (n *fuseNode) Unlink(ctx context.Context, name string) syscall.Errno {
	wfs, ok := n.fsys.(FS)
	if !ok {
		return syscall.EROFS
	}
	return fuseErrno(wfs.Remove(n.childPath(name)))
}

func (n *fuseNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	wfs, ok := n.fsys.(FS)
	if !ok {
		return syscall.EROFS
	}
	return fuseErrno(wfs.Remove(n.childPath(name)))
}

// fuseFileHandle wraps a ufs file for go-fuse.
type fuseFileHandle struct {
	file fs.File
}

func (fh *fuseFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
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

func (fh *fuseFileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
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

func (fh *fuseFileHandle) Release(ctx context.Context) syscall.Errno {
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
