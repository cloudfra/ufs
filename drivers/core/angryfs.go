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

import (
	"context"
	"io/fs"
	"strings"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const (
	angryFSPrefix = "angry:"
)

var (
	_ types.WriteFS = (*angryFS)(nil)
	_ fs.GlobFS     = (*angryFS)(nil)

	errAngry = fs.ErrInvalid

	angryDeviceInfo    = types.NewDeviceInfo("angry", "angry", 1, false)
	angryDeviceInfoMap = types.NewDeviceMap(angryDeviceInfo)
)

func init() {
	ufs.Register(ufs.NewDriver("angry", newAngryFS, isAngryFSUri, 1, false, false))
}

type angryFS struct {
	name string
}

func (fsys *angryFS) Open(name string) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	return nil, ufserrors.NewPathError("open", name, errAngry)
}

func (fsys *angryFS) Close() error {
	return errAngry
}

func newAngryFS(_ context.Context, name string) (types.WriteFS, error) {
	return makeAngryFS(name), nil
}

// MakeAngryFS returns an angry file system named name, whose operations
// always fail.
//
// Deprecated: MakeAngryFS exists only to support the shared driver tests in
// drivers/testing and will be removed soon. Use New instead.
func MakeAngryFS(name string) types.WriteFS {
	return makeAngryFS(name)
}

func makeAngryFS(name string) *angryFS {
	return &angryFS{
		name: name,
	}
}

func isAngryFSUri(name string) bool {
	return strings.HasPrefix(name, angryFSPrefix)
}
