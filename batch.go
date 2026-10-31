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
	"io/fs"
	"path"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const batchCreateOp = "batchcreate"

// BatchCreate writes files to fsys as described by [BatchCreateFS]. It calls
// fsys.BatchCreate when fsys implements [BatchCreateFS]. Otherwise it checks
// every name first and then writes each file with MkdirAll and Create. That
// is not atomic: an error can leave the files before it written. Perm is not
// applied, since Create takes no permissions. A batch with no files writes
// nothing.
//
// Writing a single file with BatchCreate costs one operation on a file system
// that implements [BatchCreateFS], such as a single transaction, instead of
// one each for MkdirAll, Create and Close.
func BatchCreate(fsys types.WriteFS, files ...types.BatchFile) error {
	if b, ok := fsys.(types.BatchCreateFS); ok {
		return b.BatchCreate(files)
	}
	for _, file := range files {
		if err := pathutil.ValidateFileName(batchCreateOp, file.Name); err != nil {
			return err
		}
	}
	for _, file := range files {
		if err := createBatchFile(fsys, file); err != nil {
			return ufserrors.NewPathError(batchCreateOp, file.Name, unwrapPathError(err))
		}
	}
	return nil
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
