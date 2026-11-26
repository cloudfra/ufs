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

package core

// ReadFile is the [fs.ReadFileFS] half of [types.ReadFS].

import (
	"bytes"
	"fmt"
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

func (fsys *memFS) ReadFile(name string) ([]byte, error) {
	if fsys.isClosed() {
		return nil, ufserrors.NewPathError("readfile", name, fs.ErrClosed)
	}
	if err := pathutil.Validate("readfile", name); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()
	node, ok := fsys.nodes[name]
	if !ok {
		return nil, ufserrors.NewPathError("readfile", name, fs.ErrNotExist)
	}
	if node.isDir {
		return nil, ufserrors.NewPathError("readfile", name, fmt.Errorf("is a directory: %w", fs.ErrInvalid))
	}
	return bytes.Clone(node.content), nil
}
