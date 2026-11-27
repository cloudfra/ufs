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

package gcsfs

// ReadFile is the [fs.ReadFileFS] half of [types.ReadFS].

import (
	"io/fs"
	"log/slog"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

// ReadFile reads through Open, which makes the closed decision and the calls
// into the client under its own read lock. This method deliberately takes no
// lock of its own: RWMutex blocks a new reader while Close waits for the write
// lock, so a read lock held across the delegation would deadlock the pair.
func (fsys *gcsFS) ReadFile(name string) ([]byte, error) {
	if err := pathutil.Validate("readfile", name); err != nil {
		return nil, err
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("failed to close file", "path", name, "error", err)
		}
	}()
	gf := f.(*gcsFile)
	if gf.isDir {
		return nil, ufserrors.NewPathError("readfile", name, fs.ErrInvalid)
	}
	return gf.content, nil
}
