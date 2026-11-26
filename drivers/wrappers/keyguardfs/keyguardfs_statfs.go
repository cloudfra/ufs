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

package keyguardfs

// The [fs.StatFS] half of a guard: both the stat that follows a link and the
// one that does not, which a guard answers the same way because a protected
// name is protected whichever way it is reached.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
)

func (fsys *keyGuardFS) Stat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("stat", name); err != nil {
		return nil, err
	}
	if fsys.protected(name) {
		return nil, fsys.denied("stat", name)
	}

	return fsys.inner.Stat(name)
}

func (fsys *keyGuardFS) Lstat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("lstat", name); err != nil {
		return nil, err
	}
	if fsys.protected(name) {
		return nil, fsys.denied("lstat", name)
	}

	return fsys.inner.Lstat(name)
}
