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

package readwrapfs

// Stat and Lstat are the [fs.StatFS] half of [types.ReadFS], falling back
// to the standard library when the wrapped file system does not answer them.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
)

func (fsys *readWrapFS) Stat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("stat", name); err != nil {
		return nil, err
	}
	if sfs, ok := fsys.fsys.(fs.StatFS); ok {
		return sfs.Stat(name)
	}
	return fs.Stat(fsys.fsys, name)
}

func (fsys *readWrapFS) Lstat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("lstat", name); err != nil {
		return nil, err
	}
	if rlfs, ok := fsys.fsys.(fs.ReadLinkFS); ok {
		return rlfs.Lstat(name)
	}
	return fsys.Stat(name)
}
