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

import (
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"slices"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const batchCreateOp = "batchcreate"

// BatchCreate writes files to fsys as described by [types.BatchCreateFS]. It calls
// fsys.BatchCreate when fsys implements [types.BatchCreateFS]. Otherwise it checks
// every name first and then writes each file with MkdirAll and Create. That
// is not atomic: an error can leave the files before it written. Perm is not
// applied, since Create takes no permissions.
//
// Writing a single file with BatchCreate costs one operation on a file system
// that implements [types.BatchCreateFS], such as a single transaction, instead of
// one each for MkdirAll, Create and Close.
//
// The file systems that [New] returns do not implement [types.BatchCreateFS], as
// their mounts cannot be written atomically together. BatchCreate splits a
// batch for them by the mounted file system that holds each file, checking
// every file before writing any, and writes each part with that file
// system's BatchCreate when it has one, so each part is atomic on its own.
// The parts are written in the order of their first file, and an error stops
// the parts after it, while the parts before it stay written.
func BatchCreate(fsys types.WriteFS, files ...types.BatchFile) error {
	if b, ok := fsys.(types.BatchCreateFS); ok {
		return b.BatchCreate(files)
	}
	for _, file := range files {
		if err := pathutil.ValidateFileName(batchCreateOp, file.Name); err != nil {
			return err
		}
	}
	if nFS, ok := fsys.(*nestFS); ok {
		return nFS.batchCreate(files)
	}
	for _, file := range files {
		if err := createBatchFile(fsys, file); err != nil {
			return ufserrors.NewPathError(batchCreateOp, file.Name, unwrapPathError(err))
		}
	}
	return nil
}

// batchPart is the files of a batch that one mounted file system holds.
type batchPart struct {
	// fsys holds the files under their names in files.
	fsys *nestFS
	// files are named by their paths in fsys, and names are their paths in
	// the batch.
	files []types.BatchFile
	names []string
	// archives are the archives mounted for the files, which are stale once
	// the files are written.
	archives []mountLevel
}

// batchCreate writes files, whose names are valid, split by the mounted file
// system that holds each of them, as [BatchCreate] describes.
func (fsys *nestFS) batchCreate(files []types.BatchFile) error {
	var parts []*batchPart
	for _, file := range files {
		levels, err := fsys.mountLevels(file.Name)
		if err != nil {
			return ufserrors.NewPathError(batchCreateOp, file.Name, unwrapPathError(err))
		}
		if err := checkBatchLevels(levels); err != nil {
			return ufserrors.NewPathError(batchCreateOp, file.Name, err)
		}
		leaf := levels[len(levels)-1]
		i := slices.IndexFunc(parts, func(part *batchPart) bool { return part.fsys == leaf.fsys })
		if i < 0 {
			i = len(parts)
			parts = append(parts, &batchPart{fsys: leaf.fsys})
		}
		part := parts[i]
		part.files = append(part.files, types.BatchFile{Name: leaf.name, Data: file.Data, Perm: file.Perm})
		part.names = append(part.names, file.Name)
		for _, level := range levels {
			if level.fsys.mounts.getMount(level.name+archiveDirExt) != nil {
				part.archives = append(part.archives, mountLevel{level.fsys, level.name + archiveDirExt})
			}
		}
	}
	for _, part := range parts {
		if err := part.write(); err != nil {
			return err
		}
	}
	return nil
}

// checkBatchLevels checks that a file can be written at the path that levels
// resolves: not inside an archive, at a mount point, or where a mount below
// the path would hide it.
func checkBatchLevels(levels []mountLevel) error {
	leaf := levels[len(levels)-1]
	switch {
	case leaf.fsys.fsys == nil:
		return fs.ErrClosed
	case leaf.fsys.inArchive:
		return fmt.Errorf("archive content cannot be changed, %w", fs.ErrPermission)
	case pathutil.IsCwd(leaf.name):
		return fmt.Errorf("a file system is mounted here, %w", fs.ErrInvalid)
	}
	for _, level := range levels {
		for _, mountPath := range level.fsys.mounts.mountsUnder(level.name) {
			if !isArchiveMountPath(mountPath) {
				return fmt.Errorf("%q is mounted below, %w", mountPath, fs.ErrExist)
			}
		}
	}
	return nil
}

// write writes the files of part, in one BatchCreate when its file system
// has it, and then unmounts the archives that the files replaced.
func (part *batchPart) write() error {
	if b, ok := part.fsys.fsys.(types.BatchCreateFS); ok {
		if err := b.BatchCreate(part.files); err != nil {
			return renameBatchError(err, part.names, part.files)
		}
	} else {
		for i, file := range part.files {
			if err := createBatchFile(part.fsys.fsys, file); err != nil {
				return ufserrors.NewPathError(batchCreateOp, part.names[i], unwrapPathError(err))
			}
		}
	}
	for _, archive := range part.archives {
		// The batch itself succeeded, so a mount that cannot be unmounted here is
		// left for a later close rather than reported as a failed batch.
		if removeErr := archive.fsys.mounts.remove(archive.name); removeErr != nil {
			slog.Warn("cannot unmount after batch create", "path", archive.name, "error", removeErr)
		}
	}
	return nil
}

// renameBatchError returns err with the path of a file of files that it names
// replaced by the path of the same file in the batch, names[i].
func renameBatchError(err error, names []string, files []types.BatchFile) error {
	pathErr, ok := err.(*fs.PathError) //nolint:errorlint // Only the outermost error names the path.
	if !ok {
		return err
	}
	for i, file := range files {
		if file.Name == pathErr.Path {
			return ufserrors.NewPathError(pathErr.Op, names[i], pathErr.Err)
		}
	}
	return err
}

// createBatchFile writes file to fsys with MkdirAll and Create.
func createBatchFile(fsys types.WriteFS, file types.BatchFile) (err error) {
	if dir := path.Dir(file.Name); !pathutil.IsCwd(dir) {
		if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
			return err
		}
	}
	f, err := fsys.Create(file.Name)
	if err != nil {
		return err
	}
	defer func() {
		err = ufserrors.Join(err, f.Close())
	}()
	_, err = f.Write(file.Data)
	return err
}

// unwrapPathError returns the error inside err when err is an
// [*fs.PathError], so that wrapping it again does not repeat the path.
func unwrapPathError(err error) error {
	if pathErr, ok := err.(*fs.PathError); ok { //nolint:errorlint // Only the outermost error names the path.
		return pathErr.Err
	}
	return err
}
