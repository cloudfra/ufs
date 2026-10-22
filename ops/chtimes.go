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

package ops

import (
	"errors"
	"fmt"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const chtimesOp = "chtimes"

// Chtimes changes the access and modification times of the file or directory
// name in fsys, like os.Chtimes. A zero [time.Time] leaves that time
// unchanged.
//
// If fsys implements [types.ChtimesFS], Chtimes calls it. Otherwise it falls
// back to rewriting the file's content through fsys (ReadFile, then Create,
// Write and Close), which works on any writable file system but has two
// limits: the file system's own Create stamps the new modification time, so
// the file ends up with the current time rather than mtime, and the rewrite
// reads and writes the whole file and is not atomic. The fallback ignores
// atime, does nothing when mtime is zero, and returns an error wrapping
// [errors.ErrUnsupported] for a directory.
//
// A missing file returns an error wrapping fs.ErrNotExist.
func Chtimes(fsys types.WriteFS, name string, atime, mtime time.Time) error {
	if err := pathutil.Validate(chtimesOp, name); err != nil {
		return err
	}
	if cfs, ok := fsys.(types.ChtimesFS); ok {
		return cfs.Chtimes(name, atime, mtime)
	}
	info, err := fsys.Stat(name)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return ufserrors.NewPathError(chtimesOp, name, fmt.Errorf("is a directory: %w", errors.ErrUnsupported))
	}
	if mtime.IsZero() {
		return nil
	}
	content, err := fsys.ReadFile(name)
	if err != nil {
		return err
	}
	f, err := fsys.Create(name)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		return ufserrors.Join(err, f.Close())
	}
	return f.Close()
}
