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

package ops

import (
	"io"
	"io/fs"
	"log/slog"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// Copy copies the single file at srcFilename in srcFS to destFilename in destFS.
// The parent directory of destFilename must already exist. The destination file
// is created (or truncated) via [types.WriteFS]. An error from closing the
// destination file is returned, since the copy may not have been persisted.
func Copy(srcFS fs.FS, srcFilename string, destFS fs.FS, destFilename string) error {
	if srcFS == destFS {
		if copyFS, ok := srcFS.(types.CopyFileFS); ok {
			return copyFS.CopyFile(srcFilename, destFilename)
		}
	}
	copyDestFS, ok := destFS.(types.CreateFileFS)
	if !ok {
		return ufserrors.NewUnimplemented(destFS, "createfile")
	}

	sfp, err := srcFS.Open(srcFilename)
	if err != nil {
		return err
	}
	defer func() {
		if err := sfp.Close(); err != nil {
			slog.Error("failed to close source file", "path", srcFilename, "error", err)
		}
	}()

	dfp, err := copyDestFS.Create(destFilename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dfp, sfp); err != nil {
		return ufserrors.Join(err, dfp.Close())
	}
	// Backends that buffer writes (object stores, databases) only persist the
	// data on Close, so a failed Close is a failed copy.
	return dfp.Close()
}
