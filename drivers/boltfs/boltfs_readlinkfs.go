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

//go:build !wasm

package boltfs

// ReadLink is the [fs.ReadLinkFS] half of [types.ReadFS]: no stored path is a symbolic link, so reading one is always refused.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *boltFS) ReadLink(name string) (string, error) {
	if fsys.isClosed() {
		return "", ufserrors.NewPathError("readlink", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("readlink", name); err != nil {
		return "", err
	}
	if _, err := fsys.statPath("readlink", name); err != nil {
		return "", err
	}
	// boltFS has no symlinks; every extant path is a regular file or directory.
	return "", ufserrors.NewPathError("readlink", name, fs.ErrInvalid)
}
