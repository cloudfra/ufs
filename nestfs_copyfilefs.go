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

// CopyFile is [types.CopyFileFS]: it copies a file inside the nest, within the
// mounted file system that holds both names when one holds both of them.

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/archive"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/ops"
)

// CopyFile copies the file at srcPath to dstPath. Paths are resolved through
// nested mounts the way [nestFS.Rename] resolves them: within one mounted file
// system the copy is made there, so a back end that can copy without moving the
// bytes keeps that, and between two the content is read from one and written
// into the other.
//
// It reports the errors [types.CopyFileFS] describes. It fails with
// [fs.ErrPermission] when srcPath only exists as a directory because a mount is
// below it, and with [fs.ErrExist] when dstPath only exists as such a directory,
// or is an archive whose content directory (dstPath plus ".d") already exists,
// which the copy would hide. A dstPath inside the content of a mounted archive
// is refused because that content is read-only. An archive mount left over by a
// copy onto its archive file is unmounted and mounted again when next opened.
func (fsys *nestFS) CopyFile(srcPath, dstPath string) error {
	if err := fsys.validPath(copyOp, srcPath); err != nil {
		return err
	}
	if err := fsys.validPath(copyOp, dstPath); err != nil {
		return err
	}
	linkErr := func(err error) error {
		return ufserrors.NewLinkError(copyOp, srcPath, dstPath, err)
	}

	src, err := fsys.pathLevels(srcPath)
	if err != nil {
		return linkErr(err)
	}
	dst, err := fsys.pathLevels(dstPath)
	if err != nil {
		return linkErr(err)
	}
	srcLeaf, dstLeaf := src[len(src)-1], dst[len(dst)-1]
	if srcLeaf.fsys.fsys == nil || dstLeaf.fsys.fsys == nil {
		return linkErr(fs.ErrClosed)
	}
	if pathutil.IsCwd(srcLeaf.name) || pathutil.IsCwd(dstLeaf.name) {
		// srcPath or dstPath is the root of a mount, which is a directory.
		return linkErr(fs.ErrPermission)
	}
	var unmount []pathLevel
	for _, level := range src {
		for _, mountPath := range level.fsys.mounts.mountsUnder(level.name) {
			// srcPath is a directory here because a mount is below it, so the
			// file that the mount hides is not the one the caller named.
			return linkErr(fmt.Errorf("%q is mounted, %w", mountPath, fs.ErrPermission))
		}
	}
	for _, level := range dst {
		for _, mountPath := range level.fsys.mounts.mountsUnder(level.name) {
			// dstPath is a directory here because a mount is below it, which
			// the file system that holds it does not know about.
			return linkErr(fmt.Errorf("%q is mounted, %w", mountPath, fs.ErrExist))
		}
		if level.fsys.mounts.getMount(level.name+archiveDirExt) != nil {
			// The copy replaces the archive file whose content is mounted at
			// level.name plus archiveDirExt, so that mount must be mounted again.
			unmount = append(unmount, pathLevel{level.fsys, level.name + archiveDirExt})
		}
	}
	if archive.IsMountablePath(dstLeaf.name) {
		contentDir := dstLeaf.name + archiveDirExt
		if _, err := dstLeaf.fsys.fsys.Lstat(contentDir); err == nil {
			return linkErr(fmt.Errorf("%q would be hidden by the archive, %w", contentDir, fs.ErrExist))
		} else if !errors.Is(err, fs.ErrNotExist) {
			return linkErr(err)
		}
	}

	// A copy stays native only when one file system holds both names.
	// types.SameFS compares the data behind the two nests, which their URIs
	// cannot do because two separate mounts of one driver can share a URI.
	if types.SameFS(srcLeaf.fsys.fsys, dstLeaf.fsys.fsys) {
		err = polyfills.CopyFile(srcLeaf.fsys.fsys, srcLeaf.name, dstLeaf.name)
	} else {
		err = copyFileBetweenMounts(srcLeaf.fsys.fsys, srcLeaf.name, dstLeaf.fsys.fsys, dstLeaf.name)
	}
	if err != nil {
		return linkErr(err)
	}
	for _, mount := range unmount {
		// The copy itself succeeded, so a mount that cannot be closed here is
		// left for a later close rather than reported as a failed copy.
		if removeErr := mount.fsys.mounts.remove(mount.name); removeErr != nil {
			slog.Warn("cannot unmount after copy", "path", mount.name, "error", removeErr)
		}
	}
	return nil
}

// copyFileBetweenMounts copies the file srcName in srcFS to dstName in dstFS,
// two different mounted file systems. [ops.Copy] would create the destination,
// so a name that is already taken is refused here first.
func copyFileBetweenMounts(srcFS fs.FS, srcName string, dstFS types.WriteFS, dstName string) error {
	if _, err := fs.Stat(dstFS, dstName); err == nil {
		return ufserrors.NewPathError(copyOp, dstName, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return ops.Copy(srcFS, srcName, dstFS, dstName)
}
