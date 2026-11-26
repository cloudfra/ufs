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

package embedfs

// Stat and Lstat are the [fs.StatFS] half of [types.ReadFS]. An
// [embed.FS] holds no symbolic links, so Lstat is Stat with another op in
// its error.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *embedFS) Stat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("stat", name); err != nil {
		return nil, err
	}
	return fs.Stat(fsys.fsys, name)
}

func (fsys *embedFS) Lstat(name string) (fs.FileInfo, error) {
	// embed.FS contains no symlinks, so Lstat is identical to Stat.
	val, err := fsys.Stat(name)
	return ufserrors.ChangePathErrorOp(val, err, "lstat")
}
