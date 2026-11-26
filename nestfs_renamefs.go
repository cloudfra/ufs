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

// Rename is [types.RenameFS]: it walks both paths down to the file system that
// holds them and remounts the archives that move with them.

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/archive"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/ops"
)

// Rename moves oldPath to newPath. Within one mounted file system it uses
// [ops.Rename] on that file system; between two it copies and then removes
// oldPath. Paths are resolved through nested mounts, including a nestFS
// mounted as a file system, down to the file system that holds them, so a
// copy never includes the mounts or archive content below oldPath.
//
// It fails with [fs.ErrPermission] for a mount point, a directory with a
// mount below it, or a path inside a mounted archive, and with [fs.ErrExist]
// when newPath is a directory that only exists because a mount is below it,
// or when newPath is an archive whose content directory (newPath plus ".d")
// already exists, which the archive would hide. Archives mounted at or below
// oldPath, or left mounted from a removed newPath, are unmounted and mounted
// again when next opened.
func (fsys *nestFS) Rename(oldPath, newPath string) error {
	if err := pathutil.ValidateRename(oldPath, newPath); err != nil {
		return err
	}
	if fsys.fsys == nil {
		return ufserrors.NewLinkError(renameOp, oldPath, newPath, fs.ErrClosed)
	}
	linkErr := func(err error) error {
		return ufserrors.NewLinkError(renameOp, oldPath, newPath, err)
	}

	src, err := fsys.renameLevels(oldPath)
	if err != nil {
		return linkErr(err)
	}
	dst, err := fsys.renameLevels(newPath)
	if err != nil {
		return linkErr(err)
	}
	srcLeaf, dstLeaf := src[len(src)-1], dst[len(dst)-1]
	if srcLeaf.fsys.fsys == nil || dstLeaf.fsys.fsys == nil {
		return linkErr(fs.ErrClosed)
	}
	if pathutil.IsCwd(srcLeaf.name) || pathutil.IsCwd(dstLeaf.name) {
		// oldPath or newPath is the root of a mount.
		return linkErr(fs.ErrPermission)
	}
	if srcLeaf.fsys.inArchive || dstLeaf.fsys.inArchive {
		return linkErr(fmt.Errorf("archive content cannot be changed, %w", fs.ErrPermission))
	}

	var unmount []renameLevel
	for _, level := range src {
		for _, mountPath := range level.fsys.mounts.mountsUnder(level.name) {
			if !isArchiveMountPath(mountPath) {
				return linkErr(fmt.Errorf("%q is mounted, %w", mountPath, fs.ErrPermission))
			}
			unmount = append(unmount, renameLevel{level.fsys, mountPath})
		}
		if level.fsys.mounts.getMount(level.name+archiveDirExt) != nil {
			unmount = append(unmount, renameLevel{level.fsys, level.name + archiveDirExt})
		}
	}
	for _, level := range dst {
		for _, mountPath := range level.fsys.mounts.mountsUnder(level.name) {
			if !isArchiveMountPath(mountPath) {
				return linkErr(fmt.Errorf("%q is mounted, %w", mountPath, fs.ErrExist))
			}
			// The rename fails if newPath exists, so on success an archive
			// mounted below it is left from a removed one.
			unmount = append(unmount, renameLevel{level.fsys, mountPath})
		}
		if level.fsys.mounts.getMount(level.name+archiveDirExt) != nil {
			unmount = append(unmount, renameLevel{level.fsys, level.name + archiveDirExt})
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

	// A rename stays native only when one file system holds both paths.
	// types.SameFS compares the data behind the two nests, which their URIs
	// cannot do because two separate mounts of one driver can share a URI.
	if types.SameFS(srcLeaf.fsys.fsys, dstLeaf.fsys.fsys) {
		err = ops.Rename(srcLeaf.fsys.fsys, srcLeaf.name, dstLeaf.name)
	} else {
		err = ops.RenameByCopy(srcLeaf.fsys.fsys, srcLeaf.name, dstLeaf.fsys.fsys, dstLeaf.name)
	}
	if err != nil {
		return linkErr(err)
	}
	for _, mount := range unmount {
		// The rename itself succeeded, so a mount that cannot be closed here is
		// left for a later close rather than reported as a failed rename.
		if removeErr := mount.fsys.mounts.remove(mount.name); removeErr != nil {
			slog.Warn("cannot unmount after rename", "path", mount.name, "error", removeErr)
		}
	}
	return nil
}

// renameLevel is a nestFS that a path passes through and the path within it.
type renameLevel struct {
	fsys *nestFS
	name string
}

// renameLevels returns each nestFS that name passes through, from fsys to the
// one whose file system holds name. Unlike getFSAndSubpath it also descends
// into a nestFS mounted as a file system (as [FSBuilder.MountFS] allows), so
// the file system of the last level is not a nestFS and has no mounts or
// archive directories of its own.
func (fsys *nestFS) renameLevels(name string) ([]renameLevel, error) {
	levels := []renameLevel{{fsys, name}}
	for current := fsys; ; {
		next, subName, err := current.getFSAndSubpath(name)
		if err != nil {
			return nil, err
		}
		if next != current {
			levels = append(levels, renameLevel{next, subName})
		}
		inner, ok := next.fsys.(*nestFS)
		if !ok {
			return levels, nil
		}
		levels = append(levels, renameLevel{inner, subName})
		current, name = inner, subName
	}
}
