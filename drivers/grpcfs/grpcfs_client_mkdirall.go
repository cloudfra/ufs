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

package grpcfs

// MkdirAll is the [types.MkdirAllFS] half of [types.WriteFS].

import (
	"io/fs"

	"github.com/cloudfra/ufs/proto"
)

// MkdirAll creates the directory at name and any missing parents. The op it
// names an error with is "mkdir" rather than "mkdirall", which is what the two
// file systems of drivers/core report for this method, so a refused name reads
// the same whichever end of the connection refused it.
func (fsys *grpcFS) MkdirAll(name string, perm fs.FileMode) error {
	return fsys.call("mkdir", name, func() error {
		_, err := fsys.client.MkdirAll(fsys.ctx, &proto.MkdirAllRequest{Name: name, Mode: uint32(perm)})
		return err
	})
}
