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

// Package embedfs wraps a Go embed.FS as a read-only ufs.FS.
//
// The implementation is split by the interface each method belongs to, one file
// per interface: this one holds the file-system value and what [types.SimpleFS]
// asks for, and the siblings named embedfs_<interface>.go hold the rest.
package embedfs

import (
	"embed"
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
)

var _ types.WriteFS = (*embedFS)(nil)

type embedFS struct {
	name string
	fsys embed.FS
}

func (fsys *embedFS) Open(name string) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	return fsys.fsys.Open(name)
}

func (fsys *embedFS) Close() error {
	return nil
}

// New wraps a Go [embed.FS] as a read-only [ufs.FS]. name is used as the
// label returned by [ufs.FS.String]; it is typically the mount path or a
// description of the embedded content. Read operations delegate directly to
// the embed.FS; all write operations return [fs.ErrPermission].
func New(name string, fsys embed.FS) types.WriteFS {
	return &embedFS{name: name, fsys: fsys}
}
