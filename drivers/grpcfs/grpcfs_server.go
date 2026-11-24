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

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var (
	_ types.FS = (*grpcServerFS)(nil)
)

// grpcServerFS will implement types.FS.
type grpcServerFS struct {
	name string
}

func (fsys *grpcServerFS) Open(name string) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	return nil, ufserrors.NewUnimplemented(fsys, "open")
}

func (fsys *grpcServerFS) Close() error {
	return nil
}

func makeGrpcServerFS(ctx context.Context, name string) (types.WriteFS, error) {
	return &grpcServerFS{
		name: name,
	}, nil
}
