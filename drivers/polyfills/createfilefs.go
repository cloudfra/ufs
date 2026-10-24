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
	"fmt"
	"io/fs"
	"os"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const createFileOp = "create"

// createPerm is the mode the emulation asks for when it creates a file. Create
// takes no permission bits, so an emulated create has to choose the mode that a
// native one would have chosen for itself: this is the mode [os.Create] asks
// for, which a file system with a umask narrows the way the operating system
// narrows it. A file that is already there keeps the mode it has, since
// replacing its content does not say anything about who may read it.
const createPerm = fs.FileMode(0o666)

// Create opens a writable file at name, replacing any file that is already
// there, and keeps the contract of [types.CreateFileFS].
//
// fsys's own [types.CreateFileFS] implementation is used when it has one, since
// a file system knows better than a standin what mode to give a new file and how
// to report a name it cannot write to. Otherwise the file is opened with
// [types.OpenFileFS] using the flags that Create is documented to use. A file
// system that implements neither gets an unimplemented error: a file system that
// can only write whole files at once, as [types.BatchCreateFS] does, cannot hand
// back a writable handle, so it is not usable here.
//
// The emulation keeps the error contract of the native method. The root and any
// other name that is not a valid file name are refused with an error wrapping
// [fs.ErrInvalid] before anything is opened, and so is a name that already holds
// a directory, which would otherwise be handed back as a writable handle. Like
// [types.CreateFileFS], the emulation does not create missing parent
// directories and reports whatever the file system says about them, and a
// failure of the underlying OpenFile reaches the caller as that file system
// reports it.
func Create(fsys fs.FS, name string) (types.File, error) {
	if cFS, ok := fsys.(types.CreateFileFS); ok {
		return cFS.Create(name)
	}
	if err := pathutil.ValidateFileName(createFileOp, name); err != nil {
		return nil, err
	}
	// The write capability is probed before the name is queried, so a file
	// system with nothing to write with reports unimplemented without touching
	// the tree.
	if oFS, ok := fsys.(types.OpenFileFS); ok {
		return createViaOpenFileFS(fsys, oFS, name)
	}
	return nil, ufserrors.NewUnimplemented(fsys, createFileOp)
}

// createViaOpenFileFS opens the file with an [types.OpenFileFS] for a file
// system that has no Create. The flags are the ones Create is documented to
// use: writable, created when it is missing and truncated when it is there,
// which is what replacing any existing file means.
func createViaOpenFileFS(fsys fs.FS, wFS types.OpenFileFS, name string) (types.File, error) {
	info, err := fs.Stat(fsys, name)
	if err == nil && info.IsDir() {
		// A directory cannot hold the content the caller is about to write, so
		// it is refused here rather than returned as a handle that fails when
		// written to.
		isADir := fmt.Errorf("%q is a directory, %w", name, fs.ErrInvalid)
		return nil, ufserrors.NewPathError(createFileOp, name, isADir)
	}
	// A stat that fails for any other reason leaves the name unknown, which is
	// not a reason to refuse the create: the file system decides when the file
	// is opened.
	return wFS.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, createPerm)
}
