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

// Package readwrapfs provides a read-only wrapper for standard library [fs.FS] file systems.
//
// The implementation is split by the interface each method belongs to, one file
// per interface: this one holds the file-system value and what [types.SimpleFS]
// asks for, and the siblings named readwrapfs_<interface>.go hold the rest.
package readwrapfs

import (
	"io"
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
)

var (
	_ types.ReadFS    = (*readWrapFS)(nil)
	_ types.WrapperFS = (*readWrapFS)(nil)
)

type readWrapFS struct {
	fsys fs.FS
}

func (fsys *readWrapFS) Open(name string) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	return fsys.fsys.Open(name)
}

func (fsys *readWrapFS) Close() error {
	if c, ok := fsys.fsys.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// FromFS wraps a standard library [fs.FS] as a read-only [ReadFS].
func FromFS(fsys fs.FS) types.ReadFS {
	return &readWrapFS{fsys: fsys}
}
